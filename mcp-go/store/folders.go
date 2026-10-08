// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

// Folders in a room: one level of them, e.g. a room's test decks out of
// the way. They are kept on the room's document ("folders": [{id, name}]),
// and a document in the room names its folder in FolderField; none, or a
// folder that is gone, is the room's top. A folder holds no rights of its
// own: who may read or change a document is still the room's to say.

// FolderField is the folder of its room a document is in.
const FolderField = "folder"

// MaxFolders a room holds; a folder's name is at most MaxFolderName runes.
const (
	MaxFolders    = 200
	MaxFolderName = 100
)

// Folder is one folder of a room.
type Folder struct {
	ID   string
	Name string
}

// ErrFolderName: the name is empty, or another folder of the room has it.
var ErrFolderName = errors.New("store: a folder needs a name of its own in the room")

// FolderName is the name as kept: one space between words, at most
// MaxFolderName runes.
func FolderName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > MaxFolderName {
		s = strings.TrimSpace(string([]rune(s)[:MaxFolderName]))
	}
	return s
}

// FoldersOf is the room document's folders by name (numbers in names as
// numbers: "Sprint 2" before "Sprint 10").
func FoldersOf(room Doc) []Folder {
	var out []Folder
	list, _ := room["folders"].([]any)
	for _, v := range list {
		m, _ := v.(map[string]any)
		id, _ := m["id"].(string)
		name, _ := m["name"].(string)
		if id != "" && name != "" {
			out = append(out, Folder{id, name})
		}
	}
	SortFolders(out)
	return out
}

// SortFolders orders folders by name, any case, numbers by value.
func SortFolders(fs []Folder) {
	sort.SliceStable(fs, func(i, j int) bool {
		if c := naturalCompare(strings.ToLower(fs[i].Name), strings.ToLower(fs[j].Name)); c != 0 {
			return c < 0
		}
		return fs[i].ID < fs[j].ID
	})
}

// a and b compared with their runs of digits as numbers
func naturalCompare(a, b string) int {
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if len(na) != len(nb) {
				return len(na) - len(nb)
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			a, b = a[da:], b[db:]
			continue
		}
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if ra != rb {
			return int(ra) - int(rb)
		}
		a, b = a[sa:], b[sb:]
	}
	return len(a) - len(b)
}

func digits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// FolderDocs is folders as the room document keeps them.
func FolderDocs(fs []Folder) []any {
	out := make([]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{"id": f.ID, "name": f.Name})
	}
	return out
}

// HasFolder: the room has a folder of that id.
func HasFolder(fs []Folder, id string) bool {
	for _, f := range fs {
		if f.ID == id {
			return true
		}
	}
	return false
}

// AddFolder is fs with a folder named name; a name the room has already
// (any case) is that folder, and made is false. → the folders, its id
func AddFolder(fs []Folder, name string, idOf func() string) (out []Folder, id string, made bool, err error) {
	n := FolderName(name)
	if n == "" {
		return fs, "", false, ErrFolderName
	}
	for _, f := range fs {
		if strings.EqualFold(f.Name, n) {
			return fs, f.ID, false, nil
		}
	}
	if len(fs) >= MaxFolders {
		return fs, "", false, errors.New("store: a room holds at most 200 folders")
	}
	for id = "f-" + idOf(); HasFolder(fs, id); id = "f-" + idOf() {
	}
	out = append(append([]Folder{}, fs...), Folder{id, n})
	SortFolders(out)
	return out, id, true, nil
}

// RenameFolder is fs with the folder id named name.
func RenameFolder(fs []Folder, id, name string) ([]Folder, error) {
	n := FolderName(name)
	if !HasFolder(fs, id) {
		return fs, ErrNotFound
	}
	out := make([]Folder, 0, len(fs))
	for _, f := range fs {
		if f.ID != id && strings.EqualFold(f.Name, n) {
			return fs, ErrFolderName
		}
		if f.ID == id {
			f.Name = n
		}
		out = append(out, f)
	}
	if n == "" {
		return fs, ErrFolderName
	}
	SortFolders(out)
	return out, nil
}

// RemoveFolder is fs without the folder id.
func RemoveFolder(fs []Folder, id string) ([]Folder, error) {
	if !HasFolder(fs, id) {
		return fs, ErrNotFound
	}
	out := make([]Folder, 0, len(fs))
	for _, f := range fs {
		if f.ID != id {
			out = append(out, f)
		}
	}
	return out, nil
}

// EditFolders changes a room's folders: editors of the room (an archived
// one is read only). fn gets the folders as they are.
func (rs Rooms) EditFolders(ctx context.Context, p Principal, room string, fn func([]Folder) ([]Folder, error)) error {
	acc, err := rs.Access(ctx, p)
	if err != nil {
		return err
	}
	if acc[room] == NoRole {
		return ErrNotFound
	}
	if !acc[room].AtLeast(Editor) {
		return ErrDenied
	}
	_, _, err = rs.S.Privileged().Update(ctx, RoomsCol, room, func(cur Doc, _ Rev) (Doc, error) {
		if cur == nil || cur["tenant"] != p.TenantID {
			return nil, ErrNotFound
		}
		if cur["archived"] == true {
			return nil, ErrDenied
		}
		next, err := fn(FoldersOf(cur))
		if err != nil {
			return nil, err
		}
		cur["folders"] = FolderDocs(next)
		return cur, nil
	})
	return err
}

// NewFolderID is a fresh folder id's random part.
func NewFolderID() string { return newID()[:8] }
