// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's info screen: a short Sliqtly presentation of the space's day and
// the days ahead, another kind of summary to put on a screen (a tablet on
// the kitchen wall, a TV). The assistant writes it (the page's "Make it
// now" asks it to) and saves it with the MCP tool info_screen; the page's
// top bar opens the latest. Each space keeps one presentation, made again
// in place, so its link stays:
//
//	spaces/{id}.info_screen  {deck_id, url, owner, made, by, morning, morning_by}
//	spaces/{id}.info_morning true when it is made again every morning
//
// The presentation is the account's own Sliqtly deck (create_presentation,
// update_presentation run as the person, aiorganize.go aiPlaceCall), seen
// by anyone with its link, like the calendar feed. A member who did not
// make the last one makes a new one of their own.
//
// Every morning `warden-morning` (a Cloud Run job, started by the GitHub
// workflow warden-morning.yml) asks the assistant to make it again for each
// space that has morning on, as the member who turned it on.

package main

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"
)

// what the assistant is asked, by the page's button and in the morning
const wardenInfoAsk = `(Make the space's info screen now and save it with info_screen.) Read the plans of today and the next six days (list_events), the reminders and deadlines that are due or late, and the open questions (list_questions). Write a short presentation for a screen on the wall, in the space's language:
- "# " title slide: the space's name, the weekday and the date.
- "## Tänään" (Today): today's plans in time order, "- 08:15 Koulu · Leo", with who takes or picks up when it is said; "Ei menoja" when there are none.
- "## Muista" (Remember): what is due or late, and the open questions, one line each with its day.
- "## Tällä viikolla" (This week): the next days, a line per day with its plans, briefly.
- one more slide only for something important coming (marked important, a birthday, a trip).
Short lines, at most seven on a slide, no tables, no pictures, no speaker notes, nothing invented. Then answer in one line that it is made.`

// the assistant's purpose that asks it for the info screen
const wardenInfoPurpose = "info_screen"

// the info screen's Markdown at most, in characters
const wardenInfoMax = 20000

// runs one of the Sliqtly MCP tools as uid → its structured answer
type wardenDeckCall func(ctx context.Context, uid, op string, args map[string]any) (map[string]any, error)

// the space's info screen as an answer: its link, when and by whom it was
// made, and whether it is made again every morning
func wardenInfoJSON(sp *wardenSpace) map[string]any {
	m, _ := sp.doc["info_screen"].(map[string]any)
	out := map[string]any{"url": "", "made": "", "made_by": "", "morning": false}
	if m == nil {
		return out
	}
	out["url"] = docStr(m, "url")
	if n := backupInt(m, "made"); n > 0 {
		out["made"] = time.UnixMilli(n).In(wardenBackupLoc(docStr(sp.doc, "tz"))).Format("2006-01-02T15:04")
	}
	out["made_by"] = sp.nameOf(docStr(m, "by"))
	out["morning"] = m["morning"] == true
	return out
}

// info_screen: reads the latest, saves a new one (markdown) and turns the
// morning remake on or off (morning)
func (s *wardenService) infoScreen(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	m := map[string]any{}
	if old, ok := sp.doc["info_screen"].(map[string]any); ok {
		for k, v := range old {
			m[k] = v
		}
	}
	md := strings.TrimSpace(argStr(a, "markdown"))
	_, setMorning := a["morning"].(bool)
	if md == "" && !setMorning {
		return wardenInfoJSON(sp), nil
	}
	if md != "" {
		if s.deck == nil {
			return nil, roomErr{"Presentations are not available on this server."}
		}
		if len([]rune(md)) > wardenInfoMax {
			return nil, roomErr{fmt.Sprintf("markdown: at most %d characters", wardenInfoMax)}
		}
		if !strings.HasPrefix(md, "#") && !strings.HasPrefix(md, "---") {
			return nil, roomErr{"markdown starts with the \"# \" title slide"}
		}
		title := strings.TrimSpace(argStr(a, "title"))
		if title == "" {
			title = docStr(sp.doc, "name") + ": info"
		}
		args := map[string]any{"title": title, "markdown": md}
		var out map[string]any
		var err error
		deck := docStr(m, "deck_id")
		if deck != "" && docStr(m, "owner") == sp.who.uid {
			args["deck_id"] = deck
			out, err = s.deck(ctx, sp.who.uid, "update_presentation", args)
			if err != nil {
				// gone, or no longer the person's: a new one
				log.Printf("warden %s: info screen %s not updated: %v", sp.id, deck, err)
				delete(args, "deck_id")
				deck = ""
			}
		} else {
			deck = ""
		}
		if deck == "" {
			args["visibility"] = "link"
			args["theme"] = "pearl"
			out, err = s.deck(ctx, sp.who.uid, "create_presentation", args)
			if err != nil {
				return nil, err
			}
			deck = argStr(out, "deck_id")
			m["url"] = argStr(out, "share_url")
			m["owner"] = sp.who.uid
		} else if u := argStr(out, "share_url"); u != "" {
			m["url"] = u
		}
		if deck == "" || docStr(m, "url") == "" {
			return nil, roomErr{"the presentation was not made"}
		}
		m["deck_id"] = deck
		m["made"] = s.now().UnixMilli()
		m["by"] = sp.who.uid
	}
	if setMorning {
		on := argBool(a, "morning", false)
		m["morning"] = on
		if on {
			m["morning_by"] = sp.who.uid
		}
		sp.doc["info_morning"] = on
	}
	sp.doc["info_screen"] = m
	up := Doc{"info_screen": m}
	if setMorning {
		up["info_morning"] = sp.doc["info_morning"]
	}
	if err := s.db.Update(ctx, wardenSpaces, sp.id, up); err != nil {
		return nil, err
	}
	out := wardenInfoJSON(sp)
	out["space_id"] = sp.id
	return out, nil
}

// warden-morning: the info screen of every space with morning on, made
// again by the assistant as the member who turned it on → how many were
// made, and how many failed
func (s *wardenService) morning(ctx context.Context) (made, failed int) {
	if s.ai == nil {
		log.Print("warden-morning: no assistant")
		return 0, 0
	}
	docs, ids, err := s.db.WhereEq(ctx, wardenSpaces, "info_morning", true)
	if err != nil {
		log.Printf("warden-morning: %v", err)
		return 0, 1
	}
	for i, d := range docs {
		m, _ := d["info_screen"].(map[string]any)
		uid := docStr(m, "morning_by")
		if uid == "" || !slices.Contains(docStrs(d, "members"), uid) {
			uid = docStr(d, "owner")
		}
		people, _ := d["people"].(map[string]any)
		p, _ := people[uid].(map[string]any)
		who := wardenWho{uid: uid, name: docStr(p, "name"), email: docStr(p, "email")}
		today := s.now().In(wardenBackupLoc(docStr(d, "tz"))).Format("2006-01-02")
		out, err := s.assist(ctx, who, map[string]any{"space_id": ids[i], "today": today, "purpose": wardenInfoPurpose})
		if err == nil && !wardenInfoSaved(out) {
			err = fmt.Errorf("the assistant did not save it")
		}
		if err != nil {
			log.Printf("warden-morning %s: %v", ids[i], err)
			failed++
			continue
		}
		made++
	}
	log.Printf("warden-morning: %d made, %d failed", made, failed)
	return made, failed
}

// whether the assistant's answer had info_screen saved
func wardenInfoSaved(out any) bool {
	m, _ := out.(map[string]any)
	calls, _ := m["calls"].([]wardenAssistCall)
	for _, c := range calls {
		if c.Tool == "info_screen" && c.OK {
			return true
		}
	}
	return false
}

// warden-morning: the Cloud Run job's work
func wardenMorningCmd() int {
	ctx := context.Background()
	e := &Env{BaseURL: env("SLIQTLY_URL", "https://sliqtly.com")}
	if err := connectFirebase(ctx, e, cloudProject(), cloudBucketName()); err != nil {
		log.Printf("firebase: %v", err)
		return 1
	}
	m, _ := newAIModel(ctx)
	if e.warden = newWardenService(e.DB); e.warden == nil || m == nil {
		log.Print("warden-morning: no database or no model")
		return 1
	}
	e.warden.ai = newWardenAssist(m)
	NewApp(e)
	if _, failed := e.warden.morning(ctx); failed > 0 {
		return 1
	}
	return 0
}
