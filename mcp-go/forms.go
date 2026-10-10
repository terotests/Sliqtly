// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// Questionnaires on a server of one's own (Sliqtly Curious, DESIGN §2c).
//
// A deck's forms/x.form.md is answered through links, each its own random
// key; only the key's SHA-256 is kept (store.Forms), and the link row is
// the only thing that ties a key to its deck and file:
//
//	/c/{code}   a QR code's short code (8 letters and digits)
//	/r/{token}  a long link (26 characters), one person's link
//	/t/{token}  the results, read-only, for viewers (JSON)
//
// The answer page is plain HTML (rgr/Forms.rgr → src/PresFormHtml.rgr) and
// posts back to the address it was opened from. Nothing it is sent names
// the deck, the file or the form: fields are q1, q2…, and the form version
// travels sealed (AES-GCM under the server's key, fresh nonce each time),
// so two links to one form share nothing but the questions' text.
//
// The deck's owner makes and lists links, reads results and responses
// under /api/forms/ (formsAPI).

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/terotests/sliqtly/mcp-go/store"
)

var (
	formCodePath    = regexp.MustCompile(`^/c/([A-Za-z0-9]{8})$`)
	formTokenPath   = regexp.MustCompile(`^/r/([a-z2-7]{26})$`)
	formResultsPath = regexp.MustCompile(`^/t/([a-z2-7]{26})$`)
	// look-alikes (0/O, 1/I/L) left out: a code is read off a screen
	formCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	formTokenCase    = base32.StdEncoding.WithPadding(base32.NoPadding)
	// wrong codes per address: a short code can be guessed at, slowly
	formMissLimit = rateLimiter(20, time.Minute)
	// responses per address
	formPostLimit = rateLimiter(60, time.Minute)
)

const (
	formFileMax = 256 << 10
	formPostMax = 64 << 10
	// a viewer's results show no counts until this many answered
	formResultsMin = 5
	// how long an answer page's sealed value is good for
	formSealTTL = 24 * time.Hour
)

func newFormCode() string {
	var b strings.Builder
	max := big.NewInt(int64(len(formCodeAlphabet)))
	for i := 0; i < 8; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b.WriteByte(formCodeAlphabet[n.Int64()])
	}
	return b.String()
}

func newFormToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(formTokenCase.EncodeToString(b))
}

func formHash(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// what a link's key is reached at
func formLinkPath(kind, key string) string {
	switch kind {
	case "code":
		return "/c/" + key
	case "results":
		return "/t/" + key
	}
	return "/r/" + key
}

// --- the sealed value

func (s *localServer) formAEAD(ctx context.Context) (cipher.AEAD, error) {
	key, err := s.env.Forms.Key(ctx)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal the form version the page shows, with when it was issued
func (s *localServer) formSeal(ctx context.Context, version string) (string, error) {
	a, err := s.formAEAD(ctx)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	plain := strconv.FormatInt(time.Now().Unix(), 10) + "\n" + version
	return base64.RawURLEncoding.EncodeToString(a.Seal(nonce, nonce, []byte(plain), []byte("sliqtly-form"))), nil
}

// the version a page was shown with; ok false when s is not ours or too old
func (s *localServer) formUnseal(ctx context.Context, sealed string) (version string, ok bool) {
	a, err := s.formAEAD(ctx)
	if err != nil {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil || len(raw) < a.NonceSize() {
		return "", false
	}
	plain, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], []byte("sliqtly-form"))
	if err != nil {
		return "", false
	}
	at, v, found := strings.Cut(string(plain), "\n")
	sec, err := strconv.ParseInt(at, 10, 64)
	if !found || err != nil || time.Since(time.Unix(sec, 0)) > formSealTTL {
		return "", false
	}
	return v, true
}

// --- the form behind a link

type formMeta struct {
	Title     string   `json:"title"`
	Version   string   `json:"version"`
	Audience  string   `json:"audience"`
	Anonymous bool     `json:"anonymous"`
	Opens     string   `json:"opens"`
	Closes    string   `json:"closes"`
	Limit     int      `json:"limit"`
	Once      string   `json:"once"`
	Results   string   `json:"results"`
	Lang      string   `json:"lang"`
	Questions int      `json:"questions"`
	IDs       []string `json:"ids"`
	Ready     bool     `json:"ready"`
	Warnings  []string `json:"warnings"`
}

func readFormMeta(src string) formMeta {
	var m formMeta
	json.Unmarshal([]byte(FormsGo_static_meta(src)), &m)
	return m
}

// "2026-10-20 09:00" or "2026-10-20" in the server's zone; ok false: none
// or unreadable
func formTime(s string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(s), time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// why the form takes no answers now ("" when it does), in en and fi
func formClosed(m formMeta, now time.Time) (en, fi string) {
	if !m.Ready || m.Questions == 0 {
		return "This questionnaire cannot be answered.", "Tähän kyselyyn ei voi vastata."
	}
	if t, ok := formTime(m.Opens); ok && now.Before(t) {
		return "This questionnaire opens " + t.Format("2.1.2006 15:04") + ".", "Kysely avautuu " + t.Format("2.1.2006 klo 15.04") + "."
	}
	if t, ok := formTime(m.Closes); ok && !now.Before(t) {
		return "This questionnaire is closed.", "Kysely on suljettu."
	}
	return "", ""
}

// a deck's form file: inside the share, a .form.md
func formFileOK(file string) bool {
	return file != "" && path.Clean(file) == file && !strings.HasPrefix(file, "../") && !strings.HasPrefix(file, "/") &&
		strings.HasSuffix(file, ".form.md")
}

func (s *localServer) formSource(ctx context.Context, deck, file string) (string, error) {
	if !shareID.MatchString(deck) || !formFileOK(file) {
		return "", store.ErrNotFound
	}
	data, err := s.bucket.Read(ctx, "shares/"+deck+"/"+file, formFileMax)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// --- the respondent's side

func formPageHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
}

func formNotFound(w http.ResponseWriter) {
	formPageHeaders(w)
	w.WriteHeader(http.StatusNotFound)
	io.WriteString(w, FormsGo_static_message("", "There is no questionnaire at this address.", "Tässä osoitteessa ei ole kyselyä."))
}

// one of the respondent's addresses; key from the path
func (s *localServer) formAnswer(w http.ResponseWriter, r *http.Request, key string, code bool) {
	if s.env.Forms == nil {
		http.NotFound(w, r)
		return
	}
	ip := clientIP(r)
	if code {
		key = strings.ToUpper(key)
	}
	l, err := s.env.Forms.Link(r.Context(), formHash(key))
	if err == nil && (l.Kind == "results" || (code != (l.Kind == "code"))) {
		err = store.ErrNotFound
	}
	if errors.Is(err, store.ErrNotFound) {
		if code && formMissLimit(ip) != "" {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Too many tries.", http.StatusTooManyRequests)
			return
		}
		formNotFound(w)
		return
	}
	if err != nil {
		log.Printf("form link: %v", err)
		http.Error(w, "Something went wrong.", 500)
		return
	}
	src, err := s.formSource(r.Context(), l.Deck, l.File)
	if err != nil {
		formNotFound(w)
		return
	}
	m := readFormMeta(src)
	action := formLinkPath(l.Kind, key)
	say := func(status int, en, fi string) {
		formPageHeaders(w)
		w.WriteHeader(status)
		io.WriteString(w, FormsGo_static_message(src, en, fi))
	}
	if r.Method == http.MethodGet && r.URL.Query().Has("sent") {
		formPageHeaders(w)
		io.WriteString(w, FormsGo_static_thanks(src))
		return
	}
	if l.Revoked {
		say(http.StatusGone, "This questionnaire is closed.", "Kysely on suljettu.")
		return
	}
	if en, fi := formClosed(m, time.Now()); en != "" {
		say(http.StatusForbidden, en, fi)
		return
	}
	if l.Once && l.Used {
		say(http.StatusConflict, "This link has been used to answer already.", "Tällä linkillä on jo vastattu.")
		return
	}
	if m.Once == "soft" {
		if c, err := r.Cookie("answered"); err == nil && c.Value == "1" {
			say(http.StatusConflict, "You have answered this questionnaire already. Thank you!", "Olet jo vastannut tähän kyselyyn. Kiitos!")
			return
		}
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		sealed, err := s.formSeal(r.Context(), m.Version)
		if err != nil {
			log.Printf("form seal: %v", err)
			http.Error(w, "Something went wrong.", 500)
			return
		}
		formPageHeaders(w)
		io.WriteString(w, FormsGo_static_page(src, action, sealed))
	case http.MethodPost:
		if formPostLimit(ip) != "" {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Too many answers from here; try again in a minute.", http.StatusTooManyRequests)
			return
		}
		s.formPost(w, r, l, src, m, action)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *localServer) formPost(w http.ResponseWriter, r *http.Request, l store.FormLink, src string, m formMeta, action string) {
	r.Body = http.MaxBytesReader(w, r.Body, formPostMax)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The answers could not be read.", http.StatusBadRequest)
		return
	}
	names := make([]string, 0, len(r.PostForm))
	for k := range r.PostForm {
		names = append(names, k)
	}
	sort.Strings(names)
	var pairs [][2]string
	for _, k := range names {
		for _, v := range r.PostForm[k] {
			pairs = append(pairs, [2]string{k, v})
		}
	}
	fields, _ := json.Marshal(pairs)
	// a page from before the form changed, or one this server did not
	// issue, comes back with the answers kept and a word on why
	notice := ""
	if v, ok := s.formUnseal(r.Context(), r.PostForm.Get("s")); !ok || v != m.Version {
		notice = "The questionnaire changed or the page was open for long. Please check your answers and send again."
		if m.Lang == "fi" {
			notice = "Kysely on muuttunut tai sivu oli auki pitkään. Tarkista vastauksesi ja lähetä uudelleen."
		}
	}
	sealed, err := s.formSeal(r.Context(), m.Version)
	if err != nil {
		log.Printf("form seal: %v", err)
		http.Error(w, "Something went wrong.", 500)
		return
	}
	var out struct {
		Status  string          `json:"status"`
		HTML    string          `json:"html"`
		Record  json.RawMessage `json:"record"`
		Deltas  json.RawMessage `json:"deltas"`
		Version string          `json:"version"`
	}
	if err := json.Unmarshal([]byte(FormsGo_static_post(src, string(fields), r.PostForm.Get("shown"), action, sealed, notice)), &out); err != nil {
		log.Printf("form post: %v", err)
		http.Error(w, "Something went wrong.", 500)
		return
	}
	if out.Status != "saved" {
		formPageHeaders(w)
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, out.HTML)
		return
	}
	resp := store.FormResponse{Deck: l.Deck, File: l.File, Version: out.Version, Record: string(out.Record), Deltas: string(out.Deltas)}
	if !m.Anonymous {
		resp.Link = l.ID
	}
	once := ""
	if l.Once {
		once = l.Hash
	}
	_, err = s.env.Forms.Submit(r.Context(), resp, m.Limit, once)
	say := func(status int, en, fi string) {
		formPageHeaders(w)
		w.WriteHeader(status)
		io.WriteString(w, FormsGo_static_message(src, en, fi))
	}
	switch {
	case errors.Is(err, store.ErrFormFull):
		say(http.StatusConflict, "This questionnaire has all the answers it takes.", "Kyselyyn on tullut kaikki vastaukset, jotka se ottaa.")
		return
	case errors.Is(err, store.ErrLinkUsed):
		say(http.StatusConflict, "This link has been used to answer already.", "Tällä linkillä on jo vastattu.")
		return
	case err != nil:
		log.Printf("form submit: %v", err)
		http.Error(w, "Something went wrong.", 500)
		return
	}
	s.formChanged(l.Deck)
	if m.Once == "soft" {
		http.SetCookie(w, &http.Cookie{Name: "answered", Value: "1", Path: action, MaxAge: 365 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	// back to a page that only thanks, so a reload sends nothing again
	http.Redirect(w, r, action+"?sent", http.StatusSeeOther)
}

// /t/{token}: a viewer's results, as JSON
func (s *localServer) formResults(w http.ResponseWriter, r *http.Request, key string) {
	if s.env.Forms == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	l, err := s.env.Forms.Link(r.Context(), formHash(key))
	if err != nil || l.Kind != "results" || l.Revoked {
		writeJSON(w, 404, map[string]string{"error": "no results here"})
		return
	}
	src, err := s.formSource(r.Context(), l.Deck, l.File)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "no results here"})
		return
	}
	m := readFormMeta(src)
	switch m.Results {
	case "owner":
		writeJSON(w, 403, map[string]string{"error": "the results are for the questionnaire's owner"})
		return
	case "after-close":
		if t, ok := formTime(m.Closes); !ok || time.Now().Before(t) {
			writeJSON(w, 403, map[string]string{"error": "the results are shown when the questionnaire closes"})
			return
		}
	}
	rows, err := s.env.Forms.Tally(r.Context(), l.Deck, l.File)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "something went wrong"})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.WriteString(w, FormsGo_static_results(src, store.TallyJSON(rows), formResultsMin))
}

// The deck's open pages hear that its answers changed (localevents.go), and
// its charts that read them (form: sources) read them again.
func (s *localServer) formChanged(deck string) {
	if s.hub != nil {
		s.hub.publishForm(deck)
	}
}

// --- the owner's side: /api/forms/…

func formsQuery(r *http.Request) (deck, file string, err error) {
	q := r.URL.Query()
	deck, file = q.Get("deck"), q.Get("file")
	if !shareID.MatchString(deck) || !formFileOK(file) {
		return "", "", fail(400, "", "deck and file (a .form.md) are needed")
	}
	return deck, file, nil
}

// a reply that is not JSON (a CSV download)
type rawReply struct {
	Type, Name, Body string
}

func (s *localServer) formsAPI(r *http.Request, op string) (any, error) {
	if s.env.Forms == nil {
		return nil, fail(404, "", "questionnaires are not kept on this server")
	}
	ctx := r.Context()
	switch {
	case op == "links" && r.Method == http.MethodPost:
		body, err := readBody(r)
		if err != nil {
			return nil, err
		}
		deck, _ := body["deck"].(string)
		file, _ := body["file"].(string)
		kind, _ := body["kind"].(string)
		many := 1
		if v, there := body["count"]; there {
			n, ok := count(v)
			if !ok {
				return nil, fail(400, "", "count is a whole number")
			}
			many = n
		}
		return s.formNewLinks(ctx, deck, file, kind, many)
	case op == "links" && r.Method == http.MethodGet:
		deck, file, err := formsQuery(r)
		if err != nil {
			return nil, err
		}
		if _, err := s.own(ctx, deck); err != nil {
			return nil, err
		}
		links, err := s.env.Forms.Links(ctx, deck, file)
		if links == nil {
			links = []store.FormLink{}
		}
		return map[string]any{"links": links}, err
	case op == "revoke" && r.Method == http.MethodPost:
		body, err := readBody(r)
		if err != nil {
			return nil, err
		}
		deck, _ := body["deck"].(string)
		id, _ := body["id"].(string)
		revoked, ok := body["revoked"].(bool)
		if !ok {
			revoked = true
		}
		if _, err := s.own(ctx, deck); err != nil {
			return nil, err
		}
		if err := s.env.Forms.SetRevoked(ctx, deck, id, revoked); errors.Is(err, store.ErrNotFound) {
			return nil, fail(404, "", "no such link")
		} else if err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	case op == "results" && r.Method == http.MethodGet:
		deck, file, err := formsQuery(r)
		if err != nil {
			return nil, err
		}
		if _, err := s.own(ctx, deck); err != nil {
			return nil, err
		}
		src, err := s.formSource(ctx, deck, file)
		if err != nil {
			return nil, fail(404, "", "no such questionnaire file")
		}
		rows, err := s.env.Forms.Tally(ctx, deck, file)
		if err != nil {
			return nil, err
		}
		// the owner sees every count, 0 and 1 too
		var res map[string]any
		json.Unmarshal([]byte(FormsGo_static_results(src, store.TallyJSON(rows), 0)), &res)
		res["warnings"] = readFormMeta(src).Warnings
		return res, nil
	case op == "responses" && r.Method == http.MethodGet:
		deck, file, err := formsQuery(r)
		if err != nil {
			return nil, err
		}
		if _, err := s.own(ctx, deck); err != nil {
			return nil, err
		}
		list, err := s.env.Forms.Responses(ctx, deck, file)
		if err != nil {
			return nil, err
		}
		if r.URL.Query().Get("format") == "csv" {
			src, _ := s.formSource(ctx, deck, file)
			var b strings.Builder
			if err := writeFormCSV(&b, readFormMeta(src).IDs, list); err != nil {
				return nil, err
			}
			return rawReply{Type: "text/csv; charset=utf-8", Name: strings.TrimSuffix(path.Base(file), ".form.md") + ".csv", Body: b.String()}, nil
		}
		if list == nil {
			list = []store.FormResponse{}
		}
		return map[string]any{"responses": list}, nil
	case op == "responses" && r.Method == http.MethodDelete:
		deck, file, err := formsQuery(r)
		if err != nil {
			return nil, err
		}
		if _, err := s.own(ctx, deck); err != nil {
			return nil, err
		}
		if err := s.env.Forms.Remove(ctx, deck, file, r.URL.Query().Get("id")); errors.Is(err, store.ErrNotFound) {
			return nil, fail(404, "", "no such response")
		} else if err != nil {
			return nil, err
		}
		s.formChanged(deck)
		return map[string]bool{"ok": true}, nil
	}
	return nil, fail(404, "", "not found")
}

func (s *localServer) formNewLinks(ctx context.Context, deck, file, kind string, count int) (any, error) {
	if !shareID.MatchString(deck) || !formFileOK(file) {
		return nil, fail(400, "", "deck and file (a .form.md) are needed")
	}
	if _, err := s.own(ctx, deck); err != nil {
		return nil, err
	}
	src, err := s.formSource(ctx, deck, file)
	if err != nil {
		return nil, fail(404, "", "no such questionnaire file in the presentation")
	}
	m := readFormMeta(src)
	if m.Once == "account" {
		return nil, fail(400, "", "once: account needs sign-in, which this server's questionnaires do not have yet; use soft, link or none")
	}
	once := false
	switch kind {
	case "code", "token", "results":
		if count != 1 {
			return nil, fail(400, "", "one "+kind+" link at a time")
		}
	case "person":
		if count < 1 || count > 1000 {
			return nil, fail(400, "", "1 to 1000 person links at a time")
		}
		once = m.Once != "none"
	default:
		return nil, fail(400, "", "kind is code, token, person or results")
	}
	type made struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	out := make([]made, 0, count)
	now := time.Now().UnixMilli()
	for i := 0; i < count; i++ {
		var key string
		var err error
		// a code is short enough to meet another now and then: draw again
		for try := 0; try < 5; try++ {
			if kind == "code" {
				key = newFormCode()
			} else {
				key = newFormToken()
			}
			h := formHash(key)
			err = s.env.Forms.AddLink(ctx, store.FormLink{Hash: h, ID: h[:16], Deck: deck, File: file, Kind: kind, Once: once, Created: now})
			if !errors.Is(err, store.ErrConflict) {
				break
			}
		}
		if err != nil {
			return nil, err
		}
		p := formLinkPath(kind, key)
		out = append(out, made{ID: formHash(key)[:16], Kind: kind, Path: p, URL: s.env.BaseURL + p})
	}
	return map[string]any{"links": out, "warnings": m.Warnings}, nil
}

// the responses as CSV: id, time, version, then the questions in the
// form's order (and their "other" text), then anything the form no longer
// asks; a multi's choices joined by "; "
func writeFormCSV(w io.Writer, ids []string, list []store.FormResponse) error {
	cols := append([]string(nil), ids...)
	seen := map[string]bool{}
	for _, c := range cols {
		seen[c] = true
	}
	records := make([]map[string]any, len(list))
	for i, r := range list {
		json.Unmarshal([]byte(r.Record), &records[i])
		var keys []string
		for k := range records[i] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	// "x.other" right after x
	sort.SliceStable(cols, func(i, j int) bool { return colRank(cols, cols[i]) < colRank(cols, cols[j]) })
	cw := csv.NewWriter(w)
	cw.Write(append([]string{"response", "time", "version"}, cols...))
	for i, r := range list {
		row := []string{r.ID, time.UnixMilli(r.At).UTC().Format(time.RFC3339), r.Version}
		for _, c := range cols {
			row = append(row, csvCell(records[i][c]))
		}
		cw.Write(row)
	}
	cw.Flush()
	return cw.Error()
}

func colRank(cols []string, c string) float64 {
	base, other := strings.CutSuffix(c, ".other")
	for i, x := range cols {
		if x == base {
			if other {
				return float64(i) + 0.5
			}
			return float64(i)
		}
	}
	return float64(len(cols))
}

func csvCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		// a spreadsheet would run "=…" as a formula
		if x != "" && strings.ContainsRune("=+-@\t\r", rune(x[0])) {
			return "'" + x
		}
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		parts := make([]string, len(x))
		for i, p := range x {
			parts[i] = csvCell(p)
		}
		return strings.Join(parts, "; ")
	}
	return fmt.Sprint(v)
}
