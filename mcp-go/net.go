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

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast))
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
