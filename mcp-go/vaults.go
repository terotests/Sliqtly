// SPDX-License-Identifier: AGPL-3.0-or-later

// Vaults on sliqtly.com: the presentations an account chose for one use,
// such as the Demo Vault's for a demo (web/vaults.js). While the editor has
// a vault on it lists only the vault's presentations. A vault is the fact
// "vault/<id>" of the account's layout (layout.go), JSON {title, decks}, its
// decks the page's ids ("cloud:<deck_id>", "sample:<key>"). A presentation
// keeps its one home room and can be in many vaults. The Demo Vault is
// there before it is ever changed, holding the welcome sample.

package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/terotests/sliqtly/mcp-go/store"
)

const (
	demoVault     = "demo"
	maxVaultDecks = 300
	maxVaults     = 100
)

// a vault as the page keeps it
type vaultDoc struct {
	Title string   `json:"title"`
	Decks []string `json:"decks"`
}

// the built-in vaults, as they are until changed (web/vaults.js BUILT_IN_VAULTS)
var builtInVaults = map[string]vaultDoc{demoVault: {Title: "Demo Vault", Decks: []string{"sample:welcome"}}}

func vaultTitle(s string) string {
	t := strings.Join(strings.Fields(s), " ")
	if r := []rune(t); len(r) > 100 {
		t = string(r[:100])
	}
	return t
}

// a vault made sound, as web/vaults.js vaultOf: a title, unique decks
func vaultOf(v vaultDoc) vaultDoc {
	seen := map[string]bool{}
	decks := []string{}
	for _, d := range v.Decks {
		if d != "" && !seen[d] && len(decks) < maxVaultDecks {
			seen[d] = true
			decks = append(decks, d)
		}
	}
	return vaultDoc{Title: vaultTitle(v.Title), Decks: decks}
}

// the account's vaults: the built-in ones as they are now, and the made ones
func vaultsOf(l layoutDoc) map[string]vaultDoc {
	out := map[string]vaultDoc{}
	for id, v := range builtInVaults {
		out[id] = v
	}
	for k, f := range l.facts {
		id, ok := strings.CutPrefix(k, "vault/")
		if !ok || f.V == nil {
			continue
		}
		var v vaultDoc
		if json.Unmarshal([]byte(*f.V), &v) != nil {
			continue
		}
		v = vaultOf(v)
		if v.Title == "" {
			v.Title = builtInVaults[id].Title
		}
		if v.Title == "" {
			v.Title = id
		}
		out[id] = v
	}
	return out
}

func putVault(l *layoutDoc, id string, v vaultDoc, now int64) {
	b, _ := json.Marshal(vaultOf(v))
	l.set("vault/"+id, string(b), now)
}

var vaultIdea = "A vault is a set of presentations chosen for one use, such as the Demo Vault's for showing Sliqtly: while the editor has a vault on (File → Vault), it lists only that vault's presentations. A presentation keeps its one home room (topic) and can be in many vaults. "

var vaultTools = []roomTool{
	{name: "list_vaults", title: "List vaults", readOnly: true,
		desc: vaultIdea + "→ each vault's vault_id, title and presentations (deck_id and name; a sample is sample:<key>). The Demo Vault (vault_id demo) is always there."},
	{name: "create_vault", title: "Create a vault",
		desc:     vaultIdea + "Make a new, empty vault; then put presentations in it with add_to_vault. A title another vault has already (any case) is that vault. → vault_id, made false when it was there",
		props:    map[string]any{"title": strProp("The vault's name, as its use is known, e.g. \"Customer demo\"")},
		required: []string{"title"}},
	{name: "add_to_vault", title: "Put a presentation in a vault",
		desc:     vaultIdea + "Put one of your presentations in a vault: by default it stays in the vaults it is in already (a copy of its place, not of the presentation); move true takes it out of every other vault. → the vaults it is in now",
		props:    map[string]any{"deck_id": strProp("The presentation's deck_id"), "vault_id": strProp("The vault, from list_vaults or create_vault (demo for the Demo Vault)"), "move": map[string]any{"type": "boolean", "description": "true: out of every other vault (default false: kept in them too)"}},
		required: []string{"deck_id", "vault_id"}},
	{name: "remove_from_vault", title: "Take a presentation out of a vault",
		desc:     "Take a presentation out of a vault (the presentation itself stays as it is). → the vaults it is in now",
		props:    map[string]any{"deck_id": strProp("The presentation's deck_id, or sample:<key> for a sample"), "vault_id": strProp("The vault")},
		required: []string{"deck_id", "vault_id"}},
}

func vaultToolOf(name string) (roomTool, bool) {
	for _, t := range vaultTools {
		if t.name == name {
			return t, true
		}
	}
	return roomTool{}, false
}

// the vault ids, built-in first, then by title
func vaultOrder(vs map[string]vaultDoc) []string {
	ids := make([]string, 0, len(vs))
	for id := range vs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		_, bi := builtInVaults[ids[i]]
		_, bj := builtInVaults[ids[j]]
		if bi != bj {
			return bi
		}
		if vs[ids[i]].Title != vs[ids[j]].Title {
			return vs[ids[i]].Title < vs[ids[j]].Title
		}
		return ids[i] < ids[j]
	})
	return ids
}

// the vaults the page's deck pk is in
func vaultsHolding(vs map[string]vaultDoc, pk string) []any {
	out := []any{}
	for _, id := range vaultOrder(vs) {
		for _, d := range vs[id].Decks {
			if d == pk {
				out = append(out, map[string]any{"vault_id": id, "title": vs[id].Title})
				break
			}
		}
	}
	return out
}

// runVault: the vault tools for uid
func (s *layoutService) runVault(ctx context.Context, uid, op string, a map[string]any) (any, error) {
	switch op {
	case "list_vaults":
		l, err := s.ls.read(ctx, uid)
		if err != nil {
			return nil, err
		}
		ds, err := s.decks(ctx, uid, true)
		if err != nil {
			return nil, err
		}
		names := map[string]string{}
		for _, d := range ds {
			names[pageDeck(d.id)] = d.name
		}
		vs := vaultsOf(l)
		out := []any{}
		for _, id := range vaultOrder(vs) {
			decks := []any{}
			for _, pk := range vs[id].Decks {
				if deck, ok := strings.CutPrefix(pk, "cloud:"); ok {
					// one the assistant may not see, or one gone, is not listed
					if name, seen := names[pk]; seen {
						decks = append(decks, map[string]any{"deck_id": deck, "name": name})
					}
				} else if strings.HasPrefix(pk, "sample:") {
					decks = append(decks, map[string]any{"deck_id": pk, "name": "Sample " + strings.TrimPrefix(pk, "sample:")})
				}
			}
			out = append(out, map[string]any{"vault_id": id, "title": vs[id].Title, "presentations": decks})
		}
		return map[string]any{"vaults": out}, nil

	case "create_vault":
		title := vaultTitle(argStr(a, "title"))
		if title == "" {
			return nil, roomErr{"title is empty"}
		}
		var out map[string]any
		_, err := s.ls.change(ctx, uid, func(l *layoutDoc, now int64) error {
			vs := vaultsOf(*l)
			for id, v := range vs {
				if strings.EqualFold(v.Title, title) {
					out = map[string]any{"vault_id": id, "title": v.Title, "made": false}
					return nil
				}
			}
			if len(vs) >= maxVaults {
				return roomErr{"an account keeps at most 100 vaults"}
			}
			id := "v-" + newShareID()
			putVault(l, id, vaultDoc{Title: title, Decks: []string{}}, now)
			out = map[string]any{"vault_id": id, "title": title, "made": true}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return out, nil

	case "add_to_vault", "remove_from_vault":
		deck, vault := argStr(a, "deck_id"), argStr(a, "vault_id")
		pk := deck
		if !strings.HasPrefix(deck, "sample:") {
			mine, err := s.own(ctx, uid, deck)
			if err != nil {
				return nil, err
			}
			if !mine {
				return nil, store.ErrNotFound
			}
			pk = pageDeck(deck)
		} else if op == "add_to_vault" {
			return nil, roomErr{"a sample is put in a vault in the editor (its Vault ▸ menu)"}
		}
		move, _ := a["move"].(bool)
		var held []any
		_, err := s.ls.change(ctx, uid, func(l *layoutDoc, now int64) error {
			vs := vaultsOf(*l)
			v, ok := vs[vault]
			if !ok {
				return roomErr{"no vault " + vault + ": list_vaults lists them, create_vault makes one"}
			}
			has := false
			keep := []string{}
			for _, d := range v.Decks {
				if d == pk {
					has = true
					if op == "remove_from_vault" {
						continue
					}
				}
				keep = append(keep, d)
			}
			if op == "add_to_vault" && !has {
				if len(keep) >= maxVaultDecks {
					return roomErr{"a vault holds at most 300 presentations"}
				}
				keep = append(keep, pk)
			}
			if op == "add_to_vault" && move {
				for id, other := range vs {
					if id == vault {
						continue
					}
					rest := []string{}
					for _, d := range other.Decks {
						if d != pk {
							rest = append(rest, d)
						}
					}
					if len(rest) != len(other.Decks) {
						other.Decks = rest
						putVault(l, id, other, now)
						vs[id] = other
					}
				}
			}
			if len(keep) != len(v.Decks) {
				v.Decks = keep
				putVault(l, vault, v, now)
				vs[vault] = v
			}
			held = vaultsHolding(vs, pk)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"deck_id": deck, "vaults": held}, nil
	}
	return nil, roomErr{"no such operation: " + op}
}
