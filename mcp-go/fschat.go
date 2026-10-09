//go:build !nocloud

// SPDX-License-Identifier: AGPL-3.0-or-later

// The rooms' chat in Firestore (store.ChatLog), for the cloud:
//
//	room_chat/{tenant}~{room}             tenant, room, seq (the last number)
//	room_chat/{tenant}~{room}/msgs/{id}   seq, thread, at, touched, msg
//
// msg is the message as JSON (store.ChatMsg, as the API answers it but for
// its files' addresses). Only the server writes: a message is numbered in
// the same transaction that raises the room's seq, so numbers have no gaps
// and none twice. touched is when it was posted or last changed (an edit,
// a reaction, a reply counted on its root): a page that has the chat open
// listens to the messages touched since it opened (web/cloudchat.js), and
// firestore.rules lets it read them while it is a member of the room.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/terotests/sliqtly/mcp-go/store"
)

const roomChatCol = "room_chat"

type fsChat struct {
	c      *firestore.Client
	prefix string
}

func newFSChat(c *firestore.Client, prefix string) *fsChat { return &fsChat{c: c, prefix: prefix} }

// a room's chat document; firestore.rules reads the room from its id
func roomChatKey(tenant, room string) string { return tenant + "~" + room }

func (c *fsChat) head(tenant, room string) *firestore.DocumentRef {
	return c.c.Collection(c.prefix + roomChatCol).Doc(roomChatKey(tenant, room))
}

func (c *fsChat) msgs(tenant, room string) *firestore.CollectionRef {
	return c.head(tenant, room).Collection("msgs")
}

func fsChatCheck(tenant, room, id string) error {
	if err := fsName(roomChatCol, roomChatKey(tenant, room)); err != nil {
		return err
	}
	if id != "" {
		return fsName("msgs", id)
	}
	return nil
}

// the document a message is kept as
func fsMsgDoc(m store.ChatMsg, touched int64) (map[string]any, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return map[string]any{"seq": m.Seq, "thread": m.Thread, "at": m.At, "touched": touched, "msg": string(b)}, nil
}

func fsMsgOf(tenant string, snap *firestore.DocumentSnapshot) (store.ChatMsg, error) {
	var m store.ChatMsg
	s, _ := snap.Data()["msg"].(string)
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return m, err
	}
	m.Tenant = tenant
	return m, nil
}

func fsGetMsg(tx *firestore.Transaction, ref *firestore.DocumentRef, tenant string) (store.ChatMsg, error) {
	snap, err := tx.Get(ref)
	if status.Code(err) == codes.NotFound {
		return store.ChatMsg{}, store.ErrNotFound
	}
	if err != nil {
		return store.ChatMsg{}, err
	}
	return fsMsgOf(tenant, snap)
}

func (c *fsChat) Append(ctx context.Context, m store.ChatMsg) (store.ChatMsg, error) {
	if m.Tenant == "" || m.Room == "" || m.From.ID == "" {
		return m, errors.New("store: a message needs its tenant, room and who wrote it")
	}
	if m.ID == "" {
		m.ID = newShareID() + newShareID()
	}
	if err := fsChatCheck(m.Tenant, m.Room, m.ID); err != nil {
		return m, err
	}
	head := c.head(m.Tenant, m.Room)
	ref := c.msgs(m.Tenant, m.Room).Doc(m.ID)
	in := m
	var out store.ChatMsg
	err := c.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		m := in
		h, err := tx.Get(head)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}
		var seq int64
		if h != nil && h.Exists() {
			seq, _ = h.Data()["seq"].(int64)
		}
		if _, err := tx.Get(ref); err == nil {
			return store.ErrConflict
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		var root store.ChatMsg
		var rootRef *firestore.DocumentRef
		if m.Thread != "" {
			rootRef = c.msgs(m.Tenant, m.Room).Doc(m.Thread)
			if fsName("msgs", m.Thread) != nil {
				return store.ErrNotFound
			}
			if root, err = fsGetMsg(tx, rootRef, m.Tenant); err != nil {
				return err
			}
			if root.Thread != "" {
				return store.ErrNotFound
			}
		}
		m.Seq = seq + 1
		m.At = time.Now().UnixMilli()
		d, err := fsMsgDoc(m, m.At)
		if err != nil {
			return err
		}
		if rootRef != nil {
			fsCountReply(&root, m)
			rd, err := fsMsgDoc(root, m.At)
			if err != nil {
				return err
			}
			if err := tx.Set(rootRef, rd); err != nil {
				return err
			}
		}
		if err := tx.Set(ref, d); err != nil {
			return err
		}
		out = m
		return tx.Set(head, map[string]any{"tenant": m.Tenant, "room": m.Room, "seq": m.Seq, "at": m.At})
	}, firestore.MaxAttempts(50))
	return out, err
}

// a reply counted on its root, as store.MemChat counts it
func fsCountReply(root *store.ChatMsg, reply store.ChatMsg) {
	root.Replies++
	root.LastReply = reply.At
	who := reply.From.ID
	root.Repliers = slices.DeleteFunc(root.Repliers, func(r string) bool { return r == who })
	root.Repliers = append(root.Repliers, who)
	if n := len(root.Repliers); n > 5 {
		root.Repliers = root.Repliers[n-5:]
	}
}

func (c *fsChat) Get(ctx context.Context, tenant, room, id string) (store.ChatMsg, error) {
	if fsChatCheck(tenant, room, id) != nil {
		return store.ChatMsg{}, store.ErrNotFound
	}
	snap, err := c.msgs(tenant, room).Doc(id).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return store.ChatMsg{}, store.ErrNotFound
	}
	if err != nil {
		return store.ChatMsg{}, err
	}
	return fsMsgOf(tenant, snap)
}

func (c *fsChat) Change(ctx context.Context, tenant, room, id string, fn func(m *store.ChatMsg) error) (store.ChatMsg, error) {
	if fsChatCheck(tenant, room, id) != nil {
		return store.ChatMsg{}, store.ErrNotFound
	}
	ref := c.msgs(tenant, room).Doc(id)
	var out store.ChatMsg
	err := c.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		was, err := fsGetMsg(tx, ref, tenant)
		if err != nil {
			return err
		}
		next := was
		next.Reactions = cloneReactions(was.Reactions)
		next.Repliers = slices.Clone(was.Repliers)
		next.Files = slices.Clone(was.Files)
		next.Links = slices.Clone(was.Links)
		if err := fn(&next); err != nil {
			return err
		}
		next.Tenant, next.Room, next.ID, next.Seq, next.Thread, next.At = was.Tenant, was.Room, was.ID, was.Seq, was.Thread, was.At
		d, err := fsMsgDoc(next, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		out = next
		return tx.Set(ref, d)
	}, firestore.MaxAttempts(50))
	return out, err
}

func cloneReactions(r map[string][]string) map[string][]string {
	if r == nil {
		return nil
	}
	out := make(map[string][]string, len(r))
	for k, v := range r {
		out[k] = slices.Clone(v)
	}
	return out
}

func (c *fsChat) Page(ctx context.Context, tenant, room string, q store.ChatPage) ([]store.ChatMsg, error) {
	if fsChatCheck(tenant, room, "") != nil {
		return []store.ChatMsg{}, nil
	}
	n := q.Limit
	switch {
	case n <= 0:
		n = 50
	case n > 500:
		n = 500
	}
	fq := c.msgs(tenant, room).Where("thread", "==", q.Thread)
	asc := false
	switch {
	case q.Before > 0:
		fq = fq.Where("seq", "<", q.Before).OrderBy("seq", firestore.Desc)
	case q.After > 0:
		// what came next, from the one after
		fq, asc = fq.Where("seq", ">", q.After).OrderBy("seq", firestore.Asc), true
	default:
		fq = fq.OrderBy("seq", firestore.Desc)
	}
	snaps, err := fq.Limit(n).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	out := make([]store.ChatMsg, 0, len(snaps))
	for _, s := range snaps {
		m, err := fsMsgOf(tenant, s)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if !asc {
		slices.Reverse(out)
	}
	return out, nil
}

func (c *fsChat) Last(ctx context.Context, tenant, room string) (int64, error) {
	if fsChatCheck(tenant, room, "") != nil {
		return 0, nil
	}
	snap, err := c.head(tenant, room).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	seq, _ := snap.Data()["seq"].(int64)
	return seq, nil
}
