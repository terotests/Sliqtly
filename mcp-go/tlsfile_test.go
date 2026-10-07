// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// a self-signed certificate for 127.0.0.1, written as PEM to dir
func writeTestCert(t *testing.T, dir, cn string) (string, string, *x509.Certificate) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(k)
	cp, kp := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0o600)
	c, _ := x509.ParseCertificate(der)
	return cp, kp, c
}

// a local server listening as main.go starts it (http:// and https:// on
// one port), configured by setup; → its port
func startExposed(t *testing.T, token string, setup func(ls *localServer)) (string, *localServer) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	e, bucket, err := localEnv(t.TempDir(), "http://127.0.0.1:"+port, "local")
	if err != nil {
		t.Fatal(err)
	}
	e.Client = fakeNet
	e.TrustHost = true
	ls := newLocalServer(e, bucket, token, nil).(*localServer)
	setup(ls)
	hs := &http.Server{Handler: ls}
	p, _ := newNetPolicy(accessLocal, nil, true)
	x := newExposure(hs, port, p)
	if ls.fileCert != nil {
		x.tls.Store(ls.fileCert.config())
	} else if ls.certs != nil {
		x.tls.Store(ls.certs.config())
	}
	ls.expo = x
	if err := x.sync(true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		hs.Shutdown(ctx)
	})
	return port, ls
}

func infoOver(t *testing.T, c *http.Client, u string) map[string]any {
	t.Helper()
	res, err := c.Get(u + "/api/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s: %s", u, b)
	}
	return out["tls"].(map[string]any)
}

// -tls-cert and -tls-key: that certificate on the https:// side of the
// port, /api/v1 and /mcp over it, and a renewed one served without a
// restart
func TestTLSFromFiles(t *testing.T) {
	dir := t.TempDir()
	cp, kp, cert := writeTestCert(t, dir, "first")
	fc, err := loadFileCert(cp, kp, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	fc.now = func() time.Time { return clock }
	port, _ := startExposed(t, "tok", func(ls *localServer) { ls.fileCert = fc })

	pool := x509.NewCertPool()
	pool.AddCert(cert)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	cl := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	tlsInfo := infoOver(t, cl, "https://127.0.0.1:"+port)
	eq(t, tlsInfo["enabled"], true)
	eq(t, tlsInfo["ownCA"], false)
	if _, ok := tlsInfo["fingerprint"]; ok {
		t.Error("a fingerprint for a certificate that is not the server's own authority")
	}
	// http:// on the same port still answers
	infoOver(t, &http.Client{Timeout: 5 * time.Second}, "http://127.0.0.1:"+port)

	req, _ := http.NewRequest("GET", "https://127.0.0.1:"+port+"/api/v1/decks", nil)
	req.Header.Set("Authorization", "Bearer tok")
	res, err := cl.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	eq(t, res.StatusCode, 200)

	hc := &http.Client{Transport: withToken{"tok", tr}}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: "https://127.0.0.1:" + port + "/mcp", HTTPClient: hc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ListTools(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	session.Close()

	// renewed on disk: served once the files are looked at again
	time.Sleep(10 * time.Millisecond) // a modification time of its own
	_, _, renewed := writeTestCert(t, dir, "second")
	fc.mu.Lock() // the server reads the clock in its handshakes
	clock = clock.Add(time.Minute)
	fc.mu.Unlock()
	pool2 := x509.NewCertPool()
	pool2.AddCert(renewed)
	cl2 := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool2}}, Timeout: 5 * time.Second}
	infoOver(t, cl2, "https://127.0.0.1:"+port)

	if _, err := loadFileCert(cp, "", nil); err == nil {
		t.Error("a certificate without its key taken")
	}
}

// the server's own authority: /api/v1/info gives its fingerprint to pin
func TestTLSOwnCAInfo(t *testing.T) {
	var certs *ownCerts
	port, _ := startExposed(t, "", func(ls *localServer) {
		certs = testCerts(t, filepath.Join(t.TempDir(), "tls"))
		ls.certs = certs
	})
	pool := x509.NewCertPool()
	pool.AddCert(certs.ca)
	cl := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, Timeout: 5 * time.Second}
	tlsInfo := infoOver(t, cl, "https://127.0.0.1:"+port)
	eq(t, tlsInfo["ownCA"], true)
	eq(t, tlsInfo["ca"], "/ca.crt")
	eq(t, tlsInfo["fingerprint"], sha256Hex(string(certs.ca.Raw)))
}
