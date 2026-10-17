// SPDX-License-Identifier: AGPL-3.0-or-later

// Warden's places: the towns a space, or one of its wards, usually is in
// (home, a grandparent's, the summer cottage). The page's summary shows the
// weather of each; with none it shows Tampere's. A place is kept with its
// name and coordinates, in the space document: `places` on the space and on
// each ward, [{name, lat, lon}].
//
// add_place takes the coordinates when the caller has them (the page picks
// a town from Open-Meteo's geocoding); without them the name is looked up
// there, among Finland's places.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const wardenMaxPlaces = 8 // places of the space, and of each ward

// a place found by its name: its name as the geocoder writes it and where
// it is; ok false when nothing was found
type wardenPlace struct {
	Name     string
	Lat, Lon float64
}

// looks a town up in Finland; tests replace it
var wardenGeocode = openMeteoGeocode

var wardenGeoClient = &http.Client{Timeout: 10 * time.Second}

func openMeteoGeocode(ctx context.Context, name string) (wardenPlace, bool, error) {
	q := url.Values{"name": {name}, "count": {"1"}, "language": {"fi"}, "countryCode": {"FI"}, "format": {"json"}}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://geocoding-api.open-meteo.com/v1/search?"+q.Encode(), nil)
	if err != nil {
		return wardenPlace{}, false, err
	}
	res, err := wardenGeoClient.Do(req)
	if err != nil {
		return wardenPlace{}, false, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return wardenPlace{}, false, fmt.Errorf("geocoding: %s", res.Status)
	}
	var out struct {
		Results []struct {
			Name      string  `json:"name"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return wardenPlace{}, false, err
	}
	if len(out.Results) == 0 {
		return wardenPlace{}, false, nil
	}
	r := out.Results[0]
	return wardenPlace{Name: r.Name, Lat: r.Latitude, Lon: r.Longitude}, true, nil
}

func argNum(a map[string]any, k string) (float64, bool) {
	v, ok := a[k].(float64)
	return v, ok && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// the places kept under d (the space document or a ward), as an answer
func wardenPlacesJSON(d map[string]any) []any {
	out := []any{}
	for _, p := range docMaps(d, "places") {
		out = append(out, map[string]any{"name": docStr(p, "name"), "lat": p["lat"], "lon": p["lon"]})
	}
	return out
}

// where a place is kept: the space document, or the ward the arguments name
func (sp *wardenSpace) placesOwner(a map[string]any) (map[string]any, error) {
	if argStr(a, "ward") == "" {
		return sp.doc, nil
	}
	id := sp.wardID(argStr(a, "ward"))
	for _, w := range docMaps(sp.doc, "wards") {
		if id != "" && docStr(w, "id") == id {
			return w, nil
		}
	}
	return nil, roomErr{"no such ward: give its id or name, or leave ward out for the whole space"}
}

// add_place: any member gives the space or a ward a usual place
func (s *wardenService) addPlace(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	owner, err := sp.placesOwner(a)
	if err != nil {
		return nil, err
	}
	name, err := wardenName(a, "place", 80)
	if err != nil {
		return nil, err
	}
	lat, okLat := argNum(a, "lat")
	lon, okLon := argNum(a, "lon")
	if okLat != okLon || (okLat && (math.Abs(lat) > 90 || math.Abs(lon) > 180)) {
		return nil, roomErr{"give both lat and lon (degrees), or neither"}
	}
	if !okLat {
		p, found, err := wardenGeocode(ctx, name)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, roomErr{"no place in Finland is called " + name}
		}
		name, lat, lon = p.Name, p.Lat, p.Lon
	}
	places := docMaps(owner, "places")
	if slices.ContainsFunc(places, func(p map[string]any) bool { return strings.EqualFold(docStr(p, "name"), name) }) {
		return nil, roomErr{name + " is there already"}
	}
	if len(places) >= wardenMaxPlaces {
		return nil, roomErr{fmt.Sprintf("at most %d places each", wardenMaxPlaces)}
	}
	round := func(v float64) float64 { return math.Round(v*10000) / 10000 }
	owner["places"] = anys(append(places, map[string]any{"name": name, "lat": round(lat), "lon": round(lon)}))
	if err := s.save(ctx, sp); err != nil {
		return nil, err
	}
	return sp.json(), nil
}

// remove_place: any member takes a place off the space or a ward
func (s *wardenService) removePlace(ctx context.Context, sp *wardenSpace, a map[string]any) (any, error) {
	owner, err := sp.placesOwner(a)
	if err != nil {
		return nil, err
	}
	name := argStr(a, "place")
	places := docMaps(owner, "places")
	n := len(places)
	places = slices.DeleteFunc(places, func(p map[string]any) bool { return strings.EqualFold(docStr(p, "name"), name) })
	if len(places) == n {
		return nil, roomErr{"no such place: " + name}
	}
	owner["places"] = anys(places)
	if err := s.save(ctx, sp); err != nil {
		return nil, err
	}
	return sp.json(), nil
}
