// SPDX-License-Identifier: AGPL-3.0-or-later

// Games' speech (the speak tool, games.go): Google Cloud Text-to-Speech,
// as the service's own account (Cloud Run's), billed to the project. Off
// unless SLIQTLY_GAMES_TTS=google; each account still needs an allowance
// from the admin page (the admins have no limit). The project needs the
// Text-to-Speech API turned on (texttospeech.googleapis.com).

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2/google"
)

const gamesTTSURL = "https://texttospeech.googleapis.com/v1/text:synthesize"

var (
	gamesLangRe  = regexp.MustCompile(`^[a-z]{2,3}(-[A-Z]{2})?$`)
	gamesVoiceRe = regexp.MustCompile(`^[a-z]{2,3}-[A-Z]{2}-[A-Za-z0-9-]{1,40}$`)
)

// a short language ("fi") as the code Google's voices are listed under
var gamesLangCodes = map[string]string{
	"fi": "fi-FI", "en": "en-US", "es": "es-ES", "sv": "sv-SE", "de": "de-DE", "fr": "fr-FR",
	"it": "it-IT", "nb": "nb-NO", "no": "nb-NO", "da": "da-DK", "et": "et-EE", "nl": "nl-NL", "pt": "pt-PT",
}

func gamesTTSRequest(text, lang, voice string) (map[string]any, error) {
	if !gamesLangRe.MatchString(lang) {
		return nil, roomErr{"lang: e.g. fi, en, es or a full code such as en-GB."}
	}
	code := lang
	if c, ok := gamesLangCodes[lang]; ok {
		code = c
	} else if !strings.Contains(lang, "-") {
		return nil, roomErr{"lang: " + lang + " needs its country too, e.g. " + lang + "-" + strings.ToUpper(lang) + "."}
	}
	v := map[string]any{"languageCode": code}
	if voice != "" {
		if !gamesVoiceRe.MatchString(voice) {
			return nil, roomErr{"voice: a Google voice name such as fi-FI-Wavenet-A."}
		}
		v["name"] = voice
		parts := strings.SplitN(voice, "-", 3)
		v["languageCode"] = parts[0] + "-" + parts[1]
	}
	return map[string]any{
		"input": map[string]any{"text": text},
		"voice": v,
		// LINEAR16 comes with its WAV header, which the engine plays itself
		// (web audio.js, Android Sound.java)
		"audioConfig": map[string]any{"audioEncoding": "LINEAR16", "sampleRateHertz": 24000},
	}, nil
}

// speech by Google Cloud TTS, with the given client (one that signs its
// requests as the service's account)
func gamesGoogleSpeaker(client *http.Client, endpoint string) gamesSpeaker {
	return func(ctx context.Context, text, lang, voice string) ([]byte, error) {
		req, err := gamesTTSRequest(text, lang, voice)
		if err != nil {
			return nil, err
		}
		body, _ := json.Marshal(req)
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", "application/json")
		res, err := client.Do(r)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		json.Unmarshal(b, &e)
		if res.StatusCode == 400 {
			// a voice or language Google does not have: the caller's to fix
			return nil, roomErr{"Speech: " + e.Error.Message}
		}
		if res.StatusCode == 403 || res.StatusCode == 429 {
			// the API not turned on in the project, no permission, or its
			// quota: said as Google says it, so that the admin knows what to do
			log.Printf("text-to-speech: %d %s", res.StatusCode, strings.TrimSpace(string(b[:min(len(b), 300)])))
			return nil, roomErr{fmt.Sprintf("Speech is not available on the server right now (Google Text-to-Speech %d: %s)", res.StatusCode, e.Error.Message)}
		}
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("text-to-speech: %d %s", res.StatusCode, strings.TrimSpace(string(b[:min(len(b), 300)])))
		}
		var out struct {
			AudioContent string `json:"audioContent"`
		}
		if err := json.Unmarshal(b, &out); err != nil || out.AudioContent == "" {
			return nil, errors.New("text-to-speech: no audio in the answer")
		}
		return base64.StdEncoding.DecodeString(out.AudioContent)
	}
}

// the speaker SLIQTLY_GAMES_TTS asks for: "google", or nil for none
func gamesSpeakerFromEnv(ctx context.Context, kind string) (gamesSpeaker, error) {
	switch strings.TrimSpace(kind) {
	case "", "off", "none":
		return nil, nil
	case "google":
		c, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return nil, err
		}
		return gamesGoogleSpeaker(c, gamesTTSURL), nil
	}
	return nil, fmt.Errorf("SLIQTLY_GAMES_TTS=%q: google or off", kind)
}
