// Fetching pictures and client metadata documents from addresses a model or
// a client names.

package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// ranges that are not the public internet beyond what net.IP's own tests
// name: "this network", carrier-grade NAT (a cloud's internal addresses),
// IETF protocol assignments, benchmarking, the reserved block, and IPv6
// that translates to IPv4 (NAT64, 6to4, Teredo) or is documentation
var notPublic = parseCIDRs("0.0.0.0/8,100.64.0.0/10,192.0.0.0/24,198.18.0.0/15,240.0.0.0/4,64:ff9b::/96,64:ff9b:1::/48,2002::/16,2001::/32,2001:db8::/32")

func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) {
		return false
	}
	// an IPv4 address written as IPv6 (::ffff:10.0.0.1) is judged as IPv4
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range notPublic {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

// The Ranger code refuses private names; this client also refuses to
// connect to an address that is not public, so a public name pointing at
// 10.x or the metadata server is refused too.
func newPublicClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return fmt.Errorf("refusing to connect to %s", host)
			}
			return nil
		},
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = dialer.DialContext
	tr.Proxy = nil
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" {
			return errors.New("redirect to a non-https address")
		}
		return nil
	}}
}

// Node's Buffer.from(s, "base64") is lenient: either alphabet, padding or
// not, white space ignored.
func decodeBase64(s string) []byte {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '-':
			return '+'
		case r == '_':
			return '/'
		case r == ' ' || r == '\n' || r == '\r' || r == '\t':
			return -1
		}
		return r
	}, s)
	if i := strings.IndexByte(s, '='); i >= 0 {
		s = s[:i]
	}
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}
