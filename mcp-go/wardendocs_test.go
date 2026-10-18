// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"strings"
	"testing"
)

func TestWardenDocs(t *testing.T) {
	env, base, stop := editorServer(t)
	defer stop()
	_, out := wardenCall(t, base, "tero", "create_space", map[string]any{"name": "Koti", "vocab": "family"})
	sid := out["space_id"].(string)
	wardenCall(t, base, "tero", "invite_member", map[string]any{"space_id": sid, "email": "anna@example.com"})
	wardenCall(t, base, "anna", "list_spaces", nil)

	// a new one needs a title and Markdown; it goes in a folder made for it
	code, out := wardenCall(t, base, "tero", "save_doc", map[string]any{"space_id": sid, "title": "Sisustus"})
	eq(t, []any{code, out["error"]}, []any{400, "a new document needs title and markdown"})
	code, out = wardenCall(t, base, "tero", "save_doc", map[string]any{"space_id": sid, "title": "Olohuoneen sisustus",
		"markdown": "# Olohuone\n\n## Värit\n\n- Seinät: vaaleanharmaa\n- Matto: sininen\n", "folder": "Koti  ja piha"})
	eq(t, code, 200, "saved")
	did := out["doc_id"].(string)
	eq(t, []any{out["title"], out["folder"], out["theme"], out["made_by"]}, []any{"Olohuoneen sisustus", "Koti ja piha", "editorial", "Tero"})
	if !strings.HasSuffix(out["url"].(string), "/editor/d/"+did+"#tab=view") {
		t.Fatalf("url %v", out["url"])
	}

	// it is a Sliqtly presentation of tero's, its members invited to edit it
	sh, err := env.DB.Get(t.Context(), "shares", did)
	if err != nil || sh == nil {
		t.Fatalf("no share: %v", err)
	}
	eq(t, []any{sh["owner"], sh["visibility"], sh["warden_space"], sh["editors"]}, []any{"u-tero", "private", sid, []any{"anna@example.com", "teroktolonen@gmail.com"}})

	// anna, a member, reads and changes it by edits; mallory cannot
	code, _ = wardenCall(t, base, "mallory", "get_doc", map[string]any{"space_id": sid, "doc_id": did})
	eq(t, code, 400, "mallory is not a member")
	code, out = wardenCall(t, base, "anna", "save_doc", map[string]any{"space_id": sid, "doc_id": did,
		"edits": []any{map[string]any{"find": "sininen", "replace": "vihreä"}}})
	eq(t, code, 200, "anna edits")
	code, out = wardenCall(t, base, "anna", "save_doc", map[string]any{"space_id": sid, "doc_id": did,
		"edits": []any{map[string]any{"find": "punainen", "replace": "x"}}})
	eq(t, []any{code, out["error"]}, []any{400, "edit 1: find is not in the document; read it again (get_doc)"})
	_, out = wardenCall(t, base, "anna", "get_doc", map[string]any{"space_id": sid, "doc_id": did})
	eq(t, out["markdown"], "# Olohuone\n\n## Värit\n\n- Seinät: vaaleanharmaa\n- Matto: vihreä\n")

	// listed by folder
	wardenCall(t, base, "anna", "save_doc", map[string]any{"space_id": sid, "title": "Muistiinpanot", "markdown": "# Muistiinpanot\n"})
	_, out = wardenCall(t, base, "tero", "list_docs", map[string]any{"space_id": sid})
	eq(t, []any{len(out["docs"].([]any)), out["folders"]}, []any{2, []any{map[string]any{"name": "Koti ja piha", "docs": 1.0}}})
	_, out = wardenCall(t, base, "tero", "list_docs", map[string]any{"space_id": sid, "folder": "koti ja piha"})
	eq(t, len(out["docs"].([]any)), 1, "one in the folder")

	// renamed, its document goes with it; removed, the document moves to the top
	_, out = wardenCall(t, base, "tero", "doc_folder", map[string]any{"space_id": sid, "action": "rename", "folder": "Koti ja piha", "new_name": "Sisustus"})
	eq(t, out["folders"], []any{map[string]any{"name": "Sisustus", "docs": 1.0}})
	_, out = wardenCall(t, base, "tero", "doc_folder", map[string]any{"space_id": sid, "action": "remove", "folder": "sisustus"})
	eq(t, len(out["folders"].([]any)), 0, "no folders")
	_, out = wardenCall(t, base, "tero", "get_doc", map[string]any{"space_id": sid, "doc_id": did})
	eq(t, out["folder"], "", "at the top")

	// anna leaves: she is no longer its editor
	wardenCall(t, base, "anna", "remove_member", map[string]any{"space_id": sid, "member": "anna@example.com"})
	sh, _ = env.DB.Get(t.Context(), "shares", did)
	eq(t, sh["editors"], []any{"teroktolonen@gmail.com"}, "anna's address is gone")

	// deleted, it is gone from Sliqtly too
	code, out = wardenCall(t, base, "tero", "delete_doc", map[string]any{"space_id": sid, "doc_id": did})
	eq(t, []any{code, out["deleted"]}, []any{200, did})
	sh, _ = env.DB.Get(t.Context(), "shares", did)
	if sh != nil {
		t.Fatal("the share is still there")
	}
}
