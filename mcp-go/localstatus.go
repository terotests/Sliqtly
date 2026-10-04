// SPDX-License-Identifier: AGPL-3.0-or-later

// What a server of one's own tells the pages open on it about itself:
//
//	migrating  the data folder is being brought to this version's format
//	           (datafmt.go): nothing is read or written until it is done
//	failed     that did not work; the folder is as it was, the log says why
//	ready      serving, with its version: a page that saw another version
//	           before offers to reload
//	stopping   shutting down, for an update or a restart
//
// GET /api/status answers {"state","version","message"}; /api/events sends
// the same as "event: status" when a page connects and when it changes.
// While the folder is not ready every other /api/ and /mcp request gets
// 503 with "code":"maintenance", and a page a "being updated" page that
// reloads itself when the server is ready.

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

type serverStatus struct {
	State   string `json:"state"`
	Version string `json:"version"`
	Message string `json:"message,omitempty"`
}

// the server's state, and a channel closed when it changes
type statusBoard struct {
	mu      sync.Mutex
	now     serverStatus
	changed chan struct{}
}

func newStatusBoard(state, version string) *statusBoard {
	return &statusBoard{now: serverStatus{State: state, Version: version}, changed: make(chan struct{})}
}

func (b *statusBoard) get() (serverStatus, chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.now, b.changed
}

func (b *statusBoard) set(state, message string) {
	b.mu.Lock()
	b.now.State, b.now.Message = state, message
	close(b.changed)
	b.changed = make(chan struct{})
	b.mu.Unlock()
}

func statusEvent(st serverStatus) string {
	b, _ := json.Marshal(st)
	return "event: status\ndata: " + string(b) + "\n\n"
}

func writeStatus(w http.ResponseWriter, st serverStatus, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(st)
}

// switchHandler serves through whichever handler was set last: the
// maintenance one while the folder is prepared, then the server
type switchHandler struct{ h atomic.Value }

func (s *switchHandler) set(h http.Handler) { s.h.Store(&h) }

func (s *switchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.h.Load().(*http.Handler)).ServeHTTP(w, r)
}

// maintenance answers while the data folder is not ready
func maintenance(board *statusBoard) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st, changed := board.get()
		p := r.URL.Path
		w.Header().Set("Access-Control-Allow-Origin", "*")
		switch {
		case p == "/api/status":
			writeStatus(w, st, 200)
		case p == "/api/events":
			fl, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "no events here", 500)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(200)
			io.WriteString(w, "retry: 2000\n\n"+statusEvent(st))
			fl.Flush()
			// told once more when it changes, then closed: the page
			// connects again, to the server proper when it is ready
			select {
			case <-r.Context().Done():
			case <-changed:
				st, _ = board.get()
				io.WriteString(w, statusEvent(st))
				fl.Flush()
			}
		case p == "/healthz":
			http.Error(w, st.State, http.StatusServiceUnavailable)
		case strings.HasPrefix(p, "/api/") || p == "/mcp" || (r.Method != http.MethodGet && r.Method != http.MethodHead):
			w.Header().Set("Retry-After", "5")
			msg := "Sliqtly is being updated; try again in a moment."
			if st.State == "failed" {
				msg = "Sliqtly could not update its data folder; the server's log says why."
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": "maintenance", "state": st.State})
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, maintenancePage)
		}
	})
}

const maintenancePage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sliqtly is being updated</title>
<style>
:root { color-scheme: light dark; --bg: #f6f6f4; --fg: #1d1d1b; --muted: #6b6b66; }
@media (prefers-color-scheme: dark) { :root { --bg: #141414; --fg: #ececea; --muted: #9a9a94; } }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: var(--bg); color: var(--fg); font: 16px/1.5 system-ui, sans-serif; padding: 16px; }
main { max-width: 32rem; text-align: center; }
p { color: var(--muted); }
</style></head>
<body><main>
<h1>Sliqtly is being updated</h1>
<p id="msg">Your presentations are safe. This page opens by itself when the server is ready.</p>
</main>
<script>
(async function wait() {
  try {
    const r = await fetch("/api/status", { cache: "no-store" });
    const s = await r.json();
    if (s.state === "ready") { location.reload(); return; }
    if (s.state === "failed") document.getElementById("msg").textContent = "The update could not finish. Your presentations are as they were; ask the server's administrator to look at its log.";
  } catch (_) { /* not up yet */ }
  setTimeout(wait, 2000);
})();
</script>
</body></html>
`
