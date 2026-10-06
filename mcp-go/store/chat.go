// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// A room's chat: its messages in the order they were posted, each with a
// number (Seq) one higher than the one before it in the same room, so a
// page that missed some sees the gap and asks for what came after the last
// it has. A reply is in its root's thread (Thread: the root's id); the
// root counts its replies and keeps the last few who wrote one.
//
// ChatLog is the messages only: who may read or post in a room is the
// caller's (roomchat.go asks Rooms), as for any other document of a room.
// SQLiteChat keeps them in sliqtly.db's chat_msgs (one row each, indexed
// by room and thread, so a page of a long chat is read without the rest);
// MemChat is the same in memory. Both pass storetest.RunChat.

// ChatFrom is who wrote a message: a person (a browser's self-chosen name,
// avatar and colour on the folder server) or an assistant (kind "bot").
type ChatFrom struct {
	ID     string `json:"id"`
	User   string `json:"user,omitempty"` // the principal it was posted for
	Name   string `json:"name"`
	Kind   string `json:"kind"` // "person" | "bot"
	Avatar string `json:"avatar,omitempty"`
	Color  string `json:"color,omitempty"`
}

// ChatMsg is one message.
type ChatMsg struct {
	Tenant string   `json:"-"`
	Room   string   `json:"room"`
	ID     string   `json:"id"`
	Seq    int64    `json:"seq"`
	Thread string   `json:"thread,omitempty"`
	At     int64    `json:"at"` // ms since 1970
	From   ChatFrom `json:"from"`
	Text   string   `json:"text"`
	Edited int64    `json:"edited,omitempty"`
	// a removed message keeps its place (and its thread) with no text
	Deleted bool `json:"deleted,omitempty"`
	// emoji → who reacted with it, in order
	Reactions map[string][]string `json:"reactions,omitempty"`
	// a root's thread: how many replies, the last one's time and the last
	// few who wrote one (newest last)
	Replies   int      `json:"replies,omitempty"`
	LastReply int64    `json:"last_reply,omitempty"`
	Repliers  []string `json:"repliers,omitempty"`
}

// ChatPage asks for a room's messages: the top level (Thread "") or one
// thread's replies, oldest first. Before > 0: those numbered below it;
// After > 0: those above it (Before wins). Limit: the newest that many of
// them (<= 0: 50; at most 500).
type ChatPage struct {
	Thread string
	Before int64
	After  int64
	Limit  int
}

// ChatLog keeps rooms' messages.
type ChatLog interface {
	// Append posts m: its Seq and At are given here, its ID too when it
	// has none. A reply's root must be a top-level message of the same
	// room; the root's count and repliers are kept with it, in the same
	// step. → the message as kept
	Append(ctx context.Context, m ChatMsg) (ChatMsg, error)
	// Get is one message; ErrNotFound when the room has none by that id.
	Get(ctx context.Context, tenant, room, id string) (ChatMsg, error)
	// Change reads the message, calls fn and keeps what fn left, as one
	// step; fn's error leaves it as it was. Room, ID, Seq, Thread and At
	// stay what they were.
	Change(ctx context.Context, tenant, room, id string, fn func(m *ChatMsg) error) (ChatMsg, error)
	// Page is a page of a room's messages.
	Page(ctx context.Context, tenant, room string, q ChatPage) ([]ChatMsg, error)
	// Last is the highest Seq in the room, 0 when it has none.
	Last(ctx context.Context, tenant, room string) (int64, error)
}

const (
	chatPageDefault = 50
	chatPageMax     = 500
	chatRepliers    = 5
)

func (q ChatPage) limit() int {
	switch {
	case q.Limit <= 0:
		return chatPageDefault
	case q.Limit > chatPageMax:
		return chatPageMax
	}
	return q.Limit
}

func chatNow() int64 { return time.Now().UnixMilli() }

// a reply counted on its root
func countReply(root *ChatMsg, reply ChatMsg) {
	root.Replies++
	root.LastReply = reply.At
	who := reply.From.ID
	for i, r := range root.Repliers {
		if r == who {
			root.Repliers = append(root.Repliers[:i:i], root.Repliers[i+1:]...)
			break
		}
	}
	root.Repliers = append(root.Repliers, who)
	if len(root.Repliers) > chatRepliers {
		root.Repliers = root.Repliers[len(root.Repliers)-chatRepliers:]
	}
}

func checkChat(m ChatMsg) error {
	if m.Tenant == "" || m.Room == "" {
		return errors.New("store: a message needs its tenant and room")
	}
	if m.From.ID == "" {
		return errors.New("store: a message needs who wrote it")
	}
	return nil
}

// ToggleReaction adds who's emoji to m, or takes it away when it is there.
// → whether it is there now
func ToggleReaction(m *ChatMsg, emoji, who string) bool {
	if m.Reactions == nil {
		m.Reactions = map[string][]string{}
	}
	list := m.Reactions[emoji]
	for i, w := range list {
		if w == who {
			list = append(list[:i:i], list[i+1:]...)
			if len(list) == 0 {
				delete(m.Reactions, emoji)
			} else {
				m.Reactions[emoji] = list
			}
			if len(m.Reactions) == 0 {
				m.Reactions = nil
			}
			return false
		}
	}
	m.Reactions[emoji] = append(list, who)
	return true
}

// --- in memory

// MemChat is a ChatLog in memory.
type MemChat struct {
	mu    sync.Mutex
	rooms map[string][]ChatMsg // tenant/room → by Seq
}

func NewMemChat() *MemChat { return &MemChat{rooms: map[string][]ChatMsg{}} }

func chatKey(tenant, room string) string { return tenant + "\x00" + room }

func copyMsg(m ChatMsg) ChatMsg {
	if m.Reactions != nil {
		r := make(map[string][]string, len(m.Reactions))
		for k, v := range m.Reactions {
			r[k] = append([]string(nil), v...)
		}
		m.Reactions = r
	}
	m.Repliers = append([]string(nil), m.Repliers...)
	if len(m.Repliers) == 0 {
		m.Repliers = nil
	}
	return m
}

func (c *MemChat) find(list []ChatMsg, id string) int {
	for i := range list {
		if list[i].ID == id {
			return i
		}
	}
	return -1
}

func (c *MemChat) Append(_ context.Context, m ChatMsg) (ChatMsg, error) {
	if err := checkChat(m); err != nil {
		return m, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	k := chatKey(m.Tenant, m.Room)
	list := c.rooms[k]
	root := -1
	if m.Thread != "" {
		root = c.find(list, m.Thread)
		if root < 0 || list[root].Thread != "" {
			return m, ErrNotFound
		}
	}
	if m.ID == "" {
		m.ID = newID()
	} else if c.find(list, m.ID) >= 0 {
		return m, ErrConflict
	}
	m.Seq = int64(len(list)) + 1
	m.At = chatNow()
	m = copyMsg(m)
	if root >= 0 {
		countReply(&list[root], m)
	}
	c.rooms[k] = append(list, m)
	return copyMsg(m), nil
}

func (c *MemChat) Get(_ context.Context, tenant, room, id string) (ChatMsg, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := c.rooms[chatKey(tenant, room)]
	if i := c.find(list, id); i >= 0 {
		return copyMsg(list[i]), nil
	}
	return ChatMsg{}, ErrNotFound
}

func (c *MemChat) Change(_ context.Context, tenant, room, id string, fn func(m *ChatMsg) error) (ChatMsg, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := c.rooms[chatKey(tenant, room)]
	i := c.find(list, id)
	if i < 0 {
		return ChatMsg{}, ErrNotFound
	}
	next := copyMsg(list[i])
	if err := fn(&next); err != nil {
		return ChatMsg{}, err
	}
	keepPlace(&next, list[i])
	list[i] = copyMsg(next)
	return copyMsg(next), nil
}

// what a change does not move
func keepPlace(next *ChatMsg, was ChatMsg) {
	next.Tenant, next.Room, next.ID, next.Seq, next.Thread, next.At = was.Tenant, was.Room, was.ID, was.Seq, was.Thread, was.At
}

func (c *MemChat) Page(_ context.Context, tenant, room string, q ChatPage) ([]ChatMsg, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var picked []ChatMsg
	for _, m := range c.rooms[chatKey(tenant, room)] {
		if m.Thread != q.Thread {
			continue
		}
		if q.Before > 0 {
			if m.Seq >= q.Before {
				continue
			}
		} else if q.After > 0 && m.Seq <= q.After {
			continue
		}
		picked = append(picked, copyMsg(m))
	}
	if n := q.limit(); len(picked) > n {
		if q.Before <= 0 && q.After > 0 {
			picked = picked[:n] // what came next, from the one after
		} else {
			picked = picked[len(picked)-n:]
		}
	}
	return picked, nil
}

func (c *MemChat) Last(_ context.Context, tenant, room string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(len(c.rooms[chatKey(tenant, room)])), nil
}

// --- SQLite

// SQLiteChat is a ChatLog in sliqtly.db's chat_msgs.
type SQLiteChat struct {
	s  *SQLiteStore
	mu sync.Mutex // Append and Change of this process in order
}

func NewSQLiteChat(s *SQLiteStore) *SQLiteChat { return &SQLiteChat{s: s} }

func (c *SQLiteChat) open() error {
	if !c.s.open() {
		return ErrClosed
	}
	return nil
}

func scanMsg(text string) (ChatMsg, error) {
	var m ChatMsg
	err := json.Unmarshal([]byte(text), &m)
	return m, err
}

func getMsg(ctx context.Context, q querier, tenant, room, id string) (ChatMsg, error) {
	var text string
	err := q.QueryRowContext(ctx, `SELECT doc FROM chat_msgs WHERE tenant = ? AND room = ? AND id = ?`, tenant, room, id).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatMsg{}, ErrNotFound
	}
	if err != nil {
		return ChatMsg{}, err
	}
	m, err := scanMsg(text)
	m.Tenant = tenant
	return m, err
}

func putMsg(ctx context.Context, tx *sql.Tx, m ChatMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO chat_msgs (tenant, room, seq, id, thread, at, doc) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (tenant, room, seq) DO UPDATE SET doc = excluded.doc`, m.Tenant, m.Room, m.Seq, m.ID, m.Thread, m.At, string(b))
	return err
}

func (c *SQLiteChat) Append(ctx context.Context, m ChatMsg) (ChatMsg, error) {
	if err := checkChat(m); err != nil {
		return m, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s.state.RLock()
	defer c.s.state.RUnlock()
	if c.s.closed {
		return m, ErrClosed
	}
	tx, err := c.s.db.BeginTx(ctx, nil)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	var root ChatMsg
	if m.Thread != "" {
		if root, err = getMsg(ctx, tx, m.Tenant, m.Room, m.Thread); err != nil {
			return m, err
		}
		if root.Thread != "" {
			return m, ErrNotFound
		}
	}
	if m.ID == "" {
		m.ID = newID()
	} else if _, err := getMsg(ctx, tx, m.Tenant, m.Room, m.ID); err == nil {
		return m, ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return m, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(seq), 0) + 1 FROM chat_msgs WHERE tenant = ? AND room = ?`, m.Tenant, m.Room).Scan(&m.Seq); err != nil {
		return m, err
	}
	m.At = chatNow()
	m = copyMsg(m)
	if err := putMsg(ctx, tx, m); err != nil {
		return m, err
	}
	if m.Thread != "" {
		countReply(&root, m)
		if err := putMsg(ctx, tx, root); err != nil {
			return m, err
		}
	}
	return m, tx.Commit()
}

func (c *SQLiteChat) Get(ctx context.Context, tenant, room, id string) (ChatMsg, error) {
	if err := c.open(); err != nil {
		return ChatMsg{}, err
	}
	return getMsg(ctx, c.s.db, tenant, room, id)
}

func (c *SQLiteChat) Change(ctx context.Context, tenant, room, id string, fn func(m *ChatMsg) error) (ChatMsg, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s.state.RLock()
	defer c.s.state.RUnlock()
	if c.s.closed {
		return ChatMsg{}, ErrClosed
	}
	tx, err := c.s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChatMsg{}, err
	}
	defer tx.Rollback()
	was, err := getMsg(ctx, tx, tenant, room, id)
	if err != nil {
		return ChatMsg{}, err
	}
	next := copyMsg(was)
	if err := fn(&next); err != nil {
		return ChatMsg{}, err
	}
	keepPlace(&next, was)
	next = copyMsg(next)
	if err := putMsg(ctx, tx, next); err != nil {
		return ChatMsg{}, err
	}
	return next, tx.Commit()
}

func (c *SQLiteChat) Page(ctx context.Context, tenant, room string, q ChatPage) ([]ChatMsg, error) {
	if err := c.open(); err != nil {
		return nil, err
	}
	where := `tenant = ? AND room = ? AND thread = ?`
	args := []any{tenant, room, q.Thread}
	order := "DESC"
	switch {
	case q.Before > 0:
		where += ` AND seq < ?`
		args = append(args, q.Before)
	case q.After > 0:
		where += ` AND seq > ?`
		args = append(args, q.After)
		order = "ASC"
	}
	args = append(args, q.limit())
	rows, err := c.s.db.QueryContext(ctx, fmt.Sprintf(`SELECT doc FROM chat_msgs WHERE %s ORDER BY seq %s LIMIT ?`, where, order), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatMsg
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		m, err := scanMsg(text)
		if err != nil {
			return nil, fmt.Errorf("chat %s/%s: %w", room, strings.TrimSpace(text[:min(len(text), 40)]), err)
		}
		m.Tenant = tenant
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (c *SQLiteChat) Last(ctx context.Context, tenant, room string) (int64, error) {
	if err := c.open(); err != nil {
		return 0, err
	}
	var n int64
	err := c.s.db.QueryRowContext(ctx, `SELECT coalesce(max(seq), 0) FROM chat_msgs WHERE tenant = ? AND room = ?`, tenant, room).Scan(&n)
	return n, err
}
