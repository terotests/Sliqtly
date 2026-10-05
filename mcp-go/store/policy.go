// SPDX-License-Identifier: AGPL-3.0-or-later

package store

// OwnerPolicy is access by ownership inside a tenant, the model the
// tests and single-owner documents use (rooms use RoomPolicy):
//
//	tenant    the document's tenant: nobody outside it reads or writes it
//	owner     the user who owns it: reads and writes it
//	readers   user ids, or "group:<id>", who may read it
//	writers   the same, who may also change it (not its owner, tenant,
//	          readers or writers)
//
// A principal with the role "admin" reads and writes everything in their
// own tenant, and no other. A document is made only by its owner, in the
// principal's tenant.
type OwnerPolicy struct{}

func (OwnerPolicy) Scope(p Principal, _ string) Expr {
	if p.UserID == "" || p.TenantID == "" {
		return False
	}
	tenant := Eq("tenant", p.TenantID)
	if p.HasRole("admin") {
		return tenant
	}
	who := Or{Eq("owner", p.UserID), Has("readers", p.UserID), Has("writers", p.UserID)}
	for _, g := range p.Groups {
		who = append(who, Has("readers", "group:"+g), Has("writers", "group:"+g))
	}
	return And{tenant, who}
}

func (pol OwnerPolicy) CanRead(p Principal, col, id string, d Doc) bool {
	return d != nil && Match(pol.Scope(p, col), id, d)
}

func (pol OwnerPolicy) CanWrite(p Principal, col, id string, old, next Doc) bool {
	if p.UserID == "" || p.TenantID == "" {
		return false
	}
	if old == nil {
		return next["tenant"] == p.TenantID && (next["owner"] == p.UserID || p.HasRole("admin"))
	}
	if old["tenant"] != p.TenantID || (next != nil && next["tenant"] != p.TenantID) {
		return false
	}
	if p.HasRole("admin") || old["owner"] == p.UserID {
		return true
	}
	if next == nil || !listed(p, old["writers"]) {
		return false
	}
	// a writer changes the content, not who has it
	return old["owner"] == next["owner"] && sameList(old["readers"], next["readers"]) && sameList(old["writers"], next["writers"])
}

func listed(p Principal, v any) bool {
	for _, x := range asList(v) {
		if x == p.UserID {
			return true
		}
		for _, g := range p.Groups {
			if x == "group:"+g {
				return true
			}
		}
	}
	return false
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func sameList(a, b any) bool {
	x, y := asList(a), asList(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
