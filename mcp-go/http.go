// Streamable HTTP, stateless (mcp/src/http.js): every POST gets a fresh
// server, so any instance can answer any request (Cloud Run scales out and
// back to zero). A browser opening the URL is sent to the instructions page.
// Sign-in is optional: see oauth.go.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Writes per caller: a sliding window, per instance. Enough to stop a loop,
// not a quota system.
func rateLimiter(max int, window time.Duration) func(who string) string {
	var mu sync.Mutex
	hits := map[string][]time.Time{}
	return func(who string) string {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		keep := []time.Time{}
		for _, t := range hits[who] {
			if now.Sub(t) < window {
				keep = append(keep, t)
			}
		}
		if len(keep) >= max {
			hits[who] = keep
			return "Too many presentations from here in a short time; try again in a few minutes."
		}
		hits[who] = append(keep, now)
		if len(hits) > 5000 {
			for k, v := range hits {
				if len(v) == 0 || now.Sub(v[len(v)-1]) >= window {
					delete(hits, k)
				}
			}
		}
		return ""
	}
}

type AppOpts struct {
	Store   Store
	BaseURL string
	Client  *http.Client // pictures and client metadata: public addresses only
	// the theme sheets come from the site itself (BaseURL), which may be a
	// local server in a test run; nil: a plain client
	ThemeClient *http.Client
	OAuth       *OAuth
	Limiter     func(who string) string
	TrustHost   bool
}

// The site's own addresses: the OAuth issuer and the resource follow the one
// the client used, so both domains work.
func originOf(r *http.Request, o AppOpts) string {
	host := r.Header.Get("x-forwarded-host")
	if host == "" {
		host = r.Host
	}
	host = strings.TrimSpace(strings.Split(host, ",")[0])
	https := "https://" + host
	if slices.Contains(SITES, https) || https == o.BaseURL {
		return https
	}
	if o.TrustHost && host != "" {
		proto := "http"
		if r.TLS != nil {
			proto = "https"
		}
		return proto + "://" + host
	}
	return o.BaseURL
}

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Headers", "content-type, mcp-session-id, mcp-protocol-version, authorization, last-event-id")
	h.Set("Access-Control-Expose-Headers", "mcp-session-id, mcp-protocol-version, www-authenticate")
	h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// A form or JSON body as one map, as express.json() and urlencoded() give it.
func readBody(r *http.Request) map[string]any {
	out := map[string]any{}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("content-type"))
	switch ct {
	case "application/json":
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		json.Unmarshal(b, &out)
	case "application/x-www-form-urlencoded":
		b, _ := io.ReadAll(io.LimitReader(r.Body, 100<<10))
		v, _ := url.ParseQuery(string(b))
		for k := range v {
			out[k] = v.Get(k)
		}
	}
	return out
}

// The whole server as one handler: /mcp, and with OAuth the sign-in
// endpoints.
func NewApp(o AppOpts) http.Handler {
	if o.Limiter == nil {
		o.Limiter = rateLimiter(60, 10*time.Minute)
	}
	themes := &ThemeCache{}
	themeClient := o.ThemeClient
	if themeClient == nil {
		themeClient = &http.Client{Timeout: 10 * time.Second}
	}
	mux := http.NewServeMux()

	if o.OAuth != nil {
		oa := o.OAuth
		prm := func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, oa.ResourceMetadata(originOf(r, o))) }
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", prm)
		mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", prm)
		asm := func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, oa.Metadata(originOf(r, o))) }
		mux.HandleFunc("GET /.well-known/oauth-authorization-server", asm)
		mux.HandleFunc("GET /.well-known/openid-configuration", asm)
		wrap := func(fn func(r *http.Request) (OAuthResult, error)) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				res, err := fn(r)
				switch {
				case err != nil:
					log.Printf("oauth failed: %v", err)
					writeJSON(w, 500, map[string]any{"error": "server_error"})
				case res.Redirect != "":
					http.Redirect(w, r, res.Redirect, http.StatusFound)
				case res.Text != "":
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.WriteHeader(res.Status)
					io.WriteString(w, res.Text)
				default:
					w.Header().Set("Cache-Control", "no-store")
					writeJSON(w, res.Status, res.JSON)
				}
			}
		}
		mux.HandleFunc("POST /oauth/register", wrap(func(r *http.Request) (OAuthResult, error) { return oa.Register(r.Context(), readBody(r)) }))
		mux.HandleFunc("GET /oauth/authorize", wrap(func(r *http.Request) (OAuthResult, error) {
			return oa.Authorize(r.Context(), r.URL.Query(), originOf(r, o))
		}))
		mux.HandleFunc("POST /oauth/approve", wrap(func(r *http.Request) (OAuthResult, error) { return oa.Approve(r.Context(), readBody(r)) }))
		mux.HandleFunc("POST /oauth/token", wrap(func(r *http.Request) (OAuthResult, error) { return oa.Token(r.Context(), readBody(r)) }))
	}

	server := NewServer(ServerOpts{Store: o.Store, BaseURL: o.BaseURL, Client: o.Client, ThemeClient: themeClient, Themes: themes})
	// DNS-rebinding protection is for servers on someone's own machine; this
	// one is public, and behind Cloud Run's proxy the local address may be a
	// loopback one while Host is sliqtly.com.
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 40 << 20, DisableLocalhostProtection: true})

	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && !strings.Contains(r.Header.Get("accept"), "text/event-stream") {
			http.Redirect(w, r, o.BaseURL+"/connect.html", http.StatusFound)
			return
		}
		if r.Method != "POST" {
			w.Header().Set("Allow", "POST")
			writeJSON(w, 405, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32000, "message": "Method not allowed: this server is stateless, POST only."}, "id": nil})
			return
		}
		// no token: anonymous; a token that does not hold: 401, so the client
		// refreshes it or signs in again
		ctx := r.Context()
		caller := &Caller{}
		if o.OAuth != nil {
			origin := originOf(r, o)
			user, ok, err := o.OAuth.Who(ctx, r.Header.Get("authorization"))
			if err != nil {
				log.Printf("token check failed: %v", err)
				writeJSON(w, 500, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32603, "message": "Internal error"}, "id": nil})
				return
			}
			if !ok {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_token", resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, origin))
				writeJSON(w, 401, map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": -32001, "message": "The sign-in has expired."}, "id": nil})
				return
			}
			caller.User = user
			caller.SignIn = origin + "/.well-known/oauth-protected-resource/mcp"
		}
		who := strings.TrimSpace(strings.Split(r.Header.Get("x-forwarded-for"), ",")[0])
		if who == "" {
			who, _, _ = net.SplitHostPort(r.RemoteAddr)
		}
		if caller.User != nil {
			who = "uid:" + caller.User.UID
		}
		caller.Limit = func(kind string) string {
			if kind == "create_presentation" || kind == "update_presentation" {
				return o.Limiter(who)
			}
			return ""
		}
		streamable.ServeHTTP(w, r.WithContext(withCaller(ctx, caller)))
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
