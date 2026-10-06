// SPDX-License-Identifier: AGPL-3.0-or-later

// The server's own certificate: https:// on a server of one's own, so that
// a browser on another computer lets a page use the microphone (calls,
// recording). Browsers allow it only on https:// or on localhost.
//
// The server makes its own certificate authority once, in the data folder
// (tls/ca.crt and tls/ca.key, the key readable by the server's user only),
// and from it a certificate for whatever name or address a browser asks
// for. Each computer that calls in installs ca.crt once (/ca tells how and
// shows its fingerprint to compare with the one on /settings); from then
// on https:// to this server is trusted there like any site.
//
// The authority can only vouch for this server's own kinds of names: its
// names (localhost, the machine's name, SLIQTLY_URL's, SLIQTLY_HOSTS), the
// local suffixes (.local, .lan, .home.arpa, .internal) and private
// addresses (10/8, 172.16/12, 192.168/16, 100.64/10, link-local, IPv6 ULA,
// loopback). So were its key taken, it still could not stand in for a
// site on the internet. A server on a public address or name needs a real
// certificate (a proxy in front of it) instead.
//
// The same port answers both: a connection that starts with a TLS
// handshake (first byte 22) is https://, anything else http://
// (netaccess.go). Nothing is redirected: http:// keeps working on the
// computer itself and for listeners who need no microphone.
//
//	GET /ca      how to install the certificate, its fingerprint
//	GET /ca.crt  the certificate

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"html/template"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	caYears   = 10
	leafDays  = 397 // what browsers accept for one certificate
	leafRenew = 30 * 24 * time.Hour
)

// the local suffixes the authority may vouch for besides the server's names
var caLocalSuffixes = []string{"localhost", "local", "lan", "home.arpa", "internal"}

// and the private addresses
var caPrivateNets = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10",
	"127.0.0.0/8", "169.254.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
}

type ownCerts struct {
	ca     *x509.Certificate
	caKey  *ecdsa.PrivateKey
	caPEM  []byte
	names  func(string) bool // a name this server answers to
	mu     sync.Mutex
	leaves map[string]*tls.Certificate
	now    func() time.Time
}

// loadOwnCerts: the authority in dir (made when there is none), vouching
// for names (the server's own) besides the local suffixes; ok says whether
// a name is one the server answers to
func loadOwnCerts(dir string, names []string, ok func(string) bool) (*ownCerts, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	crtPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	crt, err1 := os.ReadFile(crtPath)
	key, err2 := os.ReadFile(keyPath)
	if errors.Is(err1, os.ErrNotExist) && errors.Is(err2, os.ErrNotExist) {
		crt, key, err1 = newCA(names, time.Now())
		if err1 != nil {
			return nil, err1
		}
		// the key first: a certificate without its key is no use
		if err := os.WriteFile(keyPath, key, 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(crtPath, crt, 0o644); err != nil {
			return nil, err
		}
	} else if err1 != nil {
		return nil, err1
	} else if err2 != nil {
		return nil, err2
	}
	return parseOwnCerts(crt, key, ok)
}

func parseOwnCerts(crt, key []byte, ok func(string) bool) (*ownCerts, error) {
	cb, _ := pem.Decode(crt)
	kb, _ := pem.Decode(key)
	if cb == nil || kb == nil {
		return nil, errors.New("tls/ca.crt or tls/ca.key is not PEM")
	}
	ca, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, err
	}
	return &ownCerts{ca: ca, caKey: k, caPEM: crt, names: ok, leaves: map[string]*tls.Certificate{}, now: time.Now}, nil
}

// a new authority (certificate and key, PEM) for names and the local ones
func newCA(names []string, now time.Time) (crt, key []byte, err error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "this computer"
	}
	dns := append([]string{}, caLocalSuffixes...)
	for _, n := range names {
		n = strings.ToLower(strings.TrimSuffix(n, "."))
		if n != "" && net.ParseIP(n) == nil && !coveredBy(n, dns) {
			dns = append(dns, n)
		}
	}
	var ips []*net.IPNet
	for _, c := range caPrivateNets {
		_, n, _ := net.ParseCIDR(c)
		ips = append(ips, n)
	}
	t := &x509.Certificate{
		SerialNumber:                serial(),
		Subject:                     pkix.Name{CommonName: "Sliqtly server on " + host, Organization: []string{"Sliqtly (own server)"}},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    now.AddDate(caYears, 0, 0),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         dns,
		PermittedIPRanges:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, t, t, &k.PublicKey, k)
	if err != nil {
		return nil, nil, err
	}
	kd, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), nil
}

// name is one of the domains or under one
func coveredBy(name string, domains []string) bool {
	for _, d := range domains {
		if name == d || strings.HasSuffix(name, "."+d) {
			return true
		}
	}
	return false
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
	return n
}

// the TLS settings: a certificate made for each name asked (and the
// address the connection came to)
func (o *ownCerts) config() *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"http/1.1"},
		GetCertificate: o.certFor,
	}
}

func (o *ownCerts) certFor(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(strings.TrimSuffix(hello.ServerName, "."))
	if name != "" && !o.names(name) {
		return nil, fmt.Errorf("not this server's name: %s", name)
	}
	var ip net.IP
	if hello.Conn != nil {
		if a, ok := hello.Conn.LocalAddr().(*net.TCPAddr); ok {
			ip = a.IP
		}
	}
	return o.leaf(name, ip)
}

// the certificate for name (may be "") and ip, made once and again when
// it is about to run out
func (o *ownCerts) leaf(name string, ip net.IP) (*tls.Certificate, error) {
	key := name + "|" + ip.String()
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	if c := o.leaves[key]; c != nil && now.Add(leafRenew).Before(c.Leaf.NotAfter) {
		return c, nil
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	t := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "Sliqtly server"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafDays * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if name != "" {
		if a := net.ParseIP(name); a != nil {
			t.IPAddresses = append(t.IPAddresses, a)
		} else {
			t.DNSNames = append(t.DNSNames, name)
		}
	}
	if ip != nil && !ip.IsUnspecified() && (len(t.IPAddresses) == 0 || !t.IPAddresses[0].Equal(ip)) {
		t.IPAddresses = append(t.IPAddresses, ip)
	}
	if len(t.DNSNames) == 0 && len(t.IPAddresses) == 0 {
		t.DNSNames = []string{"localhost"}
	}
	der, err := x509.CreateCertificate(rand.Reader, t, o.ca, &k.PublicKey, o.caKey)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der, o.ca.Raw}, PrivateKey: k, Leaf: leaf}
	if len(o.leaves) > 256 {
		o.leaves = map[string]*tls.Certificate{}
	}
	o.leaves[key] = c
	return c, nil
}

// the authority's SHA-256 fingerprint, as browsers and the systems show it
func (o *ownCerts) fingerprint() string {
	sum := sha256.Sum256(o.ca.Raw)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, len(h)/2)
	for i := 0; i < len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, ":")
}

// what the authority may vouch for, for the page
func (o *ownCerts) covers() []string {
	out := append([]string{}, o.ca.PermittedDNSDomains...)
	sort.Strings(out)
	return out
}

// --- the pages

func (s *localServer) caFile(w http.ResponseWriter) {
	if s.certs == nil {
		http.Error(w, "this server has no certificate of its own", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="sliqtly-server-ca.crt"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(s.certs.caPEM)
}

func (s *localServer) caPage(w http.ResponseWriter, r *http.Request) {
	if s.certs == nil {
		http.Error(w, "this server has no certificate of its own", http.StatusNotFound)
		return
	}
	host := r.Host
	https := "https://" + host
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	caTmpl.Execute(w, map[string]any{
		"Fingerprint": s.certs.fingerprint(),
		"HTTPS":       https,
		"Secure":      r.TLS != nil,
		"Names":       strings.Join(s.certs.covers(), ", "),
	})
}

var caTmpl = template.Must(template.New("ca").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sliqtly server certificate</title>
<style>
:root { color-scheme: light dark; --bg: #f6f6f4; --fg: #1d1d1b; --muted: #6b6b66; --line: #e2e2dc; --ok: #1d7a3a; }
@media (prefers-color-scheme: dark) { :root { --bg: #141414; --fg: #ececea; --muted: #9a9a94; --line: #333; --ok: #6fcf8a; } }
body { margin: 0; background: var(--bg); color: var(--fg); font: 16px/1.5 system-ui, sans-serif; }
main { max-width: 720px; margin: 0 auto; padding: 24px 16px 64px; }
h1 { font-size: 1.5rem; margin: 0 0 4px; } h2 { font-size: 1.1rem; margin: 22px 0 4px; }
.muted { color: var(--muted); font-size: .9rem; } .ok { color: var(--ok); }
code { font: 13px/1.4 ui-monospace, monospace; word-break: break-all; }
a.btn { display: inline-block; padding: 8px 16px; border-radius: 6px; background: var(--fg); color: var(--bg); text-decoration: none; margin: 8px 0; }
li { margin: 4px 0; }
</style></head><body><main>
<p class="muted"><a href="/">Editor</a> · <a href="/decks">Presentations</a></p>
<h1>Use the microphone from this computer</h1>
{{if .Secure}}<p class="ok">This page is open over https:// and trusted: calls and recording can use the microphone here.</p>
{{else}}<p>Browsers let a page use the microphone only over https:// (or on the server's own computer). This server has a certificate of its own; install it once on this computer, then open <a href="{{.HTTPS}}/">{{.HTTPS}}</a>.</p>{{end}}
<a class="btn" href="/ca.crt" download>Download the certificate</a>
<p class="muted">Before installing, compare its fingerprint with the one on the server's Settings page (open it on the server's computer):<br><code>SHA-256 {{.Fingerprint}}</code></p>
<p class="muted">It can vouch only for this server's own kinds of addresses: {{.Names}}, and private network addresses. It cannot stand in for a site on the internet.</p>
<h2>macOS</h2>
<ol><li>Open the downloaded file: Keychain Access adds it to the login keychain.</li>
<li>Double-click "Sliqtly server on …", open Trust, set "When using this certificate" to Always Trust, close and give your password.</li>
<li>Safari and Chrome use it at once; Firefox: Settings → Privacy &amp; Security → Certificates → View Certificates → Authorities → Import.</li></ol>
<h2>Windows</h2>
<ol><li>Open the downloaded file, choose Install Certificate…, Current User.</li>
<li>Choose "Place all certificates in the following store", Browse, Trusted Root Certification Authorities, Finish, and Yes to the warning.</li>
<li>Restart the browser. Firefox: import it as on macOS.</li></ol>
<h2>Linux</h2>
<ol><li>Chrome: Settings → Privacy and security → Security → Manage certificates → Authorities → Import, tick "Trust this certificate for identifying websites".</li>
<li>Firefox: Settings → Privacy &amp; Security → Certificates → View Certificates → Authorities → Import, tick the same.</li></ol>
<h2>iPhone and iPad</h2>
<ol><li>Open this page in Safari and download the certificate, Allow.</li>
<li>Settings → Profile Downloaded → Install.</li>
<li>Settings → General → About → Certificate Trust Settings: turn the Sliqtly server on.</li></ol>
<h2>Android</h2>
<ol><li>Download the certificate, then Settings → Security → Encryption &amp; credentials → Install a certificate → CA certificate.</li></ol>
</main></body></html>
`))

// the server's names (hostGuard) as the authority's names
func (g *hostGuard) list() []string {
	out := make([]string, 0, len(g.names))
	for n := range g.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
