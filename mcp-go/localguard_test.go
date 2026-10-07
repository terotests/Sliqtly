package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/websocket"
)

// do sends a request with the Host and Origin headers given ("" leaves one out)
func do(t *testing.T, method, url, host, origin, ct, body string) (int, http.Header, string) {
	t.Helper()
	r, _ := http.NewRequest(method, url, strings.NewReader(body))
	if host != "" {
		r.Host = host
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(b)
}

// a page on the internet, by its own origin or by a name it points at
// 127.0.0.1, reaches only what a deck's link opens
func TestLocalGuard(t *testing.T) {
	srv, session := startLocal(t, t.TempDir(), "")
	defer srv.Close()
	defer session.Close()
	own := "http://" + strings.TrimPrefix(srv.URL, "http://")

	code, body := req(t, "POST", srv.URL+"/api/shares", "application/json", `{"name":"Deck","md":"# One\n","theme":"aurora","css":null,"deck":"d1"}`)
	eq(t, code, 201, body)
	id := regexpFind(t, body, `"id":"([A-Za-z0-9]+)"`)
	code, _, _ = do(t, "PUT", srv.URL+"/api/files/shares/"+id+"/media/x.svg", "", "", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"><script>fetch("/api/shares")</script></svg>`)
	eq(t, code, 200)
	code, _ = req(t, "PUT", srv.URL+"/api/settings/listing", "application/json", `{"enabled":true}`)
	eq(t, code, 200)

	// DNS rebinding: a name that is not this server's is answered by nothing
	for _, p := range []string{"/api/shares", "/mcp", "/", "/api/settings/network"} {
		code, _, body = do(t, "GET", srv.URL+p, "evil.example:80", "", "", "")
		eq(t, code, 421, p)
		match(t, body, `SLIQTLY_HOSTS=evil\.example`)
	}
	for _, h := range []string{"localhost:1", "127.0.0.1:9", "[::1]:9", "deck.localhost"} {
		code, _, _ = do(t, "GET", srv.URL+"/api/status", h, "", "", "")
		eq(t, code, 200, h)
	}

	// another origin: the deck by its id, its files and the page, nothing else
	evil := "https://evil.example"
	for _, c := range []struct {
		method, path, ct, body string
		want                   int
	}{
		{"GET", "/api/shares", "", "", 403},
		{"GET", "/api/collab/" + id, "", "", 403},
		{"POST", "/api/shares", "application/json", `{"name":"x","md":"# x\n"}`, 403},
		{"PATCH", "/api/shares/" + id, "application/json", `{"name":"x"}`, 403},
		{"DELETE", "/api/shares/" + id, "", "", 403},
		{"OPTIONS", "/mcp", "", "", 403},
		{"POST", "/mcp", "application/json", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, 403},
		{"PUT", "/api/settings/network", "application/json", `{"access":"network"}`, 403},
		{"PUT", "/api/settings/listing", "application/json", `{"enabled":true}`, 403},
		{"GET", "/decks", "", "", 403},
		{"GET", "/api/events", "", "", 403},
		{"GET", "/api/shares/" + id, "", "", 200},
		{"GET", "/api/status", "", "", 200},
		{"GET", "/files/shares/" + id + "/media/x.svg", "", "", 200},
		{"GET", "/s/" + id + "/overview.jpg", "", "", 200},
	} {
		for _, o := range []string{evil, "null"} {
			code, _, body = do(t, c.method, srv.URL+c.path, "", o, c.ct, c.body)
			eq(t, code, c.want, c.method+" "+c.path+" from "+o+": "+body)
		}
	}
	// the server's own page, and a program with no page at all
	code, _, _ = do(t, "GET", srv.URL+"/api/shares", "", own, "", "")
	eq(t, code, 200)
	code, _, _ = do(t, "GET", srv.URL+"/api/shares", "", "", "", "")
	eq(t, code, 200)

	// a deck's file opened by itself runs as no origin
	_, h, _ := do(t, "GET", srv.URL+"/files/shares/"+id+"/media/x.svg", "", "", "", "")
	match(t, h.Get("Content-Security-Policy"), `^sandbox allow-scripts`)

	// the stream: this server's pages only
	ws := "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/api/socket"
	if c, err := websocket.Dial(ws, "", evil); err == nil {
		c.Close()
		t.Fatal("a page elsewhere opened the stream")
	}
	c, err := websocket.Dial(ws, "", own)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func regexpFind(t *testing.T, s, re string) string {
	t.Helper()
	m := regexp.MustCompile(re).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("%q not in %s", re, s)
	}
	return m[1]
}

// the caller is read from the right of X-Forwarded-For, past our proxies
func TestClientIP(t *testing.T) {
	for _, c := range []struct{ peer, xff, want string }{
		{"203.0.113.7:5000", "", "203.0.113.7"},
		// Cloud Run: the front end connects from a link-local address and
		// adds the client it was reached from
		{"169.254.1.1:5000", "198.51.100.4", "198.51.100.4"},
		// whatever the client wrote in front stays its own text
		{"169.254.1.1:5000", "1.2.3.4, 198.51.100.4", "198.51.100.4"},
		{"169.254.1.1:5000", "not-an-ip, 1.2.3.4, 198.51.100.4", "198.51.100.4"},
		// through Hosting: Google's front end is a hop, not the caller
		{"169.254.1.1:5000", "6.6.6.6, 198.51.100.4, 35.191.10.20", "198.51.100.4"},
		// a direct connection that sends the header is itself the caller
		{"203.0.113.7:5000", "1.2.3.4", "203.0.113.7"},
		// only proxies: the farthest of them
		{"127.0.0.1:5000", "10.0.0.2", "10.0.0.2"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		eq(t, clientIP(r), c.want, c.peer+" "+c.xff)
	}
}

// a PNG whose header claims a huge picture is refused before it is decoded
func TestPictureTooLarge(t *testing.T) {
	png := func(w, h uint32) []byte {
		var b bytes.Buffer
		b.WriteString("\x89PNG\r\n\x1a\n")
		ihdr := make([]byte, 13)
		binary.BigEndian.PutUint32(ihdr[0:], w)
		binary.BigEndian.PutUint32(ihdr[4:], h)
		ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
		chunk := func(kind string, data []byte) {
			binary.Write(&b, binary.BigEndian, uint32(len(data)))
			b.WriteString(kind)
			b.Write(data)
			binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))
		}
		chunk("IHDR", ihdr)
		chunk("IEND", nil)
		return b.Bytes()
	}
	_, err := decodePicture(png(100000, 100000))
	if err == nil || !strings.Contains(err.Error(), "100000×100000") {
		t.Fatalf("a 10-gigapixel picture: %v", err)
	}
	h := &McpHost{images: map[int64][]byte{1: png(100000, 100000)}}
	match(t, h.ImageError(1), `100000×100000`)
	eq(t, len(h.ImageGrid(1)), 0)
}

// of two requests taking one code, one gets it
func TestTakeOnce(t *testing.T) {
	ctx := context.Background()
	db, _, err := newFSStore(t.TempDir(), "local")
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 20; round++ {
		db.Set(ctx, "codes", "c", Doc{"v": "x"})
		var wg sync.WaitGroup
		var mu sync.Mutex
		got := 0
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				d, err := db.Take(ctx, "codes", "c")
				if err != nil {
					t.Error(err)
				}
				if d != nil {
					mu.Lock()
					got++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		eq(t, got, 1)
	}
	if d, _ := db.Get(ctx, "codes", "c"); d != nil {
		t.Fatal("still there")
	}
}

func TestPublicIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "169.254.169.254": false, "0.1.2.3": false,
		"100.64.0.1": false, "198.18.0.1": false, "192.0.0.8": false, "240.0.0.1": false,
		"::1": false, "fd00::1": false, "64:ff9b::a00:1": false, "2002:a00:1::": false,
		"::ffff:10.0.0.1": false, "2001:db8::1": false,
	} {
		eq(t, publicIP(net.ParseIP(ip)), want, ip)
	}
}

// behind a proxy that hands requests on as localhost, a page at the name
// set in SLIQTLY_URL is still the server's own
func TestGuardBehindProxy(t *testing.T) {
	g := newHostGuard("https://decks.example.org", true)
	r := httptest.NewRequest("POST", "http://localhost:8080/api/shares", nil)
	r.Header.Set("Origin", "https://decks.example.org")
	eq(t, g.sameOrigin(r), true)
	eq(t, g.hostOK("decks.example.org"), true)
	r.Header.Set("Origin", "https://evil.example")
	eq(t, g.sameOrigin(r), false)
	// the default address is no name of the server's own for origins
	g = newHostGuard("http://localhost:8080", false)
	r.Header.Set("Origin", "http://localhost:8080")
	r.Host = "127.0.0.1:8080"
	eq(t, g.sameOrigin(r), false)
}
