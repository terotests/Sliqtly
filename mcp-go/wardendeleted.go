// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's deleted entries: delete_event moves an entry here instead of
// removing it, so whatever a member or an assistant deletes can be found
// (list_deleted) and put back (restore_event), through the MCP server and the
// page's assistant alike. Nothing removes them.
//
//	warden_deleted/{event id}  {space, at, by, by_name, doc}: doc is the
//	                           entry as it was when deleted
//
// Entries deleted before there was this are found in the space's backups
// (wardenbackup.go): one a copy has that the space no longer has is listed
// and restored the same way. The backups are only read here, never changed.

package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"
)

const (
	wardenDeleted = "warden_deleted"

	// at most this many of the newest backups are read for entries deleted
	// before warden_deleted
	wardenDeletedBackups = 40
)

// delete_event: the entry is moved to warden_deleted, and the entries linked
// with it no longer link it
func (s *wardenService) deleteEvent(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	id := argStr(a, "event_id")
	d, err := s.event(ctx, sp, id)
	if err != nil {
		return nil, err
	}
	bin := Doc{"space": sp.id, "at": s.now().UnixMilli(), "by": sp.who.uid, "by_name": sp.who.name, "doc": map[string]any(d)}
	if err := s.db.Set(ctx, wardenDeleted, id, bin); err != nil {
		return nil, err
	}
	if err := s.db.Delete(ctx, wardenEvents, id); err != nil {
		return nil, err
	}
	if err := s.linkOthers(ctx, sp, id, nil, docStrs(d, "links")); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": id, "restorable": true}, nil
}

// a deleted entry: from warden_deleted, or found in a backup
type wardenGone struct {
	id     string
	doc    Doc
	at     int64 // when it was deleted; for one from a backup, when the copy was taken
	byName string
	backup bool
}

// the space's deleted entries, newest first
func (s *wardenService) deletedEvents(ctx context.Context, sp *wardenSpace) ([]wardenGone, error) {
	docs, ids, err := s.db.WhereEq(ctx, wardenDeleted, "space", sp.id)
	if err != nil {
		return nil, err
	}
	_, have, err := s.db.WhereEq(ctx, wardenEvents, "space", sp.id)
	if err != nil {
		return nil, err
	}
	out := []wardenGone{}
	seen := map[string]bool{}
	for _, id := range have {
		seen[id] = true
	}
	for i, d := range docs {
		if seen[ids[i]] {
			continue // restored, or made again with the same id
		}
		seen[ids[i]] = true
		out = append(out, wardenGone{id: ids[i], doc: asMap(d["doc"]), at: backupInt(d, "at"), byName: docStr(d, "by_name")})
	}
	idx, err := s.db.Get(ctx, wardenBackups, sp.id)
	if err != nil {
		return nil, err
	}
	l := wardenBackupList(idx)
	sort.Slice(l, func(i, j int) bool { return backupInt(l[i], "at") > backupInt(l[j], "at") })
	if len(l) > wardenDeletedBackups {
		l = l[:wardenDeletedBackups]
	}
	for _, b := range l {
		body, err := s.readBackup(ctx, b)
		if err != nil {
			continue // an incomplete copy: the others may still have it
		}
		for _, e := range body.Events {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			out = append(out, wardenGone{id: e.ID, doc: e.Doc, at: backupInt(b, "at"), backup: true})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at > out[j].at })
	return out, nil
}

// list_deleted: what has been deleted from the space, to restore
func (s *wardenService) listDeleted(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	gone, err := s.deletedEvents(ctx, sp)
	if err != nil {
		return nil, err
	}
	loc := wardenBackupLoc(docStr(sp.doc, "tz"))
	now := s.now()
	l := []any{}
	for _, g := range gone {
		m := wardenEventJSON(g.id, g.doc, now)
		if g.backup {
			m["deleted"] = "before " + time.UnixMilli(g.at).In(loc).Format("2006-01-02 15:04")
		} else {
			m["deleted"] = time.UnixMilli(g.at).In(loc).Format("2006-01-02 15:04")
		}
		if g.byName != "" {
			m["deleted_by"] = g.byName
		}
		l = append(l, m)
	}
	return map[string]any{"space_id": sp.id, "deleted": l}, nil
}

// restore_event: a deleted entry back as it was when deleted, linked again
// with the entries it was linked with that are still there
func (s *wardenService) restoreEvent(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	id := argStr(a, "event_id")
	if !wardenIDRe.MatchString(id) {
		return nil, roomErr{"event_id is not an event's id"}
	}
	gone, err := s.deletedEvents(ctx, sp)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(gone, func(g wardenGone) bool { return g.id == id })
	if i < 0 {
		return nil, roomErr{"no such deleted entry in this space; list_deleted lists them"}
	}
	_, have, err := s.db.WhereEq(ctx, wardenEvents, "space", sp.id)
	if err != nil {
		return nil, err
	}
	if len(have) >= wardenMaxEvents {
		return nil, roomErr{fmt.Sprintf("a space has at most %d events", wardenMaxEvents)}
	}
	d := gone[i].doc
	s.stamp(d, sp)
	if err := s.db.Set(ctx, wardenEvents, id, d); err != nil {
		return nil, err
	}
	if err := s.linkOthers(ctx, sp, id, docStrs(d, "links"), nil); err != nil {
		return nil, err
	}
	out := wardenEventJSON(id, d, s.now())
	out["restored"] = true
	return out, nil
}
