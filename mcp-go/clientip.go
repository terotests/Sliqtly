// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

// The caller's address, which the rate limits and the daily quota key.
//
// Every proxy in front of the server adds the address it was reached from
// to the end of X-Forwarded-For, and a caller can send the header with
// anything at its start. So the caller is read from the right: past the
// hops that are proxies of ours (Google's front ends for Cloud Run and
// Hosting, private and link-local addresses), the first address is the
// one that connected to them. The first value, which the client writes
// itself, is never trusted.
//
// SLIQTLY_TRUSTED_PROXIES adds address ranges (CIDR, comma separated) of
// further proxies, e.g. a reverse proxy in front of a server of one's own.

var proxyNets = parseCIDRs(strings.Join([]string{
	// Google's front ends, as Cloud Run and load balancers see them
	"35.191.0.0/16", "130.211.0.0/22",
	// this machine, private and link-local networks, carrier-grade NAT
	"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "fe80::/10", "fc00::/7", "100.64.0.0/10",
	os.Getenv("SLIQTLY_TRUSTED_PROXIES"),
}, ","))

func parseCIDRs(list string) []*net.IPNet {
	var out []*net.IPNet
	for _, s := range strings.Split(list, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
				s += "/32"
			} else {
				s += "/128"
			}
		}
		if _, n, err := net.ParseCIDR(s); err == nil {
			out = append(out, n)
		} else {
			log.Printf("SLIQTLY_TRUSTED_PROXIES: %q is not an address range", s)
		}
	}
	return out
}

func isProxy(ip net.IP) bool {
	for _, n := range proxyNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

var xffNoted sync.Once

// the address that reached our proxies: X-Forwarded-For read from the
// right, then the connection itself
func clientIP(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	hops := []string{}
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, v := range strings.Split(h, ",") {
			if v = strings.TrimSpace(v); v != "" {
				hops = append(hops, v)
			}
		}
	}
	hops = append(hops, peer)
	// the nearest address that is not one of our proxies
	pick := ""
	from := 0
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(hops[i])
		if ip == nil {
			// not an address: what follows it to the left is the
			// client's own text
			break
		}
		pick, from = ip.String(), len(hops)-1-i
		if !isProxy(ip) {
			break
		}
	}
	if pick == "" {
		pick = peer
	}
	// once per instance, with no address in it: how deep the caller sat,
	// to see the proxies in front are the ones expected
	if len(hops) > 1 {
		xffNoted.Do(func() { log.Printf("client address: %d of %d hops from the right", from, len(hops)) })
	}
	return pick
}
