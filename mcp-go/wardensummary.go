// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's email summary: each person picks, for each space, whether a
// summary of its plans and reminders comes by email every morning (day) or
// on Monday mornings (week). Nothing is sent yet: the page shows what the
// email would say, from the space as it is now. The choice is the person's
// own, kept under their account:
//
//	warden_prefs/{uid}  {summary: {<space_id>: "day"|"week"}, updated}

package main

import (
	"context"
)

const wardenPrefs = "warden_prefs"

// email_summary: the caller's choice for the space, changed when period is
// given
func (s *wardenService) emailSummary(ctx context.Context, who wardenWho, a map[string]any) (any, error) {
	if who.uid == "" {
		return nil, roomErr{"sign in first"}
	}
	sp, err := s.space(ctx, who, argStr(a, "space_id"))
	if err != nil {
		return nil, err
	}
	m, err := s.summaries(ctx, who.uid)
	if err != nil {
		return nil, err
	}
	if v, ok := a["period"]; ok && v != nil {
		switch p := argStr(a, "period"); p {
		case "off":
			delete(m, sp.id)
		case "day", "week":
			m[sp.id] = p
		default:
			return nil, roomErr{"period is off, day or week"}
		}
		if err := s.db.Set(ctx, wardenPrefs, who.uid, Doc{"summary": m, "updated": s.now().UnixMilli()}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"space_id": sp.id, "period": wardenSummaryOf(m, sp.id), "email": who.email, "sending": false}, nil
}

// the person's choices, space by space
func (s *wardenService) summaries(ctx context.Context, uid string) (map[string]any, error) {
	m := map[string]any{}
	if uid == "" {
		return m, nil
	}
	d, err := s.db.Get(ctx, wardenPrefs, uid)
	if err != nil || d == nil {
		return m, err
	}
	if old, ok := d["summary"].(map[string]any); ok {
		for k, v := range old {
			m[k] = v
		}
	}
	return m, nil
}

// off, day or week
func wardenSummaryOf(m map[string]any, space string) string {
	if p, _ := m[space].(string); p != "" {
		return p
	}
	return "off"
}
