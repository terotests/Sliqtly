// SPDX-License-Identifier: AGPL-3.0-or-later

// The cloud's shared rooms and their chat, for the editor at /editor:
//
//	POST /editor/api/rooms/<op>  {arguments}   the same operations as a
//	                                           server of one's own's
//	                                           /api/rooms/<op> (roomsapi.go)
//
// made for the account the request's sign-in cookie names (editor.go), in
// the cloud's one tenant: an account sees the rooms it is a member of, by
// its user id or by the address it signed in with (verified). Who speaks in
// the chat is that account, by the name its Google account has: the page
// chooses only the avatar and colour. A page with a room's chat open reads
// the new messages from Firestore itself (firestore.rules room_chat,
// web/cloudchat.js); everything it writes comes through here.

package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

// calls per account in ten minutes: a chat left open says "here" twice a
// minute, and a busy one posts and reacts besides
var cloudRoomsLimit = rateLimiter(900, 10*time.Minute)

// the account as the rooms know it
func cloudPrincipal(t *IDToken) store.Principal {
	p := store.Principal{UserID: t.UID, TenantID: cloudTenant}
	if t.Verified && t.Email != "" {
		p.Email = strings.ToLower(t.Email)
	}
	return p
}

// the name a signed-in person speaks by: their Google account's, else the
// address's first part
func cloudSpeakerName(t *IDToken) string {
	if n := cleanName(t.Name); n != "" {
		return n
	}
	if i := strings.Index(t.Email, "@"); i > 0 {
		return cleanName(t.Email[:i])
	}
	return "Someone"
}

func serveCloudRooms(env *Env, g *editorGate, w http.ResponseWriter, r *http.Request, op string) {
	rs := env.cloudRooms
	if rs == nil || !findRoomTool(op) {
		editorJSON(w, 404, map[string]string{"error": "No such call."})
		return
	}
	if r.Method != http.MethodPost || !sameSite(env, r) {
		editorJSON(w, 403, map[string]string{"error": "Not allowed."})
		return
	}
	t := g.user(r)
	if t == nil {
		editorJSON(w, 401, map[string]string{"error": "Sign in again.", "code": "signed-out"})
		return
	}
	if cloudRoomsLimit(t.UID) != "" {
		editorJSON(w, 429, map[string]string{"error": "Too many requests; try again in a few minutes."})
		return
	}
	a := map[string]any{}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&a); err != nil && !errors.Is(err, io.EOF) {
		editorJSON(w, 400, map[string]string{"error": "A JSON object is expected."})
		return
	}
	if a == nil { // a body of null
		a = map[string]any{}
	}
	// the person is the signed-in account, whatever the page says; its
	// avatar and colour are the page's
	as, _ := a["as"].(map[string]any)
	if as == nil {
		as = map[string]any{}
	}
	as["id"], as["name"] = t.UID, cloudSpeakerName(t)
	a["as"] = as
	out, err := rs.callFor(r.Context(), cloudPrincipal(t), viaPage, op, a)
	var re roomErr
	switch {
	case errors.As(err, &re):
		editorJSON(w, 400, map[string]string{"error": re.msg})
	case err != nil:
		log.Printf("editor rooms %s: %v", op, err)
		editorJSON(w, 503, map[string]string{"error": "The room could not be reached; try again in a moment."})
	default:
		editorJSON(w, 200, out)
	}
}
