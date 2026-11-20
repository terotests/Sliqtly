// SPDX-License-Identifier: AGPL-3.0-or-later

// Nysse (Tampere's buses and trams) for Warden's home display
// (wardendisplay.go), from ITS Factory's open Journeys API
// (data.itsfactory.fi, no key): the stops by name (stop-points, all of them
// kept for a day) and the next departures from a stop as the vehicles on the
// way report them (stop-monitoring, kept for 20 seconds). A vehicle shows
// once its journey has begun, so a stop sees the departures of roughly the
// next half hour.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const nysseAPI = "https://data.itsfactory.fi/journeys/api/1"

var nysseClient = &http.Client{Timeout: 12 * time.Second}

type nysseStop struct {
	ID, Name string
	Lat, Lon float64
}

type nysseDeparture struct {
	Line, To string
	At       time.Time
	Live     bool
}

// tests replace these
var (
	wardenFindStops  = nysseFindStops
	wardenDepartures = nysseDepartures
	wardenStopLines  = nysseStopLines
)

var (
	nysseMu       sync.Mutex
	nysseStops    []nysseStop
	nysseStopsAt  time.Time
	nysseDeps     = map[string]nysseDepCache{}
	nysseLines    = map[string]nysseLineCache{}
	nyssePlatform = regexp.MustCompile(` [A-Z]$`)
)

type nysseDepCache struct {
	at   time.Time
	deps []nysseDeparture
}

type nysseLineCache struct {
	at    time.Time
	lines []string
}

func nysseGet(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", nysseAPI+path, nil)
	if err != nil {
		return err
	}
	res, err := nysseClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("nysse %s: %s", path, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// every stop, kept for a day
func nysseAllStops(ctx context.Context) ([]nysseStop, error) {
	nysseMu.Lock()
	if nysseStops != nil && time.Since(nysseStopsAt) < 24*time.Hour {
		l := nysseStops
		nysseMu.Unlock()
		return l, nil
	}
	nysseMu.Unlock()
	var in struct {
		Body []struct {
			Name      string `json:"name"`
			ShortName string `json:"shortName"`
			Location  string `json:"location"`
		} `json:"body"`
	}
	if err := nysseGet(ctx, "/stop-points", &in); err != nil {
		return nil, err
	}
	l := make([]nysseStop, 0, len(in.Body))
	for _, b := range in.Body {
		s := nysseStop{ID: b.ShortName, Name: b.Name}
		if la, lo, ok := strings.Cut(b.Location, ","); ok {
			s.Lat, _ = strconv.ParseFloat(la, 64)
			s.Lon, _ = strconv.ParseFloat(lo, 64)
		}
		l = append(l, s)
	}
	nysseMu.Lock()
	nysseStops, nysseStopsAt = l, time.Now()
	nysseMu.Unlock()
	return l, nil
}

// stops by their code, or by a name (each word in it), at most 12
func nysseFindStops(ctx context.Context, q string) ([]nysseStop, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, nil
	}
	all, err := nysseAllStops(ctx)
	if err != nil {
		return nil, err
	}
	words := strings.Fields(q)
	out := []nysseStop{}
	for _, s := range all {
		if strings.ToLower(s.ID) == q {
			return []nysseStop{s}, nil
		}
		n := strings.ToLower(s.Name)
		ok := true
		for _, w := range words {
			if !strings.Contains(n, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, s)
		}
	}
	// names that begin with the words first
	sort.SliceStable(out, func(a, b int) bool {
		pa, pb := strings.HasPrefix(strings.ToLower(out[a].Name), q), strings.HasPrefix(strings.ToLower(out[b].Name), q)
		if pa != pb {
			return pa
		}
		return out[a].Name < out[b].Name
	})
	if len(out) > 12 {
		out = out[:12]
	}
	return out, nil
}

// a stop's name without its platform letter (Keskustori H → Keskustori)
func nysseStopName(ctx context.Context, id string) string {
	all, err := nysseAllStops(ctx)
	if err != nil {
		return ""
	}
	for _, s := range all {
		if s.ID == id {
			return nyssePlatform.ReplaceAllString(s.Name, "")
		}
	}
	return ""
}

// the next departures from a stop, soonest first
func nysseDepartures(ctx context.Context, stop string, now time.Time) ([]nysseDeparture, error) {
	nysseMu.Lock()
	if c, ok := nysseDeps[stop]; ok && time.Since(c.at) < 20*time.Second {
		nysseMu.Unlock()
		return c.deps, nil
	}
	nysseMu.Unlock()
	var in struct {
		Body map[string][]struct {
			LineRef              string `json:"lineRef"`
			DestinationShortName string `json:"destinationShortName"`
			Call                 struct {
				ExpectedDepartureTime string `json:"expectedDepartureTime"`
				AimedDepartureTime    string `json:"aimedDepartureTime"`
				ExpectedArrivalTime   string `json:"expectedArrivalTime"`
			} `json:"call"`
		} `json:"body"`
	}
	if err := nysseGet(ctx, "/stop-monitoring?stops="+stop, &in); err != nil {
		return nil, err
	}
	out := []nysseDeparture{}
	for _, v := range in.Body[stop] {
		at, live := time.Time{}, false
		for i, t := range []string{v.Call.ExpectedDepartureTime, v.Call.ExpectedArrivalTime, v.Call.AimedDepartureTime} {
			if p, err := time.Parse(time.RFC3339, t); err == nil {
				at, live = p, i < 2
				break
			}
		}
		if at.IsZero() || at.Before(now.Add(-time.Minute)) {
			continue
		}
		// the last stop's own departures are its arrivals
		if v.DestinationShortName == stop {
			continue
		}
		out = append(out, nysseDeparture{Line: v.LineRef, To: nysseStopName(ctx, v.DestinationShortName), At: at, Live: live})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].At.Before(out[b].At) })
	nysseMu.Lock()
	nysseDeps[stop] = nysseDepCache{time.Now(), out}
	nysseMu.Unlock()
	return out, nil
}

// the lines that stop at a stop (its scheduled journeys'), kept for a day;
// those seen on the way there now when the schedule is out of reach
func nysseStopLines(ctx context.Context, stop string) []string {
	nysseMu.Lock()
	if c, ok := nysseLines[stop]; ok && time.Since(c.at) < 24*time.Hour {
		nysseMu.Unlock()
		return c.lines
	}
	nysseMu.Unlock()
	lines := []string{}
	var in struct {
		Body []struct {
			LineURL string `json:"lineUrl"`
		} `json:"body"`
	}
	if err := nysseGet(ctx, "/journeys?stopPointId="+stop+"&exclude-fields=calls", &in); err == nil {
		for _, j := range in.Body {
			l := j.LineURL[strings.LastIndex(j.LineURL, "/")+1:]
			if l != "" && !slices.Contains(lines, l) {
				lines = append(lines, l)
			}
		}
	}
	if len(lines) == 0 {
		if deps, err := nysseDepartures(ctx, stop, time.Now()); err == nil {
			for _, d := range deps {
				if !slices.Contains(lines, d.Line) {
					lines = append(lines, d.Line)
				}
			}
		}
	}
	sort.Slice(lines, func(a, b int) bool {
		na, _ := strconv.Atoi(strings.TrimRight(lines[a], "ABCDEFGHIJKLMNOPQRSTUVWXYZ"))
		nb, _ := strconv.Atoi(strings.TrimRight(lines[b], "ABCDEFGHIJKLMNOPQRSTUVWXYZ"))
		if na != nb {
			return na < nb
		}
		return lines[a] < lines[b]
	})
	nysseMu.Lock()
	nysseLines[stop] = nysseLineCache{time.Now(), lines}
	nysseMu.Unlock()
	return lines
}
