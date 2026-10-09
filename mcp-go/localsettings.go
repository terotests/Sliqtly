// SPDX-License-Identifier: AGPL-3.0-or-later

// The settings of a server of one's own: /settings, a page, and the API it
// uses. Kept in the folder as settings/<name> documents.
//
//	GET  /api/settings         {"naming": {enabled, pattern, example, rule}}
//	PUT  /api/settings         the same; the rule is checked before it is kept
//	POST /api/settings/check   {"naming": {…}, "name": "…"} → {ok, key, error}
//	GET  /api/settings/network {access, allow, fixed, editable, interfaces, listening}
//	PUT  /api/settings/network {access, allow}: from this computer only
//	GET  /api/settings/listing {enabled, editable}
//	PUT  /api/settings/listing {enabled}: from this computer only
//
// Like the rest of /api/ it has no sign-in: whoever reaches the server can
// change the naming rule. Whether the decks are listed on / and /decks is
// changed only from a browser on the server's own computer, and is off
// until turned on: a deck opens by its link. Who can connect (netaccess.go) is changed only
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
	if s.env.Store == nil {
		return
	}
	if d, err := s.env.DB.Get(context.Background(), "settings", "listing"); err == nil && d != nil {
		on, _ := d["enabled"].(bool)
		s.listing.Store(on)
	}
	n, err := loadNameSettings(context.Background(), s.env.DB)
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
	case p == "/api/settings/listing":
		s.listingAPI(w, r)
	case p == "/api/settings" && r.Method == http.MethodGet:
		n, err := loadNameSettings(r.Context(), s.env.DB)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		names, count := s.offNames(r, n)
		writeJSON(w, 200, map[string]any{"naming": n, "offNames": names, "offCount": count})
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
		names, count := s.offNames(r, b.Naming)
		writeJSON(w, 200, map[string]any{"naming": b.Naming, "offNames": names, "offCount": count})
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

// whether / and /decks list the decks: settings/listing, off by default
func (s *localServer) listingAPI(w http.ResponseWriter, r *http.Request) {
	state := func() map[string]any {
		return map[string]any{"enabled": s.listing.Load(), "editable": fromHere(r)}
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, state())
	case http.MethodPut:
		if !fromHere(r) {
			writeJSON(w, 403, map[string]string{"error": "listing is changed only on the server's own computer"})
			return
		}
		var b struct {
			Enabled bool `json:"enabled"`
		}
		data, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err := json.Unmarshal(data, &b); err != nil {
			writeJSON(w, 400, map[string]string{"error": "not JSON: " + err.Error()})
			return
		}
		if err := s.env.DB.Set(r.Context(), "settings", "listing", Doc{"enabled": b.Enabled}); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		s.listing.Store(b.Enabled)
		writeJSON(w, 200, state())
	default:
		writeJSON(w, 405, map[string]string{"error": "GET or PUT"})
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
		out := map[string]any{
			"access": p.Access, "allow": p.Allow, "fixed": p.Fixed,
			"editable":   !p.Fixed && fromHere(r),
			"interfaces": ifs, "listening": s.expo.Listening(),
		}
		if s.certs != nil {
			out["certificate"] = map[string]any{"fingerprint": s.certs.fingerprint(), "https": s.httpsAddrs(ifs)}
		}
		return out
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

// the https:// addresses other computers reach the server on now
func (s *localServer) httpsAddrs(ifs []netIface) []string {
	out := []string{}
	for _, it := range ifs {
		for _, n := range it.nets {
			if s.expo.reachedOn(n.IP) && !n.IP.IsLoopback() {
				out = append(out, "https://"+net.JoinHostPort(n.IP.String(), s.expo.port)+"/")
			}
		}
	}
	return out
}

// the decks kept here that do not follow the rule: their names when the
// decks are listed, else only how many
func (s *localServer) offNames(r *http.Request, n nameSettings) ([]string, int) {
	out := []string{}
	n.Enabled = true
	rule, err := n.rule()
	if err != nil {
		return out, 0
	}
	docs, _, err := s.env.DB.WhereEq(r.Context(), "shares", "owner", s.env.LocalUser)
	if err != nil {
		return out, 0
	}
	count := 0
	for _, d := range docs {
		name, _ := d["name"].(string)
		if rule.check(name) != "" {
			count++
			out = append(out, name)
		}
	}
	if !s.listing.Load() {
		return []string{}, count
	}
	sort.Strings(out)
	return out, count
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
select { font: inherit; padding: 6px 8px; border: 1px solid var(--line); border-radius: 6px; background: var(--card); color: var(--fg); }
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

<div id="cert" hidden>
<h2>Microphones on other computers</h2>
<p class="muted">Browsers let a page use the microphone (calls, recording) only over https:// or on this computer. This server has a certificate of its own: on each other computer, open <a href="/ca">/ca</a> once, install the certificate and check that its fingerprint is this one, then open the server's https:// address.</p>
<p><code id="fp" style="font:13px/1.4 ui-monospace,monospace;word-break:break-all"></code></p>
<ul id="https"></ul>
</div>

<h2>Listing presentations</h2>
<p class="muted">Off: the front page and /decks do not list the presentations, and each one opens only by its link. On: anyone who can connect sees every presentation's name. Changed only in a browser on the server's own computer.</p>
<form id="lf">
<fieldset id="list">
<label class="inline"><input type="checkbox" id="listed"> List the presentations on the front page and /decks</label>
<button type="submit">Save</button> <span id="lsaved" aria-live="polite"></span>
</fieldset>
</form>
<p id="listnote" class="muted"></p>

<h2 id="connectors">Connectors</h2>
<p class="muted">Services outside Sliqtly that scripts and workflows in presentations may use, through this server. Each is a file in the data folder's connectors/ folder; every presentation that wants one needs the admin's grant here.</p>
<div id="conn"><p class="muted">Loading…</p></div>

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
function showOff(names, on, count) {
  const el = $("off");
  el.textContent = "";
  if ((!names || !names.length) && count) {
    const p = document.createElement("p");
    p.className = "muted";
    p.textContent = count + (count === 1 ? " presentation's name does" : " presentations' names do") + (on ? " not follow it" : " not follow it yet") + ". Their names are shown here when presentations are listed.";
    el.append(p);
    return;
  }
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
  showOff(s.offNames, s.naming.enabled, s.offCount);
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
  if (r.ok) showOff(out.offNames, out.naming.enabled, out.offCount);
});
const KIND = { wired: "wired", wifi: "Wi-Fi", cellular: "phone / mobile", virtual: "virtual", other: "other (VPN, sharing…)" };
function showNet(n) {
  for (const r of document.querySelectorAll("input[name=access]")) r.checked = r.value === n.access;
  $("allow").value = (n.allow || []).join("\n");
  showCert(n.certificate);
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
function showCert(c) {
  $("cert").hidden = !c;
  if (!c) return;
  $("fp").textContent = "SHA-256 " + c.fingerprint;
  const ul = $("https");
  ul.textContent = "";
  for (const u of c.https) { const li = document.createElement("li"), a = document.createElement("a"); a.href = u; a.textContent = u; li.append(a); ul.append(li); }
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
function showList(l) {
  $("listed").checked = l.enabled;
  $("list").disabled = !l.editable;
  $("listnote").textContent = l.editable ? "" : "Can be changed only in a browser on the server's own computer.";
}
async function loadList() {
  const r = await fetch("/api/settings/listing", { cache: "no-store" });
  if (r.ok) showList(await r.json());
}
$("lf").addEventListener("submit", async (e) => {
  e.preventDefault();
  const r = await fetch("/api/settings/listing", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ enabled: $("listed").checked }) });
  const out = await r.json();
  $("lsaved").className = r.ok ? "ok" : "bad";
  $("lsaved").textContent = r.ok ? "Saved." : out.error;
  if (r.ok) { showList(out); load(); }
});
function el(tag, text, cls) { const e = document.createElement(tag); if (text != null) e.textContent = text; if (cls) e.className = cls; return e; }
async function connApi(path, method, body) {
  const r = await fetch("/api/settings/connectors" + path, { method: method || "GET", cache: "no-store", headers: body ? { "Content-Type": "application/json" } : {}, body: body ? JSON.stringify(body) : undefined });
  let out = {};
  try { out = await r.json(); } catch (e) {}
  return { ok: r.ok, status: r.status, out };
}
function when(ms) { return new Date(ms).toLocaleString(); }
async function loadConn() {
  const box = $("conn");
  const { ok, out } = await connApi("");
  box.textContent = "";
  if (!ok) { box.append(el("p", out.error || "Not available.", "muted")); return; }
  if (!out.connectors.length) box.append(el("p", "No connectors yet. Put one in the data folder's connectors/ folder (see docs/connectors.md) and restart the server.", "muted"));
  for (const [file, why] of Object.entries(out.problems || {})) box.append(el("p", "connectors/" + file + " was left out: " + why, "bad"));
  if (out.admin && out.callback && out.connectors.some((c) => c.oauth)) {
    const p = el("p", "Callback address to register at the service: ", "muted");
    p.append(el("code", out.callback));
    box.append(p);
  }
  for (const c of out.connectors) {
    const h = el("h3", c.title);
    h.style.margin = "16px 0 4px";
    box.append(h);
    if (c.oauth) {
      const p = el("p", c.connected ? "Connected" + (c.account ? " as " + c.account : "") + "." : "Your account is not connected.");
      const b = el("button", c.connected ? "Disconnect" : "Connect " + c.title);
      b.style.marginTop = "0";
      b.onclick = async () => {
        if (c.connected) { await connApi("/" + c.id + "/connection", "DELETE", {}); loadConn(); return; }
        const r = await connApi("/" + c.id + "/connect", "POST", {});
        if (!r.ok) { p.textContent = r.out.error; p.className = "bad"; return; }
        window.open(r.out.url, "sliqtly-connect", "width=720,height=760");
      };
      box.append(p, b);
    }
    const ul = el("ul");
    for (const op of c.operations) ul.append(el("li", op.name + " (" + op.effect + ")" + (op.title ? ": " + op.title : "")));
    box.append(ul);
    if (out.admin) box.append(tryForm(c));
  }
  if (out.admin) loadGrants(box);
}
function tryForm(c) {
  const f = el("form");
  const sel = el("select");
  for (const op of c.operations) { const o = el("option", op.name); o.value = op.name; sel.append(o); }
  const args = el("textarea");
  args.rows = 2; args.spellcheck = false;
  const fill = () => {
    const op = c.operations.find((o) => o.name === sel.value);
    const ex = {};
    for (const [k, v] of Object.entries((op && op.in && op.in.properties) || {})) if ((op.in.required || []).includes(k)) ex[k] = v.type === "string" ? "" : 0;
    args.value = JSON.stringify(ex);
  };
  sel.onchange = fill; fill();
  const out = el("pre");
  out.style.cssText = "white-space:pre-wrap;font:13px/1.4 ui-monospace,monospace;max-height:320px;overflow:auto";
  const b = el("button", "Try");
  const l1 = el("label", "Try an operation (as you, without a presentation)");
  f.append(l1, sel, el("label", "Arguments (JSON)"), args, b, out);
  f.onsubmit = async (e) => {
    e.preventDefault();
    let a;
    try { a = JSON.parse(args.value || "{}"); } catch (err) { out.textContent = "Arguments are not JSON: " + err.message; return; }
    const r = await connApi("/call", "POST", { test: true, connector: c.id, op: sel.value, args: a });
    out.className = r.ok ? "" : "bad";
    out.textContent = r.ok ? JSON.stringify(r.out.result, null, 2) : (r.out.code ? r.out.code + ": " : "") + (r.out.error || r.status);
  };
  return f;
}
async function loadGrants(box) {
  const { ok, out } = await connApi("/grants");
  if (!ok) return;
  const sec = el("div");
  sec.append(el("h3", "Waiting for your approval"));
  if (!out.requests.length) sec.append(el("p", "Nothing.", "muted"));
  for (const r of out.requests) {
    const p = el("p");
    p.append(el("span", "Presentation " + r.deck + " wants " + r.connector + "." + r.op + " (" + r.count + "×, last " + when(r.last) + ") "));
    const ok = el("button", "Approve"), no = el("button", "Dismiss");
    ok.style.margin = no.style.margin = "0 6px 0 0";
    ok.onclick = async () => { await connApi("/grants", "POST", { deck: r.deck, connector: r.connector, ops: [r.op] }); loadConn(); };
    no.onclick = async () => { await connApi("/grants", "DELETE", { deck: r.deck, connector: r.connector, op: r.op }); loadConn(); };
    p.append(ok, no);
    sec.append(p);
  }
  sec.append(el("h3", "Granted"));
  if (!out.grants.length) sec.append(el("p", "Nothing yet.", "muted"));
  for (const g of out.grants) {
    const p = el("p");
    p.append(el("span", (g.deck === "*" ? "Every presentation" : "Presentation " + g.deck) + ": " + g.connector + " " + g.ops.join(", ") + " "));
    const x = el("button", "Revoke");
    x.style.margin = "0";
    x.onclick = async () => { await connApi("/grants", "DELETE", { deck: g.deck, connector: g.connector, ops: [] }); loadConn(); };
    p.append(x);
    sec.append(p);
  }
  if (out.recent && out.recent.length) {
    sec.append(el("h3", "Recent calls"));
    const t = el("table");
    t.innerHTML = "<tr><th>When</th><th>Presentation</th><th>Call</th><th>Result</th></tr>";
    for (const e of out.recent) {
      const tr = el("tr");
      for (const v of [new Date(e.at).toLocaleString(), e.deck || "", e.connector + "." + e.op, e.status + (e.remote ? " " + e.remote : "")]) tr.append(el("td", v));
      t.append(tr);
    }
    sec.append(t);
  }
  box.append(sec);
}
window.addEventListener("message", (e) => { if (e.origin === location.origin && e.data && e.data.sliqtly === "connector") loadConn(); });
loadConn();
loadNet();
loadList();
load().then(tryName);
</script>
</main></body></html>
`
