// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"testing"
)

// sliqtly.com: the assistant lists and makes the account's vaults and puts
// presentations in them, as the editor's Vault ▸ menu does (web/vaults.js)
func TestVaultTools(t *testing.T) {
	f := fakeFirebase()
	env := withSignIn(testEnv(&f, nil))
	env.layouts = newLayoutService(env.DB)
	s := start(t, env, signIn(f))
	defer s.close()
	ctx := context.Background()
	md := "# Deck\n\n## One\n\nText"
	a := sc(call(t, s, "create_presentation", map[string]any{"title": "Asiakasdemo", "markdown": md}))["deck_id"].(string)
	b := sc(call(t, s, "create_presentation", map[string]any{"title": "Testi", "markdown": md}))["deck_id"].(string)

	// the Demo Vault is there before anything is changed, with the sample
	vs := sc(call(t, s, "list_vaults", map[string]any{}))["vaults"].([]any)
	eq(t, len(vs), 1)
	demo := vs[0].(map[string]any)
	eq(t, []any{demo["vault_id"], demo["title"]}, []any{"demo", "Demo Vault"})
	eq(t, demo["presentations"].([]any)[0].(map[string]any)["deck_id"], "sample:welcome")

	// a vault made, and the same title again is that vault
	made := sc(call(t, s, "create_vault", map[string]any{"title": "  Customer   demo "}))
	eq(t, []any{made["title"], made["made"]}, []any{"Customer demo", true})
	cust := made["vault_id"].(string)
	again := sc(call(t, s, "create_vault", map[string]any{"title": "customer DEMO"}))
	eq(t, []any{again["vault_id"], again["made"]}, []any{cust, false})

	// put in two vaults (a copy of its place), then moved to one
	call(t, s, "add_to_vault", map[string]any{"deck_id": a, "vault_id": "demo"})
	in := sc(call(t, s, "add_to_vault", map[string]any{"deck_id": a, "vault_id": cust}))["vaults"].([]any)
	eq(t, len(in), 2, "in both")
	in = sc(call(t, s, "add_to_vault", map[string]any{"deck_id": a, "vault_id": cust, "move": true}))["vaults"].([]any)
	eq(t, len(in), 1, "moved")
	eq(t, in[0].(map[string]any)["vault_id"], cust)
	call(t, s, "add_to_vault", map[string]any{"deck_id": b, "vault_id": "demo"})

	vs = sc(call(t, s, "list_vaults", map[string]any{}))["vaults"].([]any)
	eq(t, vs[0].(map[string]any)["vault_id"], "demo", "built-in first")
	names := func(v any) []any {
		out := []any{}
		for _, p := range v.(map[string]any)["presentations"].([]any) {
			out = append(out, p.(map[string]any)["deck_id"])
		}
		return out
	}
	eq(t, names(vs[0]), []any{"sample:welcome", b})
	eq(t, names(vs[1]), []any{a})

	// out of a vault; the sample too; mistakes said
	in = sc(call(t, s, "remove_from_vault", map[string]any{"deck_id": "sample:welcome", "vault_id": "demo"}))["vaults"].([]any)
	eq(t, len(in), 0)
	eq(t, call(t, s, "add_to_vault", map[string]any{"deck_id": a, "vault_id": "nope"}).IsError, true, "no such vault")
	eq(t, call(t, s, "add_to_vault", map[string]any{"deck_id": "someones12", "vault_id": "demo"}).IsError, true, "not the account's deck")
	eq(t, call(t, s, "create_vault", map[string]any{"title": "  "}).IsError, true, "no title")

	// the editor gets them as its facts (web/rooms.js "vault/<id>")
	out, err := env.layouts.sync(ctx, "u1", 0, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, x := range out["facts"].([]map[string]any) {
		got[x["k"].(string)] = x["v"]
	}
	var d vaultDoc
	if err := json.Unmarshal([]byte(got["vault/demo"].(string)), &d); err != nil {
		t.Fatal(err)
	}
	eq(t, d.Title, "Demo Vault")
	eq(t, d.Decks, []string{"cloud:" + b})
	var c vaultDoc
	_ = json.Unmarshal([]byte(got["vault/"+cust].(string)), &c)
	eq(t, c.Decks, []string{"cloud:" + a})
}
