// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testCerts(t *testing.T, dir string) *ownCerts {
	t.Helper()
	g := newHostGuard("http://localhost:8080", false)
	g.names["deckbox"] = true
	o, err := loadOwnCerts(dir, g.list(), g.hostOK)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// The authority is made once and kept: the key for the server's user only,
// the same fingerprint after a restart.
func TestOwnCertsKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	a := testCerts(t, dir)
	st, err := os.Stat(filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, st.Mode().Perm(), os.FileMode(0o600))
	b := testCerts(t, dir)
	eq(t, a.fingerprint(), b.fingerprint())
	eq(t, len(strings.Split(a.fingerprint(), ":")), 32)
}

// Its certificates are trusted for the server's names and private
// addresses, and the authority cannot vouch for a site on the internet even
// when it signs one.
func TestOwnCertsNames(t *testing.T) {
	o := testCerts(t, t.TempDir())
	pool := x509.NewCertPool()
	pool.AddCert(o.ca)
	verify := func(name string, ip net.IP, as string) error {
		c, err := o.leaf(name, ip)
		if err != nil {
			return err
		}
		_, err = c.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: as})
		return err
	}
	for _, ok := range []struct {
		name string
		ip   string
		as   string
	}{
		{"", "192.168.1.20", "192.168.1.20"},
		{"10.0.0.7", "10.0.0.7", "10.0.0.7"},
		{"deckbox", "192.168.1.20", "deckbox"},
		{"deckbox.local", "192.168.1.20", "deckbox.local"},
		{"localhost", "127.0.0.1", "localhost"},
		{"", "fd00::5", "fd00::5"},
	} {
		if err := verify(ok.name, net.ParseIP(ok.ip), ok.as); err != nil {
			t.Errorf("%s %s: %v", ok.name, ok.ip, err)
		}
	}
	for _, bad := range []struct{ name, ip, as string }{
		{"www.example.com", "", "www.example.com"},
		{"", "8.8.8.8", "8.8.8.8"},
	} {
		if err := verify(bad.name, net.ParseIP(bad.ip), bad.as); err == nil {
			t.Errorf("%s %s: trusted, should not be", bad.name, bad.ip)
		}
	}
	// a name the server does not answer to gets no certificate
	if _, err := o.certFor(&tls.ClientHelloInfo{ServerName: "www.example.com"}); err == nil {
		t.Error("a certificate for www.example.com")
	}
	// one certificate per name, made again when it is about to run out
	c1, _ := o.leaf("deckbox", nil)
	c2, _ := o.leaf("deckbox", nil)
	if c1 != c2 {
		t.Error("made twice")
	}
	o.now = func() time.Time { return time.Now().Add((leafDays - 10) * 24 * time.Hour) }
	c3, _ := o.leaf("deckbox", nil)
	if c3 == c1 {
		t.Error("not renewed near its end")
	}
}

// The HTTP port answers https:// and http:// both, and /ca hands out the
// certificate.
func TestHTTPSOnHTTPPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	base := "http://127.0.0.1:" + port
	dir := t.TempDir()
	e, bucket, err := localEnv(dir, base, "local")
	if err != nil {
		t.Fatal(err)
	}
	e.Client = fakeNet
	ls := newLocalServer(e, bucket, "", nil).(*localServer)
	ls.certs = testCerts(t, filepath.Join(dir, "tls"))
	hs := &http.Server{Handler: ls}
	p, _ := newNetPolicy(accessLocal, nil, true)
	x := newExposure(hs, port, p)
	x.tls.Store(ls.certs.config())
	ls.expo = x
	if err := x.sync(true); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		hs.Shutdown(ctx)
	}()

	pool := x509.NewCertPool()
	pool.AddCert(ls.certs.ca)
	cl := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, Timeout: 5 * time.Second}
	get := func(u string) (int, string) {
		t.Helper()
		r, err := cl.Get(u)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	code, body := get("https://127.0.0.1:" + port + "/healthz")
	eq(t, code, 200)
	eq(t, body, "ok\n")
	code, body = get("https://localhost:" + port + "/ca")
	eq(t, code, 200)
	if !strings.Contains(body, ls.certs.fingerprint()) || !strings.Contains(body, "trusted") {
		t.Errorf("/ca over https: %.300s", body)
	}
	code, body = get(base + "/ca")
	eq(t, code, 200)
	if !strings.Contains(body, "https://127.0.0.1:"+port+"/") {
		t.Errorf("/ca over http does not point to https: %.300s", body)
	}
	code, body = get(base + "/ca.crt")
	eq(t, code, 200)
	eq(t, body, string(ls.certs.caPEM))
	code, body = get(base + "/api/settings/network")
	eq(t, code, 200)
	if !strings.Contains(body, ls.certs.fingerprint()) {
		t.Errorf("settings without the fingerprint: %.300s", body)
	}
}
