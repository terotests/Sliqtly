// SPDX-License-Identifier: AGPL-3.0-or-later

// A room's own files (docs/adr/0001-rooms.md, "Files"): what is put into
// the room's chat, kept at rooms/{room}/files/{name} and read at
// /files/rooms/{room}/files/{name}. They are the room's, not its decks':
// a deck sees them only when it inherits the room's files.
//
//	PUT /api/files/rooms/{room}/{name}[?unique=1]   the bytes, as the body
//	put_room_file {room_id, name, data_base64|text|url|path}   assistant
//	list_room_files {room_id}                        page and assistant
//
// Who may put one is who may write in the room's chat (an editor of a room
// that is not archived); who may read one is who may read the room.

package main

import (
	"bytes"
	"context"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// the little of the kept files a room's files need (localBucket)
type roomFileStore interface {
	Save(ctx context.Context, path, contentType string, data []byte, metadata map[string]string) error
	List(ctx context.Context, dir string) ([]store.FileRef, error)
	Read(ctx context.Context, path string, limit int64) ([]byte, error)
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
	p, err := s.rooms.For(ctx, s.localPrincipal(uid))
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
		if w <= 0 || h <= 0 {
			// not said (an assistant names the file only): the picture's own
			w, h = s.pictureSize(ctx, ref)
		}
		if strings.HasPrefix(ref.Mime, "image/") && w > 0 && h > 0 && w <= 100000 && h <= 100000 {
			f.W, f.H = w, h
		}
		out = append(out, f)
	}
	return out, nil
}

// a kept picture's width and height; 0, 0 for another file or one that
// does not read
func (s *roomService) pictureSize(ctx context.Context, ref store.FileRef) (int, int) {
	if !strings.HasPrefix(ref.Mime, "image/") || ref.Mime == "image/svg+xml" {
		return 0, 0
	}
	data, err := s.files.Read(ctx, ref.Path, maxUpload)
	if err != nil {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// put_room_file: an assistant's file into the room's files, from
// data_base64, text, url or path (an import folder), as the page puts one
// with PUT /api/files/rooms/…. A picture's answer carries its size (w, h).
func (s *roomService) putToolFile(ctx context.Context, p store.Principal, room string, a map[string]any) (any, error) {
	name := roomFileName(argStr(a, "name"))
	if name == "" {
		return nil, roomErr{"the file needs a name"}
	}
	b64 := argStr(a, "data_base64")
	text, hasText := a["text"].(string)
	u := argStr(a, "url")
	fp := argStr(a, "path")
	given := 0
	for _, ok := range []bool{b64 != "", hasText, u != "", fp != ""} {
		if ok {
			given++
		}
	}
	if given != 1 {
		return nil, roomErr{"give one of data_base64, text, url or path"}
	}
	var data []byte
	sent := ""
	switch {
	case hasText:
		data = []byte(text)
	case b64 != "":
		if strings.HasPrefix(b64, "data:") {
			if i := strings.Index(b64, ";base64,"); i > 0 {
				sent = b64[5:i]
				b64 = b64[i+8:]
			}
		}
		b, err := decodeBase64Err(b64)
		if err != nil {
			return nil, roomErr{"data_base64 is not valid base64"}
		}
		data = b
	case u != "":
		b, ct, err := s.fetchFile(ctx, u)
		if err != nil {
			return nil, err
		}
		data, sent = b, ct
	default:
		if len(s.imports) == 0 {
			return nil, roomErr{"path is read only by a Sliqtly server on your own computer started with import folders (SLIQTLY_IMPORT_DIRS); send the file as data_base64, text or url"}
		}
		b, err := s.imports.read(fp, maxUpload+1)
		if err != nil {
			return nil, roomErr{fp + " was not read (" + err.Error() + "). Import folders: " + strings.Join(s.imports.list(), ", ")}
		}
		data = b
	}
	if len(data) == 0 {
		return nil, roomErr{"the file is empty"}
	}
	if len(data) > maxUpload {
		return nil, roomErr{"a file is at most 20 MB"}
	}
	ct := roomFileType(name, sent, data)
	w, h := 0, 0
	if strings.HasPrefix(ct, "image/") && ct != "image/svg+xml" {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, roomErr{name + " is not a picture that can be read (" + err.Error() + "); long base64 is easily corrupted on the way: send it as url" + map[bool]string{true: " or path", false: ""}[len(s.imports) > 0]}
		}
		w, h = cfg.Width, cfg.Height
	}
	out, err := s.putFile(ctx, p.UserID, room, name, ct, data, !argBool(a, "replace", false))
	if err != nil {
		return nil, err
	}
	res := map[string]any{"name": out.Name, "type": out.Type, "size": out.Size, "url": out.URL,
		"next": "post_room_message with files [\"" + out.Name + "\"] shows it in the chat"}
	if w > 0 && h > 0 {
		res["w"], res["h"] = w, h
	}
	return res, nil
}

// a file's type: by its name's extension, else what the sender said, else
// by its first bytes
func roomFileType(name, sent string, data []byte) string {
	if t := mime.TypeByExtension(strings.ToLower(path.Ext(name))); t != "" {
		return strings.TrimSpace(strings.Split(t, ";")[0])
	}
	if sent = strings.TrimSpace(strings.Split(sent, ";")[0]); sent != "" && sent != "application/octet-stream" {
		return sent
	}
	return strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
}

// a public https file, at most maxUpload bytes. → its bytes and type
func (s *roomService) fetchFile(ctx context.Context, u string) ([]byte, string, error) {
	pu, err := url.Parse(u)
	if err != nil || pu.Scheme != "https" || pu.Host == "" {
		return nil, "", roomErr{"url: only public https addresses are fetched"}
	}
	if s.client == nil {
		return nil, "", roomErr{"this server fetches nothing from the web"}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, "", roomErr{"url: " + err.Error()}
	}
	req.Header.Set("user-agent", "Sliqtly-MCP/1.0")
	res, err := s.client.Do(req)
	if err != nil {
		return nil, "", roomErr{pu.Hostname() + " could not be fetched"}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, "", roomErr{pu.Hostname() + " answered " + strconv.Itoa(res.StatusCode)}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxUpload+1))
	if err != nil {
		return nil, "", roomErr{pu.Hostname() + " could not be fetched"}
	}
	return b, res.Header.Get("content-type"), nil
}
