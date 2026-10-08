// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func names(fs []Folder) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}

func TestFolders(t *testing.T) {
	n := 0
	idOf := func() string { n++; return string(rune('a' + n)) }
	fs, id, made, err := AddFolder(nil, "  Sprint   10 ", idOf)
	if err != nil || !made || id != "f-b" {
		t.Fatal(fs, id, made, err)
	}
	fs, _, _, _ = AddFolder(fs, "Sprint 2", idOf)
	fs, _, _, _ = AddFolder(fs, "testing", idOf)
	if got := names(fs); !reflect.DeepEqual(got, []string{"Sprint 2", "Sprint 10", "testing"}) {
		t.Fatal(got)
	}
	if _, same, made, _ := AddFolder(fs, "TESTING", idOf); made || same != "f-d" {
		t.Fatal(same, made)
	}
	if _, _, _, err := AddFolder(fs, " ", idOf); !errors.Is(err, ErrFolderName) {
		t.Fatal(err)
	}
	if _, err := RenameFolder(fs, "f-b", "Testing"); !errors.Is(err, ErrFolderName) {
		t.Fatal(err)
	}
	if _, err := RenameFolder(fs, "f-x", "Y"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	fs, err = RenameFolder(fs, "f-d", "A tests")
	if err != nil || names(fs)[0] != "A tests" {
		t.Fatal(names(fs), err)
	}
	fs, err = RemoveFolder(fs, "f-b")
	if err != nil || len(fs) != 2 || HasFolder(fs, "f-b") {
		t.Fatal(fs, err)
	}
	// kept on the room's document and read back, junk left out
	d := Doc{"folders": append(FolderDocs(fs), map[string]any{"id": ""}, "x")}
	if got := FoldersOf(d); !reflect.DeepEqual(got, fs) {
		t.Fatal(got)
	}
	if long := FolderName(strings.Repeat("é", 150)); len([]rune(long)) != MaxFolderName {
		t.Fatal(len([]rune(long)))
	}
}
