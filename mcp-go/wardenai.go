// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's assistant on the page: the person writes or pastes anything (a
// school letter, a hobby's timetable, an invitation, a photo of one) and the
// model makes the entries of it, through Warden's own MCP tools. The tools
// are the ones an assistant connected to /apps/warden/mcp calls
// (wardenTools), run as the signed-in person in the open space: MCP is the
// interface, and the page's assistant is one more client of it.
//
//	POST /apps/warden/api/assist
//	  {space_id, text, images: [{mimeType, data}], history: [{role, text}], today}
//	  {text, changed, event_ids, deleted, calls: [{tool, ok}]}
//
// event_ids are the entries it made or changed, deleted the ones it removed:
// the page shows them under the answer.
//
// The model is the editor's (aimodel.go, SLIQTLY_AI); WARDEN_AI_MODEL names
// another of the same kind.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

const wardenAssistOp = "assist"

// the MCP tools the page's assistant may not call: it works in the open
// space, so it neither lists the person's spaces nor makes a new one
var wardenAssistLeftOut = []string{"list_spaces", "create_space"}

// the MCP tools the page's assistant calls: every other one of wardenTools,
// the same name, description and arguments (space_id is the open space's),
// so the assistant can do whatever an assistant connected to the MCP server
// can (TestWardenAssistToolsAreMCP). The owner's tools refuse a member as
// they do over MCP.
var wardenAssistTools = func() []string {
	out := []string{}
	for _, t := range wardenTools {
		if !slices.Contains(wardenAssistLeftOut, t.name) {
			out = append(out, t.name)
		}
	}
	return out
}()

const (
	wardenAssistText    = 20000 // characters of what the person gives
	wardenAssistTurns   = 6     // earlier turns kept
	wardenAssistTimeout = 3 * time.Minute
)

type wardenAssist struct {
	model aiReading
	// per account
	limit func(who string) string
}

// The page's assistant on the editor's model, or nil without one (or with
// one that cannot call tools).
func newWardenAssist(m aiModel) *wardenAssist {
	if m == nil {
		return nil
	}
	if g, ok := m.(*geminiModel); ok {
		c := *g
		c.tools = nil
		c.model = env("WARDEN_AI_MODEL", g.model)
		m = &c
	}
	if c, ok := m.(interface{ withModel(string) aiModel }); ok {
		if name := env("WARDEN_AI_MODEL", ""); name != "" {
			m = c.withModel(name)
		}
	}
	r, ok := m.(aiReading)
	if !ok {
		return nil
	}
	return &wardenAssist{model: r, limit: rateLimiter(20, 10*time.Minute)}
}

// A JSON schema property as a function declaration takes it, from a field.
func wardenFieldProp(f wardenField) map[string]any {
	desc := f.Name["en"]
	switch f.Type {
	case "number", "money", "months":
		return map[string]any{"type": "number", "description": desc}
	case "bool":
		return map[string]any{"type": "boolean", "description": desc}
	case "list", "people":
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
	case "choice":
		return map[string]any{"type": "string", "enum": f.Options, "description": desc}
	case "date":
		return strProp(desc + ", YYYY-MM-DD")
	case "time":
		return strProp(desc + ", HH:MM")
	}
	return strProp(desc)
}

// fields, as the model is told of it: the common fields typed, the type's
// own named in the description
var wardenAssistFields = func() map[string]any {
	props := map[string]any{}
	for _, f := range wardenCommon {
		props[f.Key] = wardenFieldProp(f)
	}
	return map[string]any{"type": "object", "properties": props,
		"description": "The entry's fields: these common ones and the type's own (list_entry_types and <types>), by key"}
}()

// a tool's properties without space_id (the open space's), fields typed
func wardenAssistProps(props map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range props {
		switch k {
		case "space_id":
			continue
		case "fields":
			out[k] = wardenAssistFields
		case "events":
			ev, _ := v.(map[string]any)
			items, _ := ev["items"].(map[string]any)
			p, _ := items["properties"].(map[string]any)
			it := map[string]any{}
			for ik, iv := range items {
				it[ik] = iv
			}
			it["properties"] = wardenAssistProps(p)
			e := map[string]any{}
			for ek, evv := range ev {
				e[ek] = evv
			}
			e["items"] = it
			out[k] = e
		default:
			out[k] = v
		}
	}
	return out
}

// Warden's MCP tools as the model's functions.
func wardenAssistToolList() []aiTool {
	out := []aiTool{}
	for _, name := range wardenAssistTools {
		t := findWardenTool(name)
		if t == nil {
			continue
		}
		req := slices.DeleteFunc(slices.Clone(t.required), func(k string) bool { return k == "space_id" })
		out = append(out, aiTool{Name: t.name, Description: t.desc, Params: aiObj(wardenAssistProps(t.props), req...)})
	}
	return out
}

// the types the space offers, as the model reads them: one line each
func wardenTypeLines(vocab, lang string) string {
	var b strings.Builder
	for _, t := range wardenTypesFor(vocab) {
		name := t.Name["en"]
		if lang == "fi" && t.Name["fi"] != "" {
			name += " / " + t.Name["fi"]
		}
		keys := []string{}
		for _, f := range t.Fields {
			k := f.Key + ":" + f.Type
			if f.Type == "choice" {
				k = f.Key + ":" + strings.Join(f.Options, "|")
			}
			keys = append(keys, k)
		}
		fmt.Fprintf(&b, "- %s (%s, %s) %s. %s", t.ID, t.Kind, t.Group, name, t.Help["en"])
		if len(keys) > 0 {
			b.WriteString(" Fields: " + strings.Join(keys, ", ") + ".")
		}
		if len(t.Required) > 0 {
			b.WriteString(" Requires: " + strings.Join(t.Required, ", ") + ".")
		}
		if t.Repeats {
			b.WriteString(" Usually repeats (rrule).")
		}
		for _, a := range t.AlsoIn {
			fmt.Fprintf(&b, " Also listed as %s while %s is %s.", a.Type, a.When.Field, a.When.Is)
		}
		b.WriteString("\n")
	}
	return b.String()
}

const wardenAssistRole = `You are the assistant inside Warden, a family's (or a group's) shared calendar of who takes care of what. The person gives you anything that has dates or duties in it: a school's letter, a hobby's timetable, a party invitation, a message from daycare, a photo or a screenshot of one, or just a sentence ("Leo has swimming every Tuesday at 17 from next week to the end of May"). You make the entries of it in their space with your functions, which are Warden's own MCP tools; you may also answer questions about the space.

How to work:
- Read everything given, the pictures too. Find every date, time, repeating schedule, deadline, payment, thing to bring or buy, and change to the usual routine.
- Pick the most fitting type for each entry from <types> (a birthday invitation, a hobby with its weekly times, a school event, an exception day, a doctor's visit, a bill, a need, a reminder, a schedule exception…); a plain event only when no type fits, custom_family_event for family matters no type covers. Each entry goes under the type of what it is: something to buy or get is a need (or a purchase once chosen), a job to do at home a home_task, a registration to a hobby, course, club or camp a registration (its fee and pay_by in it; each piece of gear it needs, e.g. skates, its own need with for_whom, linked to it with links), anything else to do by a day a deadline, a booking a reservation, something broken a repair; item only for a thing the family has and keeps track of (a thing marked state repair or missing is listed under repair or need as well, so it needs no second entry). Entries that belong together are linked with links, both ways: a thing, its repair or service and the contact of who repairs it; a gift idea and the reminder to buy it. Fill the type's fields and the common fields you can read; leave out what is not said. Give what each type requires.
- A schedule that repeats is ONE entry with an rrule (FREQ=WEEKLY;BYDAY=TU;UNTIL=20270531), not one entry per week; days off are exdates.
- A school timetable (lukujärjestys) is ONE school_timetable entry per child: date is the first school day, rrule FREQ=WEEKLY;UNTIL=<last school day>, and fields.times the start of the first lesson and the end of the last one on each weekday ("MO 08:15-13:00; TU 09:00-14:15"). Holidays and exception days are their own school_exception_day entries for that child; the timetable skips them.
- Deadlines (answer by, register by, pay by, bring by) are fields of the entry they belong to, which then reminds of them; a deadline with nothing else around it is its own deadline or reminder entry. Mark important things important.
- The days someone plans to do a deadline on (it is due on its date, done earlier) go in the common field planned_days, one day or several, as an array of YYYY-MM-DD; the calendar shows the entry on each. Something to get or buy between two days goes in the common fields get_from and get_by (get_what: what), which the calendar shows as a span. A recurring cost (a contract, a monthly fee) is a cost in instalments every so many months; each payment day is reminded of (cost.remind_days, 3 by default). A cost's scope is shared (the family budget) or personal (the payer's own budget); it varies by thing, so set it when it is said or plain (a shared storage is the family's, a parent's mattress or electricity contract is their own). An estimate or a frame goes in estimated_cost or estimated_cost_min and estimated_cost_max, what it really cost in actual_cost.
- The ward is who the entry is about (a child's name: a new name makes a new ward). Who takes, picks up or pays goes in the fields or in who.
- Dates are the space's own local days and times. A date without a year is the next such day from today. Never invent a date or a time that is not given or plainly meant; when one is missing, make what you can and say what is missing.
- Before adding, look at what is there already (list_events for the dates in question) and change an existing entry (save_event with its event_id) rather than adding it twice. For several new entries at once use import_events, each with an ext_id made of the source and the entry (e.g. "assist:school-letter-2026-11:retki"), so giving the same letter again changes them instead of doubling them.
- A child's clothing sizes (what it wears now in shoes, clothes, outerwear…, the next size and by when) and its clothing list (what it should have, how many it has) are the ward's: set_ward with sizes and clothes, each list given whole (the ward's present lists are in <wards>). Clothing to buy may also be a need entry when the person asks for one.
- A question to another member ("ask Anna whether she can pick Leo up on Tuesday", "kysy käykö hoitoaika", "pyydä maksamaan 40 € tasaukseksi") is ask_question with the fitting kind, to that member (<members>), the child as ward and the day and times; not a calendar entry, which the answer makes. Answering a question asked of the person is answer_question (list_questions shows them).
- Do not delete anything unless the person asks.
- If a call is refused, read why, mend it and call again.
- This is a conversation: the earlier turns are in it, and the person's next message may answer what you asked. When something you need is unclear and neither the message nor the space tells it (which child, which of several entries to change, a time or a place that is not said), make what is already clear and ask one short, concrete question about the rest instead of guessing.
- When you are asked for something your functions cannot do (sending a message or an email, booking, paying, reading a web page…), say plainly that you cannot do it and, when there is one, what the person can do instead. Never say you did something you did not do.

When you are done, answer in the person's language (Finnish when they write Finnish) in a few short lines: what you added or changed, one line each with its day, and anything you could not read or that they should check; then your question, if you have one. The page shows the entries you made or changed as cards under your answer, so do not repeat their every field. Plain sentences; **bold** and "- " lists are fine, no tables or headings. If you only answered a question, say so briefly.`

// the system prompt for one space
func wardenAssistSystem(sp *wardenSpace, today time.Time) string {
	vocab, lang := docStr(sp.doc, "vocab"), docStr(sp.doc, "lang")
	var b strings.Builder
	b.WriteString(wardenAssistRole)
	fmt.Fprintf(&b, "\n\n<today>%s, %s</today>\n", today.Format("2006-01-02"), today.Weekday())
	fmt.Fprintf(&b, "<space name=%q vocab=%q lang=%q/>\n", docStr(sp.doc, "name"), vocab, lang)
	b.WriteString("<wards>\n")
	for _, w := range docMaps(sp.doc, "wards") {
		fmt.Fprintf(&b, "- %s: %s", docStr(w, "id"), docStr(w, "name"))
		j := wardenWardJSON(w)
		for _, k := range []string{"sizes", "clothes"} {
			if l, ok := j[k]; ok {
				raw, _ := json.Marshal(l)
				fmt.Fprintf(&b, " %s=%s", k, raw)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("</wards>\n<members>\n")
	for _, uid := range docStrs(sp.doc, "members") {
		me := ""
		if uid == sp.who.uid {
			me = " (the person you talk with)"
		}
		fmt.Fprintf(&b, "- %s%s\n", wardenOr(sp.nameOf(uid), uid), me)
	}
	b.WriteString("</members>\n<common_fields>")
	keys := []string{}
	for _, f := range wardenCommon {
		keys = append(keys, f.Key+":"+f.Type)
	}
	b.WriteString(strings.Join(keys, ", "))
	b.WriteString("</common_fields>\n<types>\n")
	b.WriteString(wardenTypeLines(vocab, lang))
	b.WriteString("</types>")
	return b.String()
}

type wardenAssistAsk struct {
	SpaceID string    `json:"space_id"`
	Text    string    `json:"text"`
	Images  []aiImage `json:"images"`
	History []aiTurn  `json:"history"`
	Today   string    `json:"today"`
	// "question": written on the page's Questions view, to be asked of
	// another member
	Purpose string `json:"purpose"`
}

type wardenAssistCall struct {
	Tool string `json:"tool"`
	OK   bool   `json:"ok"`
}

// One question: the model reads it and calls the tools; what it changed and
// its answer come back.
func (s *wardenService) assist(ctx context.Context, who wardenWho, a map[string]any) (any, error) {
	if s.ai == nil {
		return nil, roomErr{"The assistant is not available on this server."}
	}
	var q wardenAssistAsk
	raw, _ := json.Marshal(a)
	if err := json.Unmarshal(raw, &q); err != nil {
		return nil, roomErr{"text, images and history are expected"}
	}
	sp, err := s.space(ctx, who, q.SpaceID)
	if err != nil {
		return nil, err
	}
	q.Text = strings.TrimSpace(q.Text)
	pics := aiQuestion{Images: q.Images}.pictures()
	if q.Text == "" && len(pics) == 0 {
		return nil, roomErr{"Write something or add a picture."}
	}
	if len([]rune(q.Text)) > wardenAssistText {
		return nil, roomErr{fmt.Sprintf("At most %d characters at a time.", wardenAssistText)}
	}
	if why := s.ai.limit(who.uid); why != "" {
		return nil, roomErr{"Too many questions in a short time; try again in a few minutes."}
	}
	// the person's own day: the page's, when it is near the server's
	today := s.now()
	if t, err := time.Parse("2006-01-02", q.Today); err == nil && t.Sub(today).Abs() < 48*time.Hour {
		today = t
	}
	turns := []aiTurn{}
	if n := len(q.History); n > wardenAssistTurns {
		q.History = q.History[n-wardenAssistTurns:]
	}
	for _, h := range q.History {
		if h.Role != "user" && h.Role != "assistant" {
			continue
		}
		turns = append(turns, aiTurn{Role: h.Role, Text: aiClip(h.Text, aiMaxTurn)})
	}
	text := q.Text
	if text == "" {
		text = "(The picture.)"
	}
	if q.Purpose == "question" {
		text = "(Written on the Questions page: make it a question to another member with ask_question.)\n" + text
	}
	turns = append(turns, aiTurn{Role: "user", Text: text, Images: pics})

	calls := []wardenAssistCall{}
	ids := []any{}
	deleted := []any{}
	changed := false
	r := &aiReader{Tools: wardenAssistToolList(), Rounds: 14, Run: func(ctx context.Context, name string, args map[string]any) aiRead {
		if !slices.Contains(wardenAssistTools, name) {
			return aiRead{Text: "No such function.", Failed: true}
		}
		if args == nil {
			args = map[string]any{}
		}
		args["space_id"] = sp.id
		out, err := s.call(ctx, who, name, args, false)
		var re roomErr
		switch {
		case asRoomErr(err, &re):
			calls = append(calls, wardenAssistCall{name, false})
			return aiRead{Text: re.msg, Failed: true}
		case err != nil:
			calls = append(calls, wardenAssistCall{name, false})
			return aiRead{Text: "The space could not be reached; try again in a moment.", Failed: true}
		}
		calls = append(calls, wardenAssistCall{name, true})
		if t := findWardenTool(name); t != nil && !t.readOnly {
			changed = true
			if name == "delete_event" {
				if id, ok := args["event_id"].(string); ok {
					deleted = append(deleted, id)
				}
			} else if m, ok := out.(map[string]any); ok {
				if id, ok := m["event_id"].(string); ok {
					ids = append(ids, id)
				}
				if l, ok := m["event_ids"].([]any); ok {
					ids = append(ids, l...)
				}
			}
		}
		return aiRead{Text: aiClip(roomJSON(out), aiReadText)}
	}}
	ctx, cancel := context.WithTimeout(ctx, wardenAssistTimeout)
	defer cancel()
	got, err := s.ai.model.AskReading(ctx, wardenAssistSystem(sp, today), turns, r)
	if err != nil && !changed {
		return nil, err
	}
	answer := strings.TrimSpace(got.Text)
	if got.Declined {
		answer = "The assistant would not answer that."
	}
	if answer == "" && changed {
		answer = fmt.Sprintf("Saved %d entries.", len(ids))
	}
	return map[string]any{"space_id": sp.id, "text": answer, "changed": changed, "event_ids": ids, "deleted": deleted, "calls": calls}, nil
}
