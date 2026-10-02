// Checks on what a model sends, before anything is stored (mcp/src/deck.js).

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var THEMES = []string{"aurora", "nebula", "carbon", "ember", "midnight", "corporate", "editorial"}

const (
	MAX_MD     = 300 * 1024
	MAX_CSS    = 100 * 1024
	MAX_IMAGE  = 5 * 1024 * 1024
	MAX_IMAGES = 20
)

var imageTypes = map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp", "svg": "image/svg+xml"}

// InputError is a mistake in what the model sent: its message goes back as
// the tool's answer.
type InputError struct{ msg string }

func (e *InputError) Error() string { return e.msg }

func inputErr(format string, a ...any) error { return &InputError{fmt.Sprintf(format, a...)} }

var (
	reSpaces   = regexp.MustCompile(`\s+`)
	reBadChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)
	reExt      = regexp.MustCompile(`\.([A-Za-z0-9]+)$`)
	reGivenTyp = regexp.MustCompile(`^image/(png|jpeg|gif|webp|svg\+xml)$`)
	reFence    = regexp.MustCompile(`^\s*(` + "```+|~~~+" + `)`)
	reHeading  = regexp.MustCompile(`^(#{1,2})\s+(.*)$`)
	reAttrs    = regexp.MustCompile(`\s*\{[^}]*\}\s*$`)
	reMedia    = regexp.MustCompile(`media/([A-Za-z0-9._-]+)`)
	reNewline  = regexp.MustCompile(`\r?\n`)
	reDataURL  = regexp.MustCompile(`^data:([^;,]+);base64,`)
)

// "team photo.JPG" → "team-photo.jpg"; "" when nothing usable is left
func cleanName(name string) string {
	s := strings.TrimPrefix(strings.TrimSpace(name), "media/")
	s = reBadChars.ReplaceAllString(reSpaces.ReplaceAllString(s, "-"), "")
	if s == "" || strings.HasPrefix(s, ".") || len(s) > 80 {
		return ""
	}
	return reExt.ReplaceAllStringFunc(s, strings.ToLower)
}

func typeOf(name, given string) string {
	if given != "" && reGivenTyp.MatchString(given) {
		return given
	}
	m := regexp.MustCompile(`\.([a-z0-9]+)$`).FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return imageTypes[m[1]]
}

// What the slides are: titles of `#` / `##` headings outside fences, and the
// media/… the text points at.
func outline(md string) (titles []string, media []string) {
	seen := map[string]bool{}
	fence := ""
	for _, line := range reNewline.Split(md, -1) {
		if f := reFence.FindStringSubmatch(line); f != nil {
			if fence == "" {
				fence = f[1]
			} else if f[1][0] == fence[0] && len(f[1]) >= len(fence) {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if h := reHeading.FindStringSubmatch(line); h != nil {
			titles = append(titles, strings.TrimSpace(reAttrs.ReplaceAllString(h[2], "")))
		}
		for _, m := range reMedia.FindAllStringSubmatch(line, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				media = append(media, m[1])
			}
		}
	}
	return
}

// Notes for the model: pictures the text names but nobody sent, and the
// other way round.
func warnings(md string, imageNames, storedNames []string) []string {
	_, media := outline(md)
	have := map[string]bool{}
	for _, n := range append(append([]string{}, imageNames...), storedNames...) {
		have[n] = true
	}
	inMedia := map[string]bool{}
	out := []string{}
	for _, m := range media {
		inMedia[m] = true
		if !have[m] {
			out = append(out, fmt.Sprintf("media/%s is used in the Markdown but no image by that name was sent.", m))
		}
	}
	for _, n := range imageNames {
		if !inMedia[n] {
			out = append(out, fmt.Sprintf("Image %s was sent but the Markdown does not use media/%s.", n, n))
		}
	}
	return out
}

func privateHost(host string) bool {
	h := strings.Trim(strings.ToLower(host), "[]")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".internal") || strings.HasSuffix(h, ".local") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return !publicIP(ip)
	}
	return h == "::1" || strings.HasPrefix(h, "fc") || strings.HasPrefix(h, "fd") || strings.HasPrefix(h, "fe80")
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast))
}

// The client that fetches pictures and client metadata: besides the name
// check above, it refuses to connect to an address that is not public, so a
// public name pointing at 10.x or the metadata server is refused too.
func newPublicClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return fmt.Errorf("refusing to connect to %s", host)
			}
			return nil
		},
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = dialer.DialContext
	tr.Proxy = nil
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" || privateHost(req.URL.Hostname()) {
			return errors.New("redirect to a non-public address")
		}
		return nil
	}}
}

type imageArg struct {
	Name       string `json:"name"`
	URL        string `json:"url,omitempty"`
	DataBase64 string `json:"data_base64,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
}

type image struct {
	Name, Type string
	Data       []byte
}

// The pictures, fetched or decoded.
func loadImages(ctx context.Context, list []imageArg, client *http.Client) ([]image, error) {
	if len(list) == 0 {
		return nil, nil
	}
	if len(list) > MAX_IMAGES {
		return nil, inputErr("At most %d images per call.", MAX_IMAGES)
	}
	out := []image{}
	for _, img := range list {
		name := cleanName(img.Name)
		if name == "" {
			return nil, inputErr(`Image name "%s" is not usable: use letters, digits, ".", "-" and "_".`, img.Name)
		}
		var data []byte
		typ := typeOf(name, img.MimeType)
		switch {
		case img.DataBase64 != "":
			b64 := img.DataBase64
			if m := reDataURL.FindStringSubmatch(b64); m != nil {
				if typ == "" {
					typ = typeOf(name, m[1])
				}
				b64 = b64[len(m[0]):]
			}
			data = decodeBase64(b64)
		case img.URL != "":
			u, err := url.Parse(img.URL)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return nil, inputErr(`Image %s: "%s" is not a URL.`, name, img.URL)
			}
			if u.Scheme != "https" || privateHost(u.Hostname()) {
				return nil, inputErr("Image %s: only public https URLs are fetched.", name)
			}
			rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(rctx, "GET", u.String(), nil)
			req.Header.Set("user-agent", "Sliqtly-MCP/1.0")
			res, err := client.Do(req)
			if err != nil {
				return nil, inputErr("Image %s: %s could not be fetched.", name, u.Hostname())
			}
			defer res.Body.Close()
			if res.StatusCode < 200 || res.StatusCode > 299 {
				return nil, inputErr("Image %s: %s answered %d.", name, u.Hostname(), res.StatusCode)
			}
			ct := strings.TrimSpace(strings.Split(res.Header.Get("content-type"), ";")[0])
			if ct != "" && !strings.HasPrefix(ct, "image/") {
				return nil, inputErr("Image %s: the URL gave %s, not a picture.", name, ct)
			}
			if n, _ := strconv.Atoi(res.Header.Get("content-length")); n > MAX_IMAGE {
				return nil, inputErr("Image %s is larger than 5 MB.", name)
			}
			data, err = io.ReadAll(io.LimitReader(res.Body, MAX_IMAGE+1))
			if err != nil {
				return nil, inputErr("Image %s: %s could not be fetched.", name, u.Hostname())
			}
			if typ == "" {
				typ = typeOf(name, ct)
			}
		default:
			return nil, inputErr("Image %s: give either url or data_base64.", name)
		}
		if len(data) == 0 {
			return nil, inputErr("Image %s is empty.", name)
		}
		if len(data) > MAX_IMAGE {
			return nil, inputErr("Image %s is larger than 5 MB.", name)
		}
		if typ == "" {
			return nil, inputErr("Image %s: unknown picture type; name it .png, .jpg, .gif, .webp or .svg.", name)
		}
		out = append(out, image{Name: name, Type: typ, Data: data})
	}
	return out, nil
}

// Node's Buffer.from(s, "base64") is lenient: either alphabet, padding or
// not, and it stops at the first character it does not know.
func decodeBase64(s string) []byte {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '-':
			return '+'
		case r == '_':
			return '/'
		case r == ' ' || r == '\n' || r == '\r' || r == '\t':
			return -1
		}
		return r
	}, s)
	if i := strings.IndexByte(s, '='); i >= 0 {
		s = s[:i]
	}
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}
