// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's backups: copies of a space (its document: wards, places,
// settings, questions, and every event) and of one person's own budget, so a
// calendar emptied by mistake, by a member or by an assistant, can be put
// back as it was.
//
// A copy is taken before an operation changes the space when the newest is
// older than wardenBackupEvery, and always before the space is deleted or a
// copy restored; one the same as the newest is not kept again. Copies live in
// collections of their own that no Firestore rule matches and no Warden tool
// or the page's assistant reaches: only the server writes them, and it
// removes old ones only by age (wardenBackupKeep).
//
//	warden_backups/{space id | own_<uid>}   {kind, space, owner, name,
//	                     list: [{id, at, reason, events, wards, items,
//	                     hash, parts, size}]}
//	warden_backup_data/{backup id}_{n}      {data}: the copy as gzipped
//	                                        JSON, base64, in parts
//
// The page alone lists and restores them (POST api/list_backups,
// api/restore_backup): a space's by its owner, also after the space was
// deleted, and the own budget's by its person. They are not MCP tools, so
// neither an assistant connected to Warden nor the page's own assistant can
// restore one.

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	wardenBackups    = "warden_backups"
	wardenBackupData = "warden_backup_data"

	wardenListBackupsOp   = "list_backups"
	wardenRestoreBackupOp = "restore_backup"

	// a change takes a copy first when the newest is older than this
	wardenBackupEvery = 2 * time.Hour
	// base64 characters in one part document (Firestore's limit is 1 MiB)
	wardenBackupPart = 700 << 10
)

// which copies are kept, by age: all of the last week, the first of each
// day for 60 days, the first of each month for a year
func wardenBackupKeep(list []map[string]any, now time.Time, loc *time.Location) (keep, drop []map[string]any) {
	sort.Slice(list, func(i, j int) bool { return backupInt(list[i], "at") < backupInt(list[j], "at") })
	seen := map[string]bool{}
	for _, b := range list {
		at := time.UnixMilli(backupInt(b, "at")).In(loc)
		age := now.Sub(at)
		var bucket string
		switch {
		case age <= 7*24*time.Hour:
			keep = append(keep, b)
			continue
		case age <= 60*24*time.Hour:
			bucket = at.Format("d2006-01-02")
		case age <= 366*24*time.Hour:
			bucket = at.Format("m2006-01")
		}
		if bucket == "" || seen[bucket] {
			drop = append(drop, b)
			continue
		}
		seen[bucket] = true
		keep = append(keep, b)
	}
	return keep, drop
}

func backupInt(d map[string]any, k string) int64 {
	switch n := d[k].(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func wardenBackupList(idx Doc) []map[string]any {
	out := []map[string]any{}
	l, _ := idx["list"].([]any)
	for _, e := range l {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func wardenBackupNewest(idx Doc) map[string]any {
	var newest map[string]any
	for _, b := range wardenBackupList(idx) {
		if newest == nil || backupInt(b, "at") > backupInt(newest, "at") {
			newest = b
		}
	}
	return newest
}

// what a copy holds
type wardenBackupBody struct {
	Version int              `json:"version"`
	Kind    string           `json:"kind"` // space or own
	ID      string           `json:"id"`   // the space's id, or the person's uid
	Doc     map[string]any   `json:"doc"`
	Events  []wardenBackupEv `json:"events,omitempty"`
}

type wardenBackupEv struct {
	ID  string         `json:"id"`
	Doc map[string]any `json:"doc"`
}

// the space's state, as a copy holds it: the marks of the last change are
// left out of the hash, so a copy of what did not change is not kept again
func (b *wardenBackupBody) hash() string {
	d := map[string]any{}
	for k, v := range b.Doc {
		if k != "changed" && k != "changed_by" && k != "updated" {
			d[k] = v
		}
	}
	j, _ := json.Marshal(map[string]any{"doc": d, "events": b.Events})
	h := sha256.Sum256(j)
	return hex.EncodeToString(h[:16])
}

// a space's copy before op changes it; force: whatever the newest's age
func (s *wardenService) backupSpace(ctx context.Context, id string, doc Doc, reason string, force bool) error {
	idx, err := s.db.Get(ctx, wardenBackups, id)
	if err != nil {
		return err
	}
	if !force && s.backupFresh(idx) {
		return nil
	}
	docs, ids, err := s.db.WhereEq(ctx, wardenEvents, "space", id)
	if err != nil {
		return err
	}
	body := &wardenBackupBody{Version: 1, Kind: "space", ID: id, Doc: doc}
	for i := range docs {
		body.Events = append(body.Events, wardenBackupEv{ID: ids[i], Doc: docs[i]})
	}
	sort.Slice(body.Events, func(i, j int) bool { return body.Events[i].ID < body.Events[j].ID })
	wards, _ := doc["wards"].([]any)
	meta := map[string]any{"events": len(body.Events), "wards": len(wards)}
	head := Doc{"kind": "space", "space": id, "owner": docStr(doc, "owner"), "name": docStr(doc, "name"), "tz": docStr(doc, "tz")}
	return s.saveBackup(ctx, id, idx, head, body, reason, meta)
}

// the own budget's copy, before a change to it
func (s *wardenService) backupOwn(ctx context.Context, uid, reason string, force bool) error {
	key := "own_" + uid
	idx, err := s.db.Get(ctx, wardenBackups, key)
	if err != nil {
		return err
	}
	if !force && s.backupFresh(idx) {
		return nil
	}
	d, err := s.db.Get(ctx, wardenOwn, uid)
	if err != nil || d == nil {
		return err
	}
	items, _ := d["items"].([]any)
	body := &wardenBackupBody{Version: 1, Kind: "own", ID: uid, Doc: d}
	head := Doc{"kind": "own", "owner": uid}
	return s.saveBackup(ctx, key, idx, head, body, reason, map[string]any{"items": len(items)})
}

func (s *wardenService) backupFresh(idx Doc) bool {
	n := wardenBackupNewest(idx)
	return n != nil && s.now().Sub(time.UnixMilli(backupInt(n, "at"))) < wardenBackupEvery
}

func (s *wardenService) saveBackup(ctx context.Context, key string, idx Doc, head Doc, body *wardenBackupBody, reason string, meta map[string]any) error {
	hash := body.hash()
	if n := wardenBackupNewest(idx); n != nil && docStr(n, "hash") == hash {
		return nil
	}
	j, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var z bytes.Buffer
	gz := gzip.NewWriter(&z)
	gz.Write(j)
	gz.Close()
	data := base64.StdEncoding.EncodeToString(z.Bytes())
	now := s.now()
	at := now.UnixMilli()
	if n := wardenBackupNewest(idx); n != nil && backupInt(n, "at") >= at {
		at = backupInt(n, "at") + 1
	}
	id := key + "." + strconv.FormatInt(at, 10)
	parts := 0
	for off := 0; off < len(data) || parts == 0; off += wardenBackupPart {
		end := min(off+wardenBackupPart, len(data))
		if err := s.db.Set(ctx, wardenBackupData, fmt.Sprintf("%s_%d", id, parts), Doc{"data": data[off:end], "at": at}); err != nil {
			return err
		}
		parts++
	}
	entry := map[string]any{"id": id, "at": at, "reason": reason, "hash": hash, "parts": parts, "size": len(j)}
	for k, v := range meta {
		entry[k] = v
	}
	keep, drop := wardenBackupKeep(append(wardenBackupList(idx), entry), now, wardenBackupLoc(docStr(body.Doc, "tz")))
	out := Doc{}
	for k, v := range head {
		out[k] = v
	}
	// a deleted space's name and owner stay with its copies
	if idx != nil {
		for _, k := range []string{"name", "owner", "tz"} {
			if docStr(out, k) == "" {
				out[k] = docStr(idx, k)
			}
		}
	}
	out["list"] = anys(keep)
	out["updated"] = at
	if err := s.db.Set(ctx, wardenBackups, key, out); err != nil {
		return err
	}
	for _, b := range drop {
		for i := int64(0); i < max(backupInt(b, "parts"), 1); i++ {
			if err := s.db.Delete(ctx, wardenBackupData, fmt.Sprintf("%s_%d", docStr(b, "id"), i)); err != nil {
				log.Printf("warden backup %s: removing an old one: %v", key, err)
			}
		}
	}
	return nil
}

func wardenBackupLoc(tz string) *time.Location {
	if tz == "" {
		tz = wardenTZ
	}
	if l, err := time.LoadLocation(tz); err == nil {
		return l
	}
	return time.UTC
}

func (s *wardenService) readBackup(ctx context.Context, entry map[string]any) (*wardenBackupBody, error) {
	var data strings.Builder
	for i := int64(0); i < max(backupInt(entry, "parts"), 1); i++ {
		d, err := s.db.Get(ctx, wardenBackupData, fmt.Sprintf("%s_%d", docStr(entry, "id"), i))
		if err != nil {
			return nil, err
		}
		if d == nil {
			return nil, roomErr{"the backup is incomplete and cannot be restored"}
		}
		data.WriteString(docStr(d, "data"))
	}
	z, err := base64.StdEncoding.DecodeString(data.String())
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, err
	}
	j, err := io.ReadAll(gz)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.UseNumber()
	body := &wardenBackupBody{}
	if err := dec.Decode(body); err != nil {
		return nil, err
	}
	body.Doc = wardenNumbers(body.Doc).(map[string]any)
	for i := range body.Events {
		body.Events[i].Doc = wardenNumbers(body.Events[i].Doc).(map[string]any)
	}
	return body, nil
}

// JSON's numbers back as Firestore's: whole ones int64, others float64
func wardenNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		if x == nil {
			return map[string]any{}
		}
		for k, e := range x {
			x[k] = wardenNumbers(e)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = wardenNumbers(e)
		}
		return x
	}
	return v
}

// ---------------------------------------------------------- the page's --

// a copy as the page lists it: when in the space's own time, "YYYY-MM-DD HH:MM"
func wardenBackupJSON(b map[string]any, loc *time.Location) map[string]any {
	at := time.UnixMilli(backupInt(b, "at"))
	out := map[string]any{"backup_id": docStr(b, "id"), "at": at.UTC().Format(time.RFC3339), "when": at.In(loc).Format("2006-01-02 15:04"), "reason": docStr(b, "reason")}
	for _, k := range []string{"events", "wards", "items"} {
		if _, ok := b[k]; ok {
			out[k] = backupInt(b, k)
		}
	}
	return out
}

func wardenBackupsJSON(idx Doc) []any {
	l := wardenBackupList(idx)
	loc := wardenBackupLoc(docStr(idx, "tz"))
	sort.Slice(l, func(i, j int) bool { return backupInt(l[i], "at") > backupInt(l[j], "at") })
	out := []any{}
	for _, b := range l {
		out = append(out, wardenBackupJSON(b, loc))
	}
	return out
}

// POST api/list_backups {space_id}: the space's copies, for its owner;
// without space_id: the caller's own budget's and those of the spaces they
// owned that are deleted
func (s *wardenService) listBackups(ctx context.Context, who wardenWho, a map[string]any) (any, error) {
	if who.uid == "" {
		return nil, roomErr{"sign in first"}
	}
	if sid := argStr(a, "space_id"); sid != "" {
		sp, err := s.space(ctx, who, sid)
		if err != nil {
			return nil, err
		}
		if err := sp.owner(); err != nil {
			return nil, err
		}
		idx, err := s.db.Get(ctx, wardenBackups, sp.id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"space_id": sp.id, "backups": wardenBackupsJSON(idx)}, nil
	}
	own, err := s.db.Get(ctx, wardenBackups, "own_"+who.uid)
	if err != nil {
		return nil, err
	}
	docs, ids, err := s.db.WhereEq(ctx, wardenBackups, "owner", who.uid)
	if err != nil {
		return nil, err
	}
	deleted := []any{}
	for i, d := range docs {
		if docStr(d, "kind") != "space" {
			continue
		}
		sd, err := s.db.Get(ctx, wardenSpaces, ids[i])
		if err != nil {
			return nil, err
		}
		if sd == nil && len(wardenBackupList(d)) > 0 {
			deleted = append(deleted, map[string]any{"space_id": ids[i], "name": docStr(d, "name"), "backups": wardenBackupsJSON(d)})
		}
	}
	return map[string]any{"own": wardenBackupsJSON(own), "deleted_spaces": deleted}, nil
}

// POST api/restore_backup {backup_id}: puts the space (or the own budget)
// back as the copy has it; what there is now is copied first, so a restore
// can be undone by restoring that
func (s *wardenService) restoreBackup(ctx context.Context, who wardenWho, a map[string]any) (any, error) {
	if who.uid == "" {
		return nil, roomErr{"sign in first"}
	}
	bid := argStr(a, "backup_id")
	dot := strings.LastIndex(bid, ".")
	if dot <= 0 {
		return nil, roomErr{"backup_id is not a backup's id; list_backups names them"}
	}
	key := bid[:dot]
	idx, err := s.db.Get(ctx, wardenBackups, key)
	if err != nil {
		return nil, err
	}
	var entry map[string]any
	for _, b := range wardenBackupList(idx) {
		if docStr(b, "id") == bid {
			entry = b
		}
	}
	if entry == nil || docStr(idx, "owner") != who.uid {
		return nil, roomErr{"no such backup, or it is not yours to restore"}
	}
	body, err := s.readBackup(ctx, entry)
	if err != nil {
		return nil, err
	}
	if body.Kind == "own" {
		if key != "own_"+who.uid || body.ID != who.uid {
			return nil, roomErr{"no such backup, or it is not yours to restore"}
		}
		if err := s.backupOwn(ctx, who.uid, "before_restore", true); err != nil {
			return nil, err
		}
		body.Doc["updated"] = s.now().UnixMilli()
		if err := s.db.Set(ctx, wardenOwn, who.uid, body.Doc); err != nil {
			return nil, err
		}
		items, _ := body.Doc["items"].([]any)
		return map[string]any{"restored": bid, "items": len(items)}, nil
	}
	if body.Kind != "space" || body.ID != key {
		return nil, roomErr{"the backup cannot be restored"}
	}
	cur, err := s.db.Get(ctx, wardenSpaces, key)
	if err != nil {
		return nil, err
	}
	doc := body.Doc
	if cur != nil {
		// the space is still there: its owner alone restores, and who is in
		// it stays as it is now
		if docStr(cur, "owner") != who.uid {
			return nil, roomErr{"only the space's owner can do that"}
		}
		if err := s.backupSpace(ctx, key, cur, "before_restore", true); err != nil {
			return nil, err
		}
		for _, k := range []string{"owner", "members", "people", "invites"} {
			if v, ok := cur[k]; ok {
				doc[k] = v
			} else {
				delete(doc, k)
			}
		}
	} else {
		// a deleted space comes back with only its owner in it: the others
		// are invited again
		doc["owner"] = who.uid
		doc["members"] = []any{who.uid}
		if people, _ := doc["people"].(map[string]any); people != nil {
			invites := docStrs(doc, "invites")
			for uid, p := range people {
				if uid == who.uid {
					continue
				}
				if e := docStr(asMap(p), "email"); e != "" && !slices.Contains(invites, e) {
					invites = append(invites, e)
				}
				delete(people, uid)
			}
			doc["invites"] = anys(invites)
		}
	}
	_, have, err := s.db.WhereEq(ctx, wardenEvents, "space", key)
	if err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, e := range body.Events {
		keep[e.ID] = true
	}
	for _, id := range have {
		if !keep[id] {
			if err := s.db.Delete(ctx, wardenEvents, id); err != nil {
				return nil, err
			}
		}
	}
	for _, e := range body.Events {
		if err := s.db.Set(ctx, wardenEvents, e.ID, e.Doc); err != nil {
			return nil, err
		}
	}
	doc["changed"] = s.now().UnixMilli()
	doc["changed_by"] = ""
	if err := s.db.Set(ctx, wardenSpaces, key, doc); err != nil {
		return nil, err
	}
	return map[string]any{"restored": bid, "space_id": key, "events": len(body.Events)}, nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// before op changes the space: a copy when it is due (delete_space: always);
// a copy that cannot be taken stops only the space's deletion
func (s *wardenService) backupBefore(ctx context.Context, sp *wardenSpace, op string) error {
	err := s.backupSpace(ctx, sp.id, sp.doc, op, op == "delete_space")
	if err != nil {
		log.Printf("warden %s: backup before %s: %v", sp.id, op, err)
		if op == "delete_space" {
			return roomErr{"the space could not be backed up before deleting it; nothing was deleted, try again in a moment"}
		}
	}
	return nil
}
