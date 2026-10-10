// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// The public viewer's layouts, kept (GET /api/view/<id>, rgr/View.rgr
// View.json). A deck is laid out again for every reader the CDN does not
// answer: it keeps an answer a minute, on each of its nodes, and in
// production a layout on a fresh instance took 5–17 s (every picture read
// from Storage and sampled, SVGs drawn). The layout depends on the deck's
// document and on this build only, so it is kept under their hash: on the
// instance, and (on the hosted service) in Storage at views/<id>.json,
// one per address, which the next layout of a changed deck replaces.
// A layout that read live data from the web, or missed a picture or file,
// is not kept.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"sync"
)

// what tells this build from another: the executable's size and time
// (each image's binary has its own); "" when it cannot be read, and then
// nothing is kept
var viewBuild = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s/%d/%d", version, st.Size(), st.ModTime().UnixNano())
})

// ViewKey is the key of a layout of `parts` (the address, its slides, the
// deck's document) made by this build; "" when no layout is kept
func (h *McpHost) ViewKey(parts string) string {
	b := viewBuild()
	if b == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(b + "\n" + parts))
	return hex.EncodeToString(sum[:])
}

// the instance keeps this many layouts, none larger than viewMemLarge;
// Storage none larger than viewStoreMax
const (
	viewMemMax   = 24
	viewMemLarge = 2 << 20
	viewStoreMax = 16 << 20
)

var viewMem = struct {
	sync.Mutex
	m     map[string]string
	order []string
}{m: map[string]string{}}

func viewMemPut(key, body string) {
	if len(body) > viewMemLarge {
		return
	}
	viewMem.Lock()
	defer viewMem.Unlock()
	if _, ok := viewMem.m[key]; !ok {
		viewMem.order = append(viewMem.order, key)
	}
	viewMem.m[key] = body
	for len(viewMem.order) > viewMemMax {
		delete(viewMem.m, viewMem.order[0])
		viewMem.order = viewMem.order[1:]
	}
}

// in Storage a layout is its key, a newline and the body
func viewObject(id string) string { return "views/" + id + ".json" }

// the stored layouts are the hosted service's: a server of one's own lays
// its decks out on the spot and keeps its folder to the decks
func (h *McpHost) viewStored() bool {
	return h.env != nil && h.env.Bucket != nil && h.env.LocalUser == ""
}

// ViewGet is the layout kept under key for address id; "" when none is
func (h *McpHost) ViewGet(id, key string) string {
	if key == "" {
		return ""
	}
	viewMem.Lock()
	body, ok := viewMem.m[key]
	viewMem.Unlock()
	if ok || !h.viewStored() {
		return body
	}
	b, err := h.env.Bucket.Read(h.ctx, viewObject(id), viewStoreMax+1)
	if err != nil || len(b) > viewStoreMax {
		return ""
	}
	nl := bytes.IndexByte(b, '\n')
	if nl < 0 || string(b[:nl]) != key {
		return ""
	}
	body = string(b[nl+1:])
	viewMemPut(key, body)
	return body
}

// ViewPut keeps the layout just made, unless what it read may read
// otherwise next time (McpHost.unsure)
func (h *McpHost) ViewPut(id, key, body string) {
	if key == "" || body == "" || h.unsure {
		return
	}
	viewMemPut(key, body)
	if !h.viewStored() || len(key)+1+len(body) > viewStoreMax {
		return
	}
	if err := h.env.Bucket.Save(h.ctx, viewObject(id), "application/json", []byte(key+"\n"+body), nil); err != nil {
		// the reader has the layout all the same; the next one makes it again
		log.Printf("view %s: not kept: %v", id, err)
	}
}
