// SPDX-License-Identifier: AGPL-3.0-or-later

// The settings of a server of one's own: /settings, a page, and the API it
// uses. Kept in the folder as settings/<name> documents.
//
//	GET  /api/settings         {"naming": {enabled, pattern, example, rule}}
//	PUT  /api/settings         the same; the rule is checked before it is kept
//	POST /api/settings/check   {"naming": {…}, "name": "…"} → {ok, key, error}
//	GET  /api/settings/network {access, allow, fixed, editable, interfaces, listening}
//	PUT  /api/settings/network {access, allow}: from this computer only
//
// Like the rest of /api/ it has no sign-in: whoever reaches the server can
// change the naming rule. Who can connect (netaccess.go) is changed only
// from a browser on the server's own computer, and not at all when
// SLIQTLY_LISTEN or SLIQTLY_ALLOW set it. A write must be JSON, which a page
// on another site cannot send without asking first, and is not answered.

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
)

// the naming rule from the folder, into the env the tools read
func (s *localServer) loadSettings() {
	db, ok := s.env.DB.(*fsDB)
	if !ok {
		return
	}
	n, err := loadNameSettings(context.Background(), db)
	if err != nil {
		return
	}
	if r, err := n.rule(); err == nil {
		s.env.names.Store(r)
	}
}

type settingsBody struct {
	Naming nameSettings `json:"naming"`
	Name   string       `json:"name,omitempty"`
}

func (s *localServer) settingsAPI(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if r.Method != http.MethodGet {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeJSON(w, 415, map[string]string{"error": "send JSON"})
			return
		}
	}
	read := func() (settingsBody, bool) {
		var b settingsBody
		b.Naming = defaultNames
		data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err := json.Unmarshal(data, &b); err != nil {
			writeJSON(w, 400, map[string]string{"error": "not JSON: " + err.Error()})
			return b, false
		}
		return b, true
	}
	switch {
	case p == "/api/settings/network":
		s.networkAPI(w, r)
	case p == "/api/settings" && r.Method == http.MethodGet:
		n, err := loadNameSettings(r.Context(), s.env.DB)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"naming": n, "offNames": s.offNames(r, n)})
	case p == "/api/settings" && r.Method == http.MethodPut:
		b, ok := read()
		if !ok {
			return
		}
		rule, err := b.Naming.rule()
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		if err := saveNameSettings(r.Context(), s.env.DB, b.Naming); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		s.env.names.Store(rule)
		writeJSON(w, 200, map[string]any{"naming": b.Naming, "offNames": s.offNames(r, b.Naming)})
	case p == "/api/settings/check" && r.Method == http.MethodPost:
		b, ok := read()
		if !ok {
			return
		}
		b.Naming.Enabled = true
		rule, err := b.Naming.rule()
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		if why := rule.check(b.Name); why != "" {
			writeJSON(w, 200, map[string]any{"ok": false, "error": why})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "key": rule.key(b.Name)})
	default:
		writeJSON(w, 404, map[string]string{"error": "no such call"})
	}
}

// who can connect: settings/network, or local
func loadNetPolicy(ctx context.Context, db DB) (*netPolicy, error) {
	d, err := db.Get(ctx, "settings", "network")
	if err != nil {
		return nil, err
	}
	if d == nil {
		return newNetPolicy(accessLocal, nil, false)
	}
	access, _ := d["access"].(string)
	allow := []string{}
	if list, ok := d["allow"].([]any); ok {
		for _, a := range list {
			if s, ok := a.(string); ok {
				allow = append(allow, s)
			}
		}
	}
	return newNetPolicy(access, allow, false)
}

// fromHere: the request came from this computer itself
func fromHere(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *localServer) networkAPI(w http.ResponseWriter, r *http.Request) {
	if s.expo == nil {
		writeJSON(w, 404, map[string]string{"error": "this server's connections are not set here"})
		return
	}
	state := func() map[string]any {
		p := s.expo.policy.Load()
		ifs := *s.expo.ifaces.Load()
		return map[string]any{
			"access": p.Access, "allow": p.Allow, "fixed": p.Fixed,
			"editable":   !p.Fixed && fromHere(r),
			"interfaces": ifs, "listening": s.expo.Listening(),
		}
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, state())
	case http.MethodPut:
		if s.expo.policy.Load().Fixed {
			writeJSON(w, 409, map[string]string{"error": "SLIQTLY_LISTEN or SLIQTLY_ALLOW sets who can connect to this server; change it there"})
			return
		}
		if !fromHere(r) {
			writeJSON(w, 403, map[string]string{"error": "who can connect is changed only on the server's own computer"})
			return
		}
		var b struct {
			Access string   `json:"access"`
			Allow  []string `json:"allow"`
		}
		data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err := json.Unmarshal(data, &b); err != nil {
			writeJSON(w, 400, map[string]string{"error": "not JSON: " + err.Error()})
			return
		}
		p, err := newNetPolicy(b.Access, b.Allow, false)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		allow := []any{}
		for _, a := range p.Allow {
			allow = append(allow, a)
		}
		if err := s.env.DB.Set(r.Context(), "settings", "network", Doc{"access": p.Access, "allow": allow}); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		s.expo.setPolicy(p)
		s.expo.sync(false)
		writeJSON(w, 200, state())
	default:
		writeJSON(w, 405, map[string]string{"error": "GET or PUT"})
	}
}

// the names of the decks kept here that do not follow the rule
func (s *localServer) offNames(r *http.Request, n nameSettings) []string {
	out := []string{}
	n.Enabled = true
	rule, err := n.rule()
	if err != nil {
		return out
	}
	docs, _, err := s.env.DB.WhereEq(r.Context(), "shares", "owner", s.env.LocalUser)
	if err != nil {
		return out
	}
	for _, d := range docs {
		name, _ := d["name"].(string)
		if rule.check(name) != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (s *localServer) settingsPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, settingsHTML)
}

const settingsHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sliqtly settings</title>
<style>
:root { color-scheme: light dark; --bg: #f6f6f4; --fg: #1d1d1b; --muted: #6b6b66; --card: #fff; --line: #e2e2dc; --ok: #1d7a3a; --bad: #b3261e; }
@media (prefers-color-scheme: dark) { :root { --bg: #141414; --fg: #ececea; --muted: #9a9a94; --card: #1e1e1e; --line: #333; --ok: #6fcf8a; --bad: #f2867d; } }
body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.5 system-ui, sans-serif; }
main { max-width: 720px; margin: 0 auto; padding: 24px 16px 64px; }
h1 { font-size: 1.5rem; margin: 0 0 4px; }
h2 { font-size: 1.15rem; margin: 24px 0 4px; }
.muted { color: var(--muted); font-size: .9rem; }
a { color: inherit; }
label { display: block; margin: 14px 0 4px; font-weight: 600; }
label.inline { display: flex; gap: 8px; align-items: center; font-weight: 600; }
label.opt { font-weight: 400; align-items: baseline; }
textarea { width: 100%; box-sizing: border-box; font: 14px/1.4 ui-monospace, monospace; padding: 8px 10px; border: 1px solid var(--line); border-radius: 6px; background: var(--card); color: var(--fg); }
table { border-collapse: collapse; font-size: .9rem; margin-top: 8px; }
td, th { text-align: left; padding: 4px 12px 4px 0; border-bottom: 1px solid var(--line); }
input[type=text] { width: 100%; box-sizing: border-box; font: inherit; padding: 8px 10px; border: 1px solid var(--line); border-radius: 6px; background: var(--card); color: var(--fg); }
input.mono { font-family: ui-monospace, monospace; }
button { font: inherit; padding: 8px 16px; border-radius: 6px; border: 1px solid var(--line); background: var(--fg); color: var(--bg); cursor: pointer; margin-top: 18px; }
.ok { color: var(--ok); } .bad { color: var(--bad); }
fieldset { border: 0; padding: 0; margin: 0; }
fieldset:disabled { opacity: .55; }
ul { padding-left: 20px; }
</style></head><body><main>
<p class="muted"><a href="/decks">Presentations</a> · <a href="/">Editor</a></p>
<h1>Settings</h1>
<p class="muted">For this server.</p>

<h2>Who can connect</h2>
<p class="muted">The server, its MCP endpoint and its presentations. Anyone who can connect can read and change them.</p>
<form id="nf">
<fieldset id="net">
<label class="inline opt"><input type="radio" name="access" value="local"> This computer only</label>
<label class="inline opt"><input type="radio" name="access" value="wired"> Also computers on a wired network (Ethernet). Never over Wi-Fi, a phone's connection or a VPN.</label>
<label class="inline opt"><input type="radio" name="access" value="network"> Every network. For a server, not a laptop.</label>
<label for="allow">Only these address ranges (optional, one per line, e.g. 10.20.0.0/16 for the office)</label>
<textarea id="allow" rows="3" spellcheck="false"></textarea>
<button type="submit">Save</button> <span id="nsaved" aria-live="polite"></span>
</fieldset>
</form>
<p id="netnote" class="muted"></p>
<div id="ifs"></div>

<h2>Names of presentations</h2>
<p class="muted" style="margin-top:0">Whoever can connect can change this.</p>

<p class="muted">Require a form for every name an assistant gives, for example a ticket key first. Assistants are told the rule, a name that does not follow it is refused, and the key is listed apart from the name for searching.</p>
<form id="f">
<label class="inline"><input type="checkbox" id="enabled"> Require this form</label>
<fieldset id="rule">
<label for="pattern">Pattern (a regular expression; its first group is the key)</label>
<input type="text" id="pattern" class="mono" spellcheck="false" autocomplete="off">
<label for="example">Example name</label>
<input type="text" id="example" autocomplete="off">
<label for="text">The rule in words, for assistants</label>
<input type="text" id="text" autocomplete="off">
<label for="try">Try a name</label>
<input type="text" id="try" autocomplete="off" placeholder="ABC-1234 Quarterly review">
<p id="tried" class="muted" aria-live="polite"></p>
</fieldset>
<button type="submit">Save</button> <span id="saved" aria-live="polite"></span>
</form>
<div id="off"></div>

<script>
const $ = (id) => document.getElementById(id);
const form = () => ({ enabled: $("enabled").checked, pattern: $("pattern").value, example: $("example").value, rule: $("text").value });
function showOff(names, on) {
  const el = $("off");
  el.textContent = "";
  if (!names || !names.length) return;
  const h = document.createElement("h2");
  h.textContent = on ? "Presentations whose names do not follow it" : "Presentations whose names would not follow it";
  const p = document.createElement("p");
  p.className = "muted";
  p.textContent = "They stay as they are; rename them in the editor or ask an assistant.";
  const ul = document.createElement("ul");
  for (const n of names) { const li = document.createElement("li"); li.textContent = n || "(untitled)"; ul.append(li); }
  el.append(h, p, ul);
}
async function load() {
  const r = await fetch("/api/settings", { cache: "no-store" });
  const s = await r.json();
  $("enabled").checked = s.naming.enabled;
  $("pattern").value = s.naming.pattern;
  $("example").value = s.naming.example;
  $("text").value = s.naming.rule;
  $("rule").disabled = !s.naming.enabled;
  showOff(s.offNames, s.naming.enabled);
}
let timer = 0;
async function tryName() {
  const name = $("try").value || $("example").value;
  const r = await fetch("/api/settings/check", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ naming: form(), name }) });
  const out = await r.json();
  const el = $("tried");
  el.className = out.ok ? "ok" : "bad";
  el.textContent = out.ok ? "Follows the rule. Key: " + (out.key || "(none)") : out.error;
}
for (const id of ["pattern", "example", "text", "try"]) $(id).addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(tryName, 250); });
$("enabled").addEventListener("change", () => { $("rule").disabled = !$("enabled").checked; });
$("f").addEventListener("submit", async (e) => {
  e.preventDefault();
  const r = await fetch("/api/settings", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ naming: form() }) });
  const out = await r.json();
  $("saved").className = r.ok ? "ok" : "bad";
  $("saved").textContent = r.ok ? "Saved." : out.error;
  if (r.ok) showOff(out.offNames, out.naming.enabled);
});
const KIND = { wired: "wired", wifi: "Wi-Fi", cellular: "phone / mobile", virtual: "virtual", other: "other (VPN, sharing…)" };
function showNet(n) {
  for (const r of document.querySelectorAll("input[name=access]")) r.checked = r.value === n.access;
  $("allow").value = (n.allow || []).join("\n");
  $("net").disabled = !n.editable;
  $("netnote").textContent = n.fixed
    ? "Set by SLIQTLY_LISTEN / SLIQTLY_ALLOW where the server is started; change it there."
    : n.editable ? "Listening on " + n.listening.join(", ") + "." : "Can be changed only in a browser on the server's own computer. Listening on " + n.listening.join(", ") + ".";
  const el = $("ifs");
  el.textContent = "";
  if (!n.interfaces || !n.interfaces.length) return;
  const t = document.createElement("table");
  t.innerHTML = "<tr><th>Interface</th><th>Kind</th><th>Addresses</th></tr>";
  for (const i of n.interfaces) {
    const tr = document.createElement("tr");
    for (const v of [i.name, KIND[i.kind] || i.kind, i.addresses.join(", ")]) { const td = document.createElement("td"); td.textContent = v; tr.append(td); }
    t.append(tr);
  }
  el.append(t);
}
async function loadNet() {
  const r = await fetch("/api/settings/network", { cache: "no-store" });
  if (r.ok) showNet(await r.json()); else $("netnote").textContent = "";
}
$("nf").addEventListener("submit", async (e) => {
  e.preventDefault();
  const access = document.querySelector("input[name=access]:checked")?.value || "local";
  const allow = $("allow").value.split(/[\s,]+/).filter(Boolean);
  const r = await fetch("/api/settings/network", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ access, allow }) });
  const out = await r.json();
  $("nsaved").className = r.ok ? "ok" : "bad";
  $("nsaved").textContent = r.ok ? "Saved." : out.error;
  if (r.ok) showNet(out);
});
loadNet();
load().then(tryName);
</script>
</main></body></html>
`
