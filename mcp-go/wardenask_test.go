// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

func TestWardenQuestions(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	wardenCall(t, base, "tero", "invite_member", map[string]any{"space_id": sid, "email": "anna@example.com"})
	wardenCall(t, base, "anna", "list_spaces", nil)
	wardenCall(t, base, "tero", "add_ward", map[string]any{"space_id": sid, "name": "Leo"})

	// what each kind needs, and to whom
	code, out := wardenCall(t, base, "tero", "ask_question", map[string]any{"space_id": sid, "kind": "school", "to": "Anna"})
	eq(t, []any{code, out["error"]}, []any{400, "school needs date"})
	code, out = wardenCall(t, base, "tero", "ask_question", map[string]any{"space_id": sid, "kind": "payment", "to": "anna@example.com", "amount": -5})
	eq(t, []any{code, out["error"]}, []any{400, "amount is a sum in euros, more than 0"})
	code, out = wardenCall(t, base, "tero", "ask_question", map[string]any{"space_id": sid, "kind": "free", "to": "Tero", "question": "Hei?"})
	eq(t, []any{code, out["error"]}, []any{400, "to: ask another member, not yourself"})

	// a school run: the words are made, a yes puts it on the calendar
	code, out = wardenCall(t, base, "tero", "ask_question", map[string]any{"space_id": sid, "kind": "school", "to": "anna", "ward": "Leo", "date": "2026-10-20", "start": "08:00", "direction": "pick"})
	eq(t, code, 200, "asked")
	qid := out["event_id"].(string)
	f := out["fields"].(map[string]any)
	eq(t, []any{out["title"], out["type"], f["ask_to"], f["ask_to_uid"], f["asked_by"], f["status"], f["add_to_calendar"]},
		[]any{"Voitko hakea: Leo (koulu) ti 20.10. klo 08:00?", "question", "Anna", "u-anna", "Tero", "planned", true})

	code, out = wardenCall(t, base, "tero", "answer_question", map[string]any{"space_id": sid, "event_id": qid, "answer": "kyllä"})
	eq(t, []any{code, out["error"]}, []any{400, "only Anna answers this question"}, "the asker does not answer")
	code, out = wardenCall(t, base, "anna", "list_questions", map[string]any{"space_id": sid, "mine": "to", "open_only": true})
	qs := out["questions"].([]any)
	eq(t, []any{code, len(qs), qs[0].(map[string]any)["to_me"]}, []any{200, 1, true})
	code, out = wardenCall(t, base, "anna", "answer_question", map[string]any{"space_id": sid, "event_id": qid, "answer": "Käy", "note": "Hoidan"})
	eq(t, code, 200, "anna answers")
	q := out["question"].(map[string]any)
	made := out["made"].(map[string]any)
	eq(t, []any{out["answer"], q["fields"].(map[string]any)["status"], q["fields"].(map[string]any)["answer_note"]}, []any{"yes", "confirmed", "Hoidan"})
	eq(t, []any{made["title"], made["date"], made["start"], made["who"], made["links"]},
		[]any{"Haku: Koulu", "2026-10-20", "08:00", []any{map[string]any{"name": "Anna", "role": "hakee"}}, []any{qid}})
	_, out = wardenCall(t, base, "anna", "answer_question", map[string]any{"space_id": sid, "event_id": qid, "answer": "ei"})
	eq(t, strings.HasPrefix(out["error"].(string), "it has been answered already"), true, "once")

	// a payment: a yes goes to the books, apart from the costs
	_, out = wardenCall(t, base, "tero", "ask_question", map[string]any{"space_id": sid, "kind": "payment", "to": "Anna", "amount": 42.5})
	pid := out["event_id"].(string)
	eq(t, out["title"], "Voitko maksaa 42,50 € budjetin tasaukseksi?")
	_, out = wardenCall(t, base, "anna", "answer_question", map[string]any{"space_id": sid, "event_id": pid, "answer": "yes"})
	made = out["made"].(map[string]any)
	eq(t, []any{made["type"], made["fields"].(map[string]any)["from_person"], made["fields"].(map[string]any)["to_person"]}, []any{"budget_settlement", "Anna", "Tero"})
	_, out = wardenCall(t, base, "tero", "budget", map[string]any{"space_id": sid})
	eq(t, []any{out["total"], len(out["settlements"].([]any)), out["settled"]}, []any{0.0, 1, map[string]any{"Anna": 42.5, "Tero": -42.5}})

	// options: one of them, or no; a no makes nothing
	_, out = wardenCall(t, base, "tero", "ask_question", map[string]any{"space_id": sid, "kind": "suggest", "to": "Anna", "what": "Mummolan reissu", "options": []any{"la", "su"}})
	sid2 := out["event_id"].(string)
	code, out = wardenCall(t, base, "anna", "answer_question", map[string]any{"space_id": sid, "event_id": sid2, "answer": "yes"})
	eq(t, []any{code, out["error"]}, []any{400, "answer: one of the options (la, su) or no"})
	_, out = wardenCall(t, base, "anna", "answer_question", map[string]any{"space_id": sid, "event_id": sid2, "answer": "SU", "date": "2026-10-25"})
	eq(t, []any{out["answer"], out["made"].(map[string]any)["title"], out["made"].(map[string]any)["date"]}, []any{"su", "Mummolan reissu", "2026-10-25"})
	_, out = wardenCall(t, base, "tero", "ask_question", map[string]any{"space_id": sid, "kind": "take_kids", "to": "Anna", "date": "2026-10-22", "start": "16:00", "hours": 2})
	eq(t, out["title"], "Voitko ottaa hoitoon: lapset to 22.10. klo 16:00 2 tunniksi?")
	kid := out["event_id"].(string)
	_, out = wardenCall(t, base, "anna", "answer_question", map[string]any{"space_id": sid, "event_id": kid, "answer": "ei"})
	eq(t, []any{out["answer"], out["made"], out["question"].(map[string]any)["fields"].(map[string]any)["status"]}, []any{"no", nil, "cancelled"})
}

// the hours of a care time make its end
func TestWardenQuestionHours(t *testing.T) {
	_, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family", "lang": "en"})
	sid := out["space_id"].(string)
	wardenCall(t, base, "tero", "invite_member", map[string]any{"space_id": sid, "email": "anna@example.com"})
	wardenCall(t, base, "anna", "list_spaces", nil)
	_, out = wardenCall(t, base, "tero", "ask_question", map[string]any{"space_id": sid, "kind": "take_kids", "to": "Anna", "date": "2026-10-22", "start": "16:00", "hours": 2.5})
	eq(t, out["title"], "Can you look after the children Thu 22 Oct at 16:00 2.5 hours?")
	_, out = wardenCall(t, base, "anna", "answer_question", map[string]any{"space_id": sid, "event_id": out["event_id"], "answer": "yes"})
	m := out["made"].(map[string]any)
	eq(t, []any{m["type"], m["start"], m["end"], m["fields"].(map[string]any)["carer"]}, []any{"childcare_shift", "16:00", "18:30", "Anna"})
}
