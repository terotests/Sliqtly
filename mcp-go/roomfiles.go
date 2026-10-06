// SPDX-License-Identifier: AGPL-3.0-or-later

// A room's own files (docs/adr/0001-rooms.md, "Files"): what is put into
// the room's chat, kept at rooms/{room}/files/{name} and read at
// /files/rooms/{room}/files/{name}. They are the room's, not its decks':
// a deck sees them only when it inherits the room's files.
//
//	PUT /api/files/rooms/{room}/{name}[?unique=1]   the bytes, as the body
//	list_room_files {room_id}                        page and assistant
//
// Who may put one is who may write in the room's chat (an editor of a room
// that is not archived); who may read one is who may read the room.

package main

import (
	"context"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// the little of the kept files a room's files need (localBucket)
type roomFileStore interface {
	Save(ctx context.Context, path, contentType string, data []byte, metadata map[string]string) error
	List(ctx context.Context, dir string) ([]store.FileRef, error)
	RemoveAll(path string) error
}

// how many files one message shows
const chatMaxFiles = 10

var roomIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

func roomFilesDir(room string) string { return "rooms/" + room + "/files" }

// a file's name in the room: its last part, no control characters, tabs
// or slashes, at most 120 characters; "" when nothing is left
func roomFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base("/" + name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	if utf8.RuneCountInString(name) > 120 {
		ext := path.Ext(name)
		if utf8.RuneCountInString(ext) > 16 {
			ext = ""
		}
		r := []rune(strings.TrimSuffix(name, ext))
		name = string(r[:120-utf8.RuneCountInString(ext)]) + ext
	}
	return name
}

// name, or "name (2).ext", "name (3).ext"… when the room has it already
func uniqueFileName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	ext := path.Ext(name)
	if ext == name {
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		n := stem + " (" + strconv.Itoa(i) + ")" + ext
		if !taken[n] {
			return n
		}
	}
}

// a room file as answered
type roomFileOut struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	Updated int64  `json:"updated"`
	URL     string `json:"url"`
}

func (s *roomService) fileURL(room, name string) string {
	if s.filesURL == nil {
		return ""
	}
	return s.filesURL(roomFilesDir(room) + "/" + name)
}

// the room's files by name
func (s *roomService) roomFiles(ctx context.Context, room string) (map[string]store.FileRef, []roomFileOut, error) {
	refs, err := s.files.List(ctx, roomFilesDir(room))
	if err != nil {
		return nil, nil, err
	}
	byName := map[string]store.FileRef{}
	out := []roomFileOut{}
	dir := roomFilesDir(room) + "/"
	for _, r := range refs {
		name := strings.TrimPrefix(r.Path, dir)
		if name == r.Path || strings.Contains(name, "/") {
			continue
		}
		byName[name] = r
		out = append(out, roomFileOut{Name: name, Type: r.Mime, Size: r.Size, Updated: r.Updated.UnixMilli(), URL: s.fileURL(room, name)})
	}
	return byName, out, nil
}

// putFile keeps data as the room's file name (another name when unique and
// the room has one by it). → the file as kept
func (s *roomService) putFile(ctx context.Context, uid, room, name, contentType string, data []byte, unique bool) (roomFileOut, error) {
	if s.files == nil {
		return roomFileOut{}, roomErr{"this server keeps no room files"}
	}
	if !roomIDPattern.MatchString(room) {
		return roomFileOut{}, store.ErrNotFound
	}
	p, err := s.principal(ctx, uid)
	if err != nil {
		return roomFileOut{}, err
	}
	if err := s.chatRole(p, room, true); err != nil {
		return roomFileOut{}, err
	}
	name = roomFileName(name)
	if name == "" {
		return roomFileOut{}, roomErr{"the file needs a name"}
	}
	if unique {
		byName, _, err := s.roomFiles(ctx, room)
		if err != nil {
			return roomFileOut{}, err
		}
		taken := map[string]bool{}
		for n := range byName {
			taken[n] = true
		}
		name = uniqueFileName(name, taken)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := s.files.Save(ctx, roomFilesDir(room)+"/"+name, contentType, data, nil); err != nil {
		return roomFileOut{}, err
	}
	return roomFileOut{Name: name, Type: contentType, Size: int64(len(data)), URL: s.fileURL(room, name)}, nil
}

// the files a message names ("files": names, or {name, w, h} for a
// picture's size), as the room keeps them
func (s *roomService) chatFiles(ctx context.Context, room string, v any) ([]store.ChatFile, error) {
	list, _ := v.([]any)
	if len(list) == 0 {
		return nil, nil
	}
	if s.files == nil {
		return nil, roomErr{"this server keeps no room files"}
	}
	if len(list) > chatMaxFiles {
		return nil, roomErr{"a message shows at most " + strconv.Itoa(chatMaxFiles) + " files"}
	}
	byName, _, err := s.roomFiles(ctx, room)
	if err != nil {
		return nil, err
	}
	var out []store.ChatFile
	for _, it := range list {
		var name string
		var w, h int
		switch x := it.(type) {
		case string:
			name = x
		case map[string]any:
			name, _ = x["name"].(string)
			w, h = int(argInt(x, "w")), int(argInt(x, "h"))
		}
		ref, ok := byName[name]
		if !ok {
			return nil, roomErr{"the room has no file called " + strconv.Quote(name) + " (list_room_files; put it in first)"}
		}
		f := store.ChatFile{Name: name, Type: ref.Mime, Size: ref.Size}
		if strings.HasPrefix(ref.Mime, "image/") && w > 0 && h > 0 && w <= 100000 && h <= 100000 {
			f.W, f.H = w, h
		}
		out = append(out, f)
	}
	return out, nil
}
