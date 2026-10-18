// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's documents: a space's plans, notes and write-ups (home
// decorating, a trip's plan, the week's summary), each one a Sliqtly
// presentation of its own, shares/{id}, the same document the editor at
// /editor/d/{id} opens and the MCP server's create_presentation makes. The
// share carries the space it belongs to and its folder there:
//
//	shares/{id}  name, md, theme, owner (who made it), visibility "private",
//	             editors (the space's members' addresses), source "warden",
//	             warden_space, warden_folder
//
// The space keeps its folders by name (`doc_folders` on warden_spaces/{id});
// a document in no folder, or in one that is gone, is at the top.
//
// Who may do what: every member of the space reads, changes, moves and
// deletes its documents here, through the page or Warden's MCP server. In
// the Sliqtly editor its maker owns it and the other members are its
// invited editors (`editors`, firestore.rules invited()): they open it
// there, and change it there under a license of their own. The editors are
// set to the members' addresses whenever a document is saved or the
// documents are listed, and on a member leaving.

package main

import (
	"context"
	"crypto/rand"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	wardenDocsCol       = "shares"
	wardenMaxDocs       = 300    // documents of a space
	wardenMaxDocFolders = 50     // folders of a space
	wardenMaxDocMD      = 307200 // Markdown of one document (Deck.MAX_MD)
	wardenMaxDocTitle   = 200
	wardenMaxFolderName = 60
	wardenDocTheme      = "editorial"
)

// the themes a document may take (Deck.THEMES in mcp-go/rgr/Deck.rgr)
var wardenDocThemes = []string{"aurora", "nebula", "carbon", "ember", "midnight", "white", "corporate", "editorial", "pearl", "hive", "lattice", "apex", "tide", "mist", "forge", "foundry", "site", "clinic", "care", "vital"}

var wardenDocTools = []roomTool{
	{name: "list_docs", title: "List documents", readOnly: true,
		desc:     "The space's documents (the page's Documents view): plans, notes and write-ups on any subject (home decorating, a trip, the week's summary), each a Sliqtly document, and the space's folders. Each with doc_id, title, folder (\"\" at the top), who made it, when it last changed (day), and url: where it opens in Sliqtly. with_text gives each one's Markdown too.",
		props:    map[string]any{"space_id": spaceIDProp, "folder": strProp("Only the documents in this folder (\"\" for the top)"), "with_text": map[string]any{"type": "boolean", "description": "Each document's Markdown too"}},
		required: []string{"space_id"}},
	{name: "get_doc", title: "Read a document", readOnly: true,
		desc:     "One document of the space: its title, folder, theme, url and its Markdown. Read it before changing it.",
		props:    map[string]any{"space_id": spaceIDProp, "doc_id": strProp("The document (list_docs)")},
		required: []string{"space_id", "doc_id"}},
	{name: "save_doc", title: "Save a document",
		desc: "Write a new document in the space, or change one (give doc_id): a Sliqtly document in Markdown. `# Title` is its first page and each `## Heading` starts a new page; under them lists (- item, - [ ] a task), paragraphs, **bold**, tables and ![pictures](https://…). A new one needs title and markdown. To change one, read it (get_doc) and give either the whole new markdown or edits, each replacing one exact passage of it; title, folder and theme change only when given. folder puts it in one of the space's folders (made when it has none by that name; \"\" moves it to the top). → the document's card with its url.",
		props: map[string]any{"space_id": spaceIDProp, "doc_id": strProp("The document to change; leave out to write a new one"), "title": strProp("Its title"), "markdown": strProp("The whole document in Markdown"), "folder": strProp("The folder it goes in, by name; \"\" for the top"), "theme": strProp("Its look in Sliqtly: " + strings.Join(wardenDocThemes, ", ") + " (" + wardenDocTheme + " by default)"),
			"edits": map[string]any{"type": "array", "description": "Instead of markdown: changes to the document's Markdown, in order", "items": map[string]any{"type": "object", "properties": map[string]any{
				"find":    strProp("A passage of the document exactly as it is; it must be found once"),
				"replace": strProp("What comes in its place (\"\" removes it)"),
			}, "required": []string{"find", "replace"}}}},
		required: []string{"space_id"}},
	{name: "delete_doc", title: "Delete a document", destructive: true,
		desc:     "Delete one document of the space, from Sliqtly too.",
		props:    map[string]any{"space_id": spaceIDProp, "doc_id": strProp("The document (list_docs)")},
		required: []string{"space_id", "doc_id"}},
	{name: "doc_folder", title: "Make, rename or remove a folder",
		desc:     "The space's folders of documents, one level of them: add makes one (folder: its name), rename gives one a new name (new_name), remove takes one away and its documents move to the top (none is deleted).",
		props:    map[string]any{"space_id": spaceIDProp, "action": map[string]any{"type": "string", "enum": []string{"add", "rename", "remove"}, "description": "add, rename or remove"}, "folder": strProp("The folder's name"), "new_name": strProp("rename: its new name")},
		required: []string{"space_id", "action", "folder"}},
}

// a share's id as the MCP server makes them (host_short_id: ten letters
// and digits)
func wardenDocID() string {
	const abc = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 10)
	rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

// a folder's name as kept: one space between words, at most
// wardenMaxFolderName runes
func wardenFolderName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > wardenMaxFolderName {
		s = strings.TrimSpace(string([]rune(s)[:wardenMaxFolderName]))
	}
	return s
}

// the folder of folders named n, any case; "" when none
func wardenFolderOf(folders []string, n string) string {
	for _, f := range folders {
		if strings.EqualFold(f, n) {
			return f
		}
	}
	return ""
}

// the members' addresses: who the editor lets in as the documents' editors
func (sp *wardenSpace) docEditors() []any {
	people, _ := sp.doc["people"].(map[string]any)
	out := []string{}
	for _, uid := range docStrs(sp.doc, "members") {
		p, _ := people[uid].(map[string]any)
		if e := strings.ToLower(docStr(p, "email")); e != "" && !slices.Contains(out, e) && len(out) < 50 {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return anys(out)
}

func (s *wardenService) docURL(id string) string {
	return s.base + "/editor/d/" + id + "#tab=view"
}

// a document as an answer
func (s *wardenService) docJSON(sp *wardenSpace, id string, d Doc, text bool) map[string]any {
	folder := wardenFolderOf(docStrs(sp.doc, "doc_folders"), docStr(d, "warden_folder"))
	out := map[string]any{
		"doc_id": id, "title": docStr(d, "name"), "folder": folder, "theme": docStr(d, "theme"),
		"made_by": wardenOr(sp.nameOf(docStr(d, "owner")), docStr(d, "made_by")), "updated": wardenDocTime(d),
		"url": s.docURL(id),
	}
	if ms := wardenDocTime(d); ms > 0 {
		out["day"] = sp.today(time.UnixMilli(ms))
	}
	if text {
		out["markdown"] = docStr(d, "md")
	}
	return out
}

// when it last changed (ms since 1970), from what the store keeps
func wardenDocTime(d Doc) int64 {
	for _, k := range []string{"warden_updated", "updated", "created"} {
		switch v := d[k].(type) {
		case int64:
			return v
		case float64:
			return int64(v)
		case int:
			return int64(v)
		}
	}
	return 0
}

// the space's documents, the newest change first
func (s *wardenService) docs(ctx context.Context, sp *wardenSpace) ([]Doc, []string, error) {
	docs, ids, err := s.db.WhereEq(ctx, wardenDocsCol, "warden_space", sp.id)
	if err != nil {
		return nil, nil, err
	}
	order := make([]int, len(docs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return wardenDocTime(docs[order[a]]) > wardenDocTime(docs[order[b]]) })
	od, oi := make([]Doc, len(docs)), make([]string, len(docs))
	for n, i := range order {
		od[n], oi[n] = docs[i], ids[i]
	}
	return od, oi, nil
}

// one document of the space
func (s *wardenService) doc(ctx context.Context, sp *wardenSpace, id string) (Doc, error) {
	if !wardenIDRe.MatchString(id) {
		return nil, roomErr{"doc_id is missing or not a document's id; list_docs names them"}
	}
	d, err := s.db.Get(ctx, wardenDocsCol, id)
	if err != nil {
		return nil, err
	}
	if d == nil || docStr(d, "warden_space") != sp.id {
		return nil, roomErr{"no such document in this space"}
	}
	return d, nil
}

// the documents' editors made the members' addresses, where they are not
func (s *wardenService) syncDocEditors(ctx context.Context, sp *wardenSpace, docs []Doc, ids []string) error {
	want := sp.docEditors()
	for i, d := range docs {
		if slices.Equal(anys(docStrs(d, "editors")), want) {
			continue
		}
		if err := s.db.Update(ctx, wardenDocsCol, ids[i], Doc{"editors": want}); err != nil {
			return err
		}
		d["editors"] = want
	}
	return nil
}

func (s *wardenService) listDocs(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	docs, ids, err := s.docs(ctx, sp)
	if err != nil {
		return nil, err
	}
	if err := s.syncDocEditors(ctx, sp, docs, ids); err != nil {
		return nil, err
	}
	_, only := a["folder"].(string)
	want := wardenFolderOf(docStrs(sp.doc, "doc_folders"), argStr(a, "folder"))
	text := argBool(a, "with_text", false)
	out := []any{}
	count := map[string]int{}
	for i, d := range docs {
		j := s.docJSON(sp, ids[i], d, text)
		count[j["folder"].(string)]++
		if only && j["folder"] != want {
			continue
		}
		out = append(out, j)
	}
	folders := []any{}
	for _, f := range docStrs(sp.doc, "doc_folders") {
		folders = append(folders, map[string]any{"name": f, "docs": count[f]})
	}
	return map[string]any{"space_id": sp.id, "folders": folders, "docs": out}, nil
}

func (s *wardenService) getDoc(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	id := argStr(a, "doc_id")
	d, err := s.doc(ctx, sp, id)
	if err != nil {
		return nil, err
	}
	return s.docJSON(sp, id, d, true), nil
}

// the folder named n made one of the space's, when it is not; "" for the top
func (s *wardenService) ensureDocFolder(ctx context.Context, sp *wardenSpace, n string) (string, error) {
	n = wardenFolderName(n)
	if n == "" {
		return "", nil
	}
	folders := docStrs(sp.doc, "doc_folders")
	if f := wardenFolderOf(folders, n); f != "" {
		return f, nil
	}
	if len(folders) >= wardenMaxDocFolders {
		return "", roomErr{"the space has as many folders as it may"}
	}
	sp.doc["doc_folders"] = anys(append(folders, n))
	return n, s.save(ctx, sp)
}

func (s *wardenService) saveDoc(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	id := argStr(a, "doc_id")
	var cur Doc
	if id != "" {
		var err error
		if cur, err = s.doc(ctx, sp, id); err != nil {
			return nil, err
		}
	}
	patch := Doc{}
	if t, ok := a["title"].(string); ok {
		t = strings.Join(strings.Fields(t), " ")
		if t == "" || utf8.RuneCountInString(t) > wardenMaxDocTitle {
			return nil, roomErr{"title is 1 to 200 characters"}
		}
		patch["name"] = t
	}
	md, hasMD := a["markdown"].(string)
	edits, _ := a["edits"].([]any)
	if hasMD && len(edits) > 0 {
		return nil, roomErr{"give markdown or edits, not both"}
	}
	if len(edits) > 0 {
		if cur == nil {
			return nil, roomErr{"edits change a document: give doc_id, or markdown for a new one"}
		}
		md = docStr(cur, "md")
		for i, e := range edits {
			m, _ := e.(map[string]any)
			find, _ := m["find"].(string)
			repl, ok := m["replace"].(string)
			if find == "" || !ok {
				return nil, roomErr{"each edit is {find, replace}"}
			}
			switch strings.Count(md, find) {
			case 0:
				return nil, roomErr{"edit " + strconv.Itoa(i+1) + ": find is not in the document; read it again (get_doc)"}
			case 1:
			default:
				return nil, roomErr{"edit " + strconv.Itoa(i+1) + ": find is in the document more than once; give more of it"}
			}
			md = strings.Replace(md, find, repl, 1)
		}
		hasMD = true
	}
	if hasMD {
		if strings.TrimSpace(md) == "" {
			return nil, roomErr{"markdown is empty"}
		}
		if len(md) > wardenMaxDocMD {
			return nil, roomErr{"the document is longer than 300 KB"}
		}
		patch["md"] = md
	}
	if th, ok := a["theme"].(string); ok && th != "" {
		if !slices.Contains(wardenDocThemes, th) {
			return nil, roomErr{"theme is one of " + strings.Join(wardenDocThemes, ", ")}
		}
		patch["theme"] = th
	}
	if f, ok := a["folder"].(string); ok {
		folder, err := s.ensureDocFolder(ctx, sp, f)
		if err != nil {
			return nil, err
		}
		patch["warden_folder"] = folder
	}
	patch["editors"] = sp.docEditors()
	patch["warden_updated"] = s.now().UnixMilli()
	patch["updated"] = s.db.ServerTime()
	if cur == nil {
		if patch["name"] == nil || patch["md"] == nil {
			return nil, roomErr{"a new document needs title and markdown"}
		}
		docs, _, err := s.docs(ctx, sp)
		if err != nil {
			return nil, err
		}
		if len(docs) >= wardenMaxDocs {
			return nil, roomErr{"the space has as many documents as it may"}
		}
		d := Doc{"owner": sp.who.uid, "made_by": sp.who.name, "visibility": "private", "deck": "mcp", "source": "warden",
			"theme": wardenDocTheme, "css": nil, "files": []any{}, "created": s.db.ServerTime(),
			"warden_space": sp.id, "warden_folder": ""}
		for k, v := range patch {
			d[k] = v
		}
		// a share's id and a viewing link's are never the same
		for range 5 {
			id = wardenDocID()
			if l, err := s.db.Get(ctx, "links", id); err != nil {
				return nil, err
			} else if l != nil {
				continue
			}
			was, err := s.db.Create(ctx, wardenDocsCol, id, d)
			if err != nil {
				return nil, err
			}
			if was == nil {
				break
			}
			id = ""
		}
		if id == "" {
			return nil, roomErr{"the document could not be made; try again"}
		}
		return s.docJSON(sp, id, d, false), nil
	}
	if err := s.db.Update(ctx, wardenDocsCol, id, patch); err != nil {
		return nil, err
	}
	for k, v := range patch {
		cur[k] = v
	}
	return s.docJSON(sp, id, cur, false), nil
}

func (s *wardenService) deleteDoc(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	id := argStr(a, "doc_id")
	if _, err := s.doc(ctx, sp, id); err != nil {
		return nil, err
	}
	if err := s.db.Delete(ctx, wardenDocsCol, id); err != nil {
		return nil, err
	}
	// its pictures and data files (Store.remove)
	if s.removeFiles != nil {
		s.removeFiles(ctx, "shares/"+id+"/")
	}
	return map[string]any{"deleted": id}, nil
}

func (s *wardenService) docFolder(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	folders := docStrs(sp.doc, "doc_folders")
	name := wardenFolderName(argStr(a, "folder"))
	if name == "" {
		return nil, roomErr{"folder is the folder's name"}
	}
	have := wardenFolderOf(folders, name)
	// the documents in the folder named from, moved to the one named to
	move := func(from, to string) error {
		docs, ids, err := s.docs(ctx, sp)
		if err != nil {
			return err
		}
		for i, d := range docs {
			if strings.EqualFold(docStr(d, "warden_folder"), from) {
				if err := s.db.Update(ctx, wardenDocsCol, ids[i], Doc{"warden_folder": to}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	switch argStr(a, "action") {
	case "add":
		if _, err := s.ensureDocFolder(ctx, sp, name); err != nil {
			return nil, err
		}
	case "rename":
		if have == "" {
			return nil, roomErr{"no such folder: " + name}
		}
		to := wardenFolderName(argStr(a, "new_name"))
		if to == "" {
			return nil, roomErr{"new_name is the folder's new name"}
		}
		if other := wardenFolderOf(folders, to); other != "" && other != have {
			return nil, roomErr{"the space has a folder called " + other + " already"}
		}
		for i, f := range folders {
			if f == have {
				folders[i] = to
			}
		}
		sp.doc["doc_folders"] = anys(folders)
		if err := s.save(ctx, sp); err != nil {
			return nil, err
		}
		if err := move(have, to); err != nil {
			return nil, err
		}
	case "remove":
		if have == "" {
			return nil, roomErr{"no such folder: " + name}
		}
		sp.doc["doc_folders"] = anys(slices.DeleteFunc(folders, func(f string) bool { return f == have }))
		if err := s.save(ctx, sp); err != nil {
			return nil, err
		}
		if err := move(have, ""); err != nil {
			return nil, err
		}
	default:
		return nil, roomErr{"action is add, rename or remove"}
	}
	return s.listDocs(ctx, sp, map[string]any{})
}

// the space is deleted: its documents stay with who made them, in Sliqtly,
// no longer the space's nor shared with its members
func (s *wardenService) releaseDocs(ctx context.Context, sp *wardenSpace) error {
	_, ids, err := s.db.WhereEq(ctx, wardenDocsCol, "warden_space", sp.id)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.db.Update(ctx, wardenDocsCol, id, Doc{"warden_space": "", "warden_folder": "", "editors": []any{}}); err != nil {
			return err
		}
	}
	return nil
}
