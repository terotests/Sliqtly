// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden over HTTP (warden.go has the operations):
//
//	GET  /apps/warden/login       the sign-in page (the editor's, in
//	                              Warden's words); signed in: to the page
//	POST /apps/warden/api/<op>    the page's calls, signed in with the
//	                              editor's cookie (editor.go)
//	POST /apps/warden/mcp         Warden's own MCP server: an assistant
//	                              connects to it as its own connector,
//	                              signed in with the site's OAuth (the
//	                              tokens /mcp's sign-in gives, for this
//	                              resource)
//	GET  /.well-known/oauth-protected-resource/apps/warden/mcp   RFC 9728
//
// The page itself is static (Hosting, /apps/warden/).

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	wardenPath     = "/apps/warden"
	wardenMcpPath  = wardenPath + "/mcp"
	wardenResource = "/.well-known/oauth-protected-resource" + wardenMcpPath
)

// calls per account in ten minutes: the page's and an assistant's together
var wardenLimit = rateLimiter(600, 10*time.Minute)

// The open page asks every few seconds whether its space changed (an
// assistant wrote to it through the MCP server, or another member did):
// POST api/changes {space_id} answers {changed, mine}. It has a limit of its
// own, so asking does not use up the calls that change things. The page
// names itself in X-Warden-Client, so a change it made itself is "mine".
const wardenChangesOp = "changes"

var wardenChangesLimit = rateLimiter(400, 10*time.Minute)

// ------------------------------------------------------------------ tools --

var wardenEventProps = map[string]any{
	"title":    strProp("What happens"),
	"date":     strProp("The day, YYYY-MM-DD, in the space's own time zone"),
	"end_date": strProp("All-day events that span days: the last day, YYYY-MM-DD (inclusive)"),
	"start":    strProp("Start time HH:MM (24 h, the space's time zone); leave out for an all-day event"),
	"end":      strProp("End time HH:MM; an hour after start when left out"),
	"ward":     strProp("Who or what the event is about: a ward's id or name; a new name makes a new ward"),
	"place":    strProp("Where"),
	"notes":    strProp("Anything else, plain text"),
	"color":    strProp("blue, green, red, orange, purple, teal, pink, yellow, gray or #rrggbb"),
	"rrule":    strProp("Repeats: an iCalendar RRULE with FREQ=DAILY|WEEKLY|MONTHLY and optionally INTERVAL, BYDAY (MO,TU…), UNTIL (YYYYMMDD), COUNT"),
	"exdates":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Days a repeating event skips, YYYY-MM-DD"},
	"who": map[string]any{"type": "array", "description": "Who is responsible: e.g. [{name: \"Aino\", role: \"takes\"}, {name: \"Leo\", role: \"picks up\"}]",
		"items": map[string]any{"type": "object", "properties": map[string]any{"name": strProp("A member's or another person's name"), "role": strProp("What they do: takes, picks up, looks after…")}, "required": []string{"name"}}},
	"important": map[string]any{"type": "boolean", "description": "Marks an event that must not be missed"},
	"source":    strProp("Where it came from, e.g. \"Google Calendar\" or \"school\""),
	"ext_id":    strProp("The event's id where it came from (an iCalendar UID): importing it again changes it instead of adding it twice"),
	"type":      strProp("The entry's type, from list_entry_types: event (the default) or e.g. birthday_invitation, activity, activity_occurrence, school_event, school_timetable, school_exception_day, doctor_visit, trip, bill, purchase, need, gift_idea, repair, home_task, reminder, schedule_exception, contact_task, custom_family_event. A record type (purchase, need, gift_idea, item, contact…) needs no date; each type says what it requires"),
	"fields":    map[string]any{"type": "object", "description": "The type's own fields and the common ones (status, people, responsible, due, get_from and get_by: the days between which something is to be got or bought, tags, tasks, links, estimated_cost, actual_cost, payer, budget_scope…), e.g. a birthday_invitation's {birthday_child, rsvp: \"going\", rsvp_by, takes, gift_idea}; a list field takes an array of texts; given fields are merged into those the entry has, a field set to null is removed"},
	"cost": map[string]any{"type": "object", "description": "What it costs, for the space's budget; null removes it",
		"properties": map[string]any{
			"amount":       map[string]any{"type": "number", "description": "Euros"},
			"payer":        strProp("Who pays"),
			"scope":        strProp("shared (the family budget, the default) or personal (the payer's own budget; still listed in the group's spending). Whether a thing is the family's varies: a shared storage is, a parent's mattress or electricity contract is not"),
			"instalments":  map[string]any{"type": "integer", "description": "Paid in this many parts (default 1)"},
			"first":        strProp("The first payment's day, YYYY-MM-DD; the entry's date by default"),
			"every_months": map[string]any{"type": "integer", "description": "Months between parts (default 1)"},
			"remind_days":  map[string]any{"type": "integer", "description": "Days before each payment day to remind of it (default 3 for a cost in parts, 0 for none)"},
		}, "required": []string{"amount"}},
}

func wardenWith(props map[string]any, more map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range props {
		out[k] = v
	}
	for k, v := range more {
		out[k] = v
	}
	return out
}

var spaceIDProp = strProp("The space's id (list_spaces)")

var wardenTools = []roomTool{
	{name: "list_spaces", title: "List spaces", readOnly: true,
		desc: "The Warden spaces you are a member of: each with its members, pending invites and wards (the people or things the group is responsible for: children, pets, a car, clients, players). Start here."},
	{name: "create_space", title: "Create a space",
		desc:     "A new shared space; you are its owner. vocab picks the words the page uses: demo, family, business or sports.",
		props:    map[string]any{"name": strProp("The space's name"), "vocab": strProp("demo, family, business or sports"), "lang": strProp("fi or en: the page's language")},
		required: []string{"name"}},
	{name: "get_space", title: "Get a space", readOnly: true,
		desc:     "A space with all its events.",
		props:    map[string]any{"space_id": spaceIDProp},
		required: []string{"space_id"}},
	{name: "list_events", title: "List events", readOnly: true,
		desc:     "A space's events that may happen between from and to (repeating ones are listed once, with their rrule).",
		props:    map[string]any{"space_id": spaceIDProp, "from": strProp("First day, YYYY-MM-DD"), "to": strProp("Last day, YYYY-MM-DD"), "important_only": map[string]any{"type": "boolean", "description": "Only events marked important"}, "type": strProp("Only entries of this type")},
		required: []string{"space_id"}},
	{name: "list_entry_types", title: "List entry types", readOnly: true,
		desc:  "The kinds of entries a space keeps besides plain events (for a family: invitations and birthdays, hobbies, school, daycare and care, health, travel and travel cards, bills and purchases, gift ideas, repairs and home tasks, reminders, changes to the routine…), each with its group, its fields, the common fields every entry also takes (also_fields: the window to get it in get_from/get_by, estimates estimated_cost_min/max, actual_cost, budget_scope family or own, budget_owner), what it requires, the money fields that go to the budget and the dates that make reminders; the fields common to every entry; and how a cost is given. Read this before saving a typed entry.",
		props: map[string]any{"space_id": strProp("The space's id: only the types its vocabulary offers")}},
	{name: "budget", title: "Budget", readOnly: true,
		desc:     "The costs of a space's entries month by month: each payment (an instalment of a cost), the totals, the family's shared part, the personal part and each payer's part. Budgets are personal: give person for one member's budget, their own costs and the family's shared ones.",
		props:    map[string]any{"space_id": spaceIDProp, "from": strProp("First month, YYYY-MM (this month by default)"), "to": strProp("Last month, YYYY-MM (a year on by default)"), "person": strProp("One member's budget: their personal costs and the shared ones"), "payer": strProp("Only the costs this one pays")},
		required: []string{"space_id"}},
	{name: "import_events", title: "Import events",
		desc: "Bring events into a space, e.g. the important ones from the user's own calendar, a school's or a club's schedule, or an email. Give each its ext_id (the source's event id) so importing again updates instead of duplicating. Name the ward each event concerns and who is responsible (who). Times are the space's local time. All events are checked before any is saved.",
		props: map[string]any{"space_id": spaceIDProp, "events": map[string]any{"type": "array", "description": "At most 200 events",
			"items": map[string]any{"type": "object", "properties": wardenEventProps, "required": []string{"title", "date"}}}},
		required: []string{"space_id", "events"}},
	{name: "import_ics", title: "Import an iCalendar file",
		desc:     "Bring the events of an iCalendar (.ics) text into a space. Times with a zone are turned into the space's own; UIDs keep a second import from duplicating; cancelled events are left out.",
		props:    map[string]any{"space_id": spaceIDProp, "ics": strProp("The iCalendar text, BEGIN:VCALENDAR … END:VCALENDAR"), "ward": strProp("A ward's id or name for all of them"), "important": map[string]any{"type": "boolean", "description": "Mark all of them important"}, "source": strProp("Where they came from")},
		required: []string{"space_id", "ics"}},
	{name: "save_event", title: "Save an event",
		desc:     "Make one entry (an event or a typed entry, see list_entry_types), or change one: give event_id to change it (only the fields given change). From a photo or a text the user gives (an invitation card, a school letter, a receipt), read the details and save them with the fitting type.",
		props:    wardenWith(wardenEventProps, map[string]any{"space_id": spaceIDProp, "event_id": strProp("The event to change; leave out to make a new one")}),
		required: []string{"space_id"}},
	{name: "delete_event", title: "Delete an event", destructive: true,
		props:    map[string]any{"space_id": spaceIDProp, "event_id": strProp("The event")},
		desc:     "Delete one event or entry of a space.",
		required: []string{"space_id", "event_id"}},
	{name: "add_ward", title: "Add a ward",
		desc:     "A new ward: someone or something the group is responsible for (a child, a pet, a car, a client, a player).",
		props:    map[string]any{"space_id": spaceIDProp, "name": strProp("Its name"), "color": strProp("Its colour in the calendar")},
		required: []string{"space_id", "name"}},
	{name: "set_ward", title: "Change a ward",
		desc:     "Rename a ward or give it another colour in the calendar.",
		props:    map[string]any{"space_id": spaceIDProp, "ward": strProp("The ward's id or name"), "name": strProp("A new name"), "color": strProp("A new colour: blue, emerald, violet, amber, rose, cyan, indigo, or #rrggbb")},
		required: []string{"space_id", "ward"}},
	{name: "add_place", title: "Add a place",
		desc:     "A town in Finland where the whole space, or one ward, usually is (home, a grandparent's, the summer cottage). The summary shows the weather of each place; with none, Tampere's. The town is looked up by its name unless lat and lon are given.",
		props:    map[string]any{"space_id": spaceIDProp, "place": strProp("The town's name, e.g. Kuopio"), "ward": strProp("The ward's id or name; leave out for the whole space"), "lat": map[string]any{"type": "number", "description": "Latitude in degrees, with lon; found from the name when left out"}, "lon": map[string]any{"type": "number", "description": "Longitude in degrees, with lat"}},
		required: []string{"space_id", "place"}},
	{name: "remove_place", title: "Remove a place",
		desc:     "Take a town off the space's or a ward's places.",
		props:    map[string]any{"space_id": spaceIDProp, "place": strProp("The town's name"), "ward": strProp("The ward's id or name; leave out for the space's own places")},
		required: []string{"space_id", "place"}},
	{name: "remove_ward", title: "Remove a ward", destructive: true,
		desc:     "Owner only: remove a ward added by mistake. Its events stay, without a ward.",
		props:    map[string]any{"space_id": spaceIDProp, "ward": strProp("The ward's id or name")},
		required: []string{"space_id", "ward"}},
	{name: "invite_member", title: "Invite a member",
		desc:     "Owner only: invite someone by e-mail address. They join by opening the Warden page signed in with that address.",
		props:    map[string]any{"space_id": spaceIDProp, "email": strProp("Their address")},
		required: []string{"space_id", "email"}},
	{name: "remove_member", title: "Remove a member", destructive: true,
		desc:     "Owner only (or yourself, to leave): remove a member by uid or address, or take back an invite.",
		props:    map[string]any{"space_id": spaceIDProp, "member": strProp("A member's uid or address, or an invited address")},
		required: []string{"space_id", "member"}},
	{name: "set_space", title: "Change a space",
		desc:     "Owner only: rename a space or change its words (vocab) or language.",
		props:    map[string]any{"space_id": spaceIDProp, "name": strProp("A new name"), "vocab": strProp("demo, family, business or sports"), "lang": strProp("fi or en")},
		required: []string{"space_id"}},
	{name: "delete_space", title: "Delete a space", destructive: true,
		desc:     "Owner only: delete a space and all its events. confirm_name must be the space's name.",
		props:    map[string]any{"space_id": spaceIDProp, "confirm_name": strProp("The space's name, exactly")},
		required: []string{"space_id", "confirm_name"}},
	{name: "set_watch", title: "Watch for offers",
		desc: "Have Warden look for offers of an entry (a thing still to get, a need, a planned purchase, any entry) on second-hand sites and the web, once a day by default. New finds are kept on the entry (list_watches, the entry's watch) and the space gets a reminder of them. Only the fields given change; active: false stops the watch.",
		props: map[string]any{"space_id": spaceIDProp, "event_id": strProp("The entry to watch for"),
			"query":      strProp("Search words, e.g. \"lasten sänky 90x200\"; the entry's title when left out"),
			"max_price":  map[string]any{"type": "number", "description": "Leave out offers that cost more, euros (0 or null: no limit). An offer whose price is not seen is kept."},
			"sources":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Where to look: tori (Tori.fi), vinted (Vinted.fi), huuto (Huuto.net), web (the whole web). Default tori, vinted, huuto."},
			"exclude":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Words that leave an offer out, e.g. \"ostetaan\", \"rikki\""},
			"every_days": map[string]any{"type": "integer", "description": "Days between searches, 1 to 30 (default 1)"},
			"area":       strProp("A town whose listings come first, e.g. \"Espoo\"; by default the first usual place of the entry's ward, else the space's, else the entry's own place, else Tampere (\"\" goes back to that)"),
			"active":     map[string]any{"type": "boolean", "description": "false stops the watch (it is kept with its finds); true by default"}},
		required: []string{"space_id", "event_id"}},
	{name: "list_watches", title: "List offer watches", readOnly: true,
		desc:     "The space's entries that are watched for offers: each watch's search words, price limit, sources, area, when it last ran and its latest finds (title, url, price, where the item is, source, the day found).",
		props:    map[string]any{"space_id": spaceIDProp},
		required: []string{"space_id"}},
	{name: "run_watch", title: "Look for offers now",
		desc:     "Run an entry's watch now instead of waiting for the daily run: the new finds are answered, kept on the entry and put in a reminder.",
		props:    map[string]any{"space_id": spaceIDProp, "event_id": strProp("The watched entry")},
		required: []string{"space_id", "event_id"}},
}

func findWardenTool(name string) *roomTool {
	for i := range wardenTools {
		if wardenTools[i].name == name {
			return &wardenTools[i]
		}
	}
	return nil
}

const wardenInstructions = "Warden keeps a group's shared calendar: its spaces, the wards the group is responsible for (children, pets, a car, clients, players), their events and who takes care of what. Call list_spaces first. To bring the important events of the user's own calendar or of a schedule into a space, use import_events (or import_ics for an .ics file) with each event's source id as ext_id, the ward it concerns and who is responsible; times are the space's local time. Every entry has a type (birthday invitations, hobbies and their sessions, school events and exception days, doctor's visits, trips and bookings, children's travel cards and tickets, bills, purchases and needs, gift ideas, repairs, home tasks, reminders, changes to the usual routine and more): list_entry_types lists them with the fields common to all, each type's own fields, what it requires, which money fields go to the budget and which dates make reminders. save_event saves one with its type, fields and cost, and budget sums the costs. set_watch has Warden look for offers of an entry (a thing to get) on Tori, Vinted, Huuto.net or the web once a day; list_watches shows the finds. Tell the user the page: "

// ------------------------------------------------------------------- page --

// the editor's sign-in page, in Warden's words
var wardenLoginHTML = strings.NewReplacer(
	"<title>Sliqtly editor</title>", "<title>Warden</title>",
	">Sign in to the editor<", ">Sign in to Warden<",
	"The Sliqtly editor is for signed-in users. Sign in with your Google account: a Trial license lets you edit two presentations in the cloud.",
	"Warden is a group's shared calendar of who takes care of what. Sign in with your Google account.",
	`"Kirjaudu editoriin"`, `"Kirjaudu Wardeniin"`,
	"Sliqtlyn editori on kirjautuneille käyttäjille. Kirjaudu Google-tililläsi: Trial-lisenssillä voit muokata kahta esitystä pilvessä.",
	"Warden on ryhmän yhteinen kalenteri siitä, kuka huolehtii mistäkin. Kirjaudu Google-tililläsi.",
	"The editor did not open after sign-in.", "The page did not open after sign-in.",
	"Editori ei auennut kirjautumisen jälkeen.", "Sivu ei auennut kirjautumisen jälkeen.",
).Replace(editorLoginHTML)

func wardenWhoOf(t *IDToken) wardenWho {
	w := wardenWho{uid: t.UID, name: cloudSpeakerName(t)}
	if t.Verified && t.Email != "" {
		w.email = strings.ToLower(t.Email)
	}
	return w
}

// serveWarden: true when the request was Warden's
func serveWarden(env *Env, w http.ResponseWriter, r *http.Request) bool {
	p := r.URL.Path
	if p == wardenResource && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		serveWardenResource(env, w)
		return true
	}
	if !strings.HasPrefix(p, wardenPath+"/") {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch {
	case env.warden == nil:
		http.NotFound(w, r)
	case p == wardenMcpPath:
		serveWardenMcp(env, w, r)
	case p == wardenPath+"/login":
		serveWardenLogin(env, w, r)
	case p == wardenPath+"/api/run_watches":
		serveWardenWatchRun(env, w, r)
	case strings.HasPrefix(p, wardenPath+"/api/"):
		serveWardenAPI(env, w, r, strings.TrimPrefix(p, wardenPath+"/api/"))
	default:
		// the page and its files are Hosting's
		http.NotFound(w, r)
	}
	return true
}

func serveWardenLogin(env *Env, w http.ResponseWriter, r *http.Request) {
	g := env.EditorGate
	if g == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	if g.user(r) != nil {
		http.Redirect(w, r, wardenPath+"/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, wardenLoginHTML)
}

func serveWardenAPI(env *Env, w http.ResponseWriter, r *http.Request, op string) {
	g := env.EditorGate
	if g == nil || (op != "list_spaces" && op != "create_space" && op != wardenAssistOp && op != wardenChangesOp && findWardenTool(op) == nil) {
		editorJSON(w, 404, map[string]string{"error": "No such call."})
		return
	}
	if r.Method != http.MethodPost || !sameSite(env, r) {
		editorJSON(w, 403, map[string]string{"error": "Not allowed."})
		return
	}
	t := g.user(r)
	if t == nil {
		editorJSON(w, 401, map[string]string{"error": "Sign in again.", "code": "signed-out"})
		return
	}
	limit := wardenLimit
	if op == wardenChangesOp {
		limit = wardenChangesLimit
	}
	if why := limit(t.UID); why != "" {
		editorJSON(w, 429, map[string]string{"error": "Too many requests; try again in a few minutes."})
		return
	}
	a := map[string]any{}
	// the assistant's question carries its pictures
	max := int64(4 << 20)
	if op == wardenAssistOp {
		max = 16 << 20
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, max)).Decode(&a); err != nil && !errors.Is(err, io.EOF) {
		editorJSON(w, 400, map[string]string{"error": "A JSON object is expected."})
		return
	}
	who := wardenWhoOf(t)
	if c := r.Header.Get("X-Warden-Client"); wardenIDRe.MatchString(c) {
		who.client = c
	}
	out, err := env.warden.call(r.Context(), who, op, a, true)
	var re roomErr
	switch {
	case errors.As(err, &re):
		editorJSON(w, 400, map[string]string{"error": re.msg})
	case err != nil:
		log.Printf("warden %s: %v", op, err)
		editorJSON(w, 503, map[string]string{"error": "The space could not be reached; try again in a moment."})
	default:
		editorJSON(w, 200, out)
	}
}

// -------------------------------------------------------------------- MCP --

func wardenOrigin(env *Env) string { return strings.TrimRight(env.BaseURL, "/") }

func serveWardenResource(env *Env, w http.ResponseWriter) {
	o := wardenOrigin(env)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	editorJSON(w, 200, map[string]any{
		"resource": o + wardenMcpPath, "authorization_servers": []string{o},
		"scopes_supported": []string{"decks"}, "bearer_methods_supported": []string{"header"},
		"resource_name": "Warden", "resource_documentation": o + wardenPath + "/",
	})
}

// the account an OAuth access token names, for this resource; nil when it
// holds none (or is another resource's)
func wardenTokenWho(ctx context.Context, env *Env, header string) (*wardenWho, error) {
	h := strings.TrimSpace(header)
	if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
		return nil, nil
	}
	tok := strings.TrimSpace(h[7:])
	if tok == "" || strings.Contains(tok, " ") {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(tok))
	d, err := env.DB.Get(ctx, "mcp_oauth_tokens", hex.EncodeToString(sum[:]))
	if err != nil {
		return nil, err
	}
	if d == nil || docStr(d, "kind") != "access" || millis(d["exp"]) < time.Now().UnixMilli() || docStr(d, "uid") == "" {
		return nil, nil
	}
	// a token asked for another resource (sliqtly.com/mcp) is not Warden's
	if res := strings.TrimRight(docStr(d, "resource"), "/"); res != "" && res != wardenOrigin(env)+wardenMcpPath {
		return nil, nil
	}
	return &wardenWho{uid: docStr(d, "uid"), name: docStr(d, "name")}, nil
}

func wardenRPCError(id any, code int, msg string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

func serveWardenMcp(env *Env, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id")
	switch r.Method {
	case http.MethodOptions:
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Protocol-Version, Mcp-Session-Id")
		w.WriteHeader(204)
		return
	case http.MethodGet:
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			http.Redirect(w, r, wardenPath+"/", http.StatusFound)
			return
		}
		w.Header().Set("Allow", "POST")
		editorJSON(w, 405, wardenRPCError(nil, -32000, "Method not allowed: POST."))
		return
	case http.MethodDelete:
		w.WriteHeader(204) // stateless: there is no session to end
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST")
		editorJSON(w, 405, wardenRPCError(nil, -32000, "Method not allowed: POST."))
		return
	}
	if !env.OAuth {
		http.NotFound(w, r)
		return
	}
	who, err := wardenTokenWho(r.Context(), env, r.Header.Get("Authorization"))
	if err != nil {
		log.Printf("warden mcp token: %v", err)
		editorJSON(w, 500, wardenRPCError(nil, -32603, "Internal error"))
		return
	}
	if who == nil {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+wardenOrigin(env)+wardenResource+`"`)
		editorJSON(w, 401, wardenRPCError(nil, -32001, "Sign in to use Warden."))
		return
	}
	var msg any
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&msg); err != nil {
		editorJSON(w, 400, wardenRPCError(nil, -32700, "Parse error"))
		return
	}
	if batch, ok := msg.([]any); ok {
		out := []any{}
		for _, m := range batch {
			if res := wardenRPC(r.Context(), env, *who, m); res != nil {
				out = append(out, res)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(202)
			return
		}
		editorJSON(w, 200, out)
		return
	}
	res := wardenRPC(r.Context(), env, *who, msg)
	if res == nil {
		w.WriteHeader(202)
		return
	}
	editorJSON(w, 200, res)
}

var wardenVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// one JSON-RPC message → its answer, nil for a notification
func wardenRPC(ctx context.Context, env *Env, who wardenWho, msg any) any {
	m, ok := msg.(map[string]any)
	if !ok {
		return wardenRPCError(nil, -32600, "Invalid Request")
	}
	id, hasID := m["id"]
	method, _ := m["method"].(string)
	if method == "" || !hasID {
		return nil // a notification, or a client's answer: nothing to say
	}
	params, _ := m["params"].(map[string]any)
	ok200 := func(res any) any { return map[string]any{"jsonrpc": "2.0", "id": id, "result": res} }
	switch method {
	case "initialize":
		v := wardenVersions[0]
		if asked, _ := params["protocolVersion"].(string); slices.Contains(wardenVersions, asked) {
			v = asked
		}
		return ok200(map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "warden", "title": "Warden", "version": "0.1.0", "websiteUrl": wardenOrigin(env) + wardenPath + "/"},
			"instructions":    wardenInstructions + wardenOrigin(env) + wardenPath + "/",
		})
	case "ping":
		return ok200(map[string]any{})
	case "tools/list":
		l := []any{}
		for _, t := range wardenTools {
			l = append(l, toolEntry(t))
		}
		return ok200(map[string]any{"tools": l})
	case "tools/call":
		name, _ := params["name"].(string)
		t := findWardenTool(name)
		if t == nil {
			return wardenRPCError(id, -32602, "Tool "+name+" not found")
		}
		args, _ := params["arguments"].(map[string]any)
		if !t.readOnly {
			if why := wardenLimit(who.uid); why != "" {
				return ok200(wardenToolText("Too many changes in a short time; try again in a few minutes.", true))
			}
		}
		out, err := env.warden.call(ctx, who, name, args, false)
		var re roomErr
		switch {
		case errors.As(err, &re):
			return ok200(wardenToolText(re.msg, true))
		case err != nil:
			log.Printf("warden mcp %s: %v", name, err)
			return ok200(wardenToolText("The space could not be reached; try again in a moment.", true))
		}
		res := wardenToolText(roomJSON(out), false)
		if o, ok := out.(map[string]any); ok {
			res["structuredContent"] = o
		}
		return ok200(res)
	}
	return wardenRPCError(id, -32601, "Method not found: "+method)
}

func wardenToolText(text string, isError bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError}
}
