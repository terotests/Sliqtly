// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"crypto/tls"
	"errors"
	"os"
	"sync"
	"time"
)

// A certificate of the server's own name from files (-tls-cert and
// -tls-key, SLIQTLY_TLS_CERT and SLIQTLY_TLS_KEY): a company's, or one
// Let's Encrypt renews. It is served on the same port in place of the
// server's own authority (owncert.go), so browsers and the desktop app
// trust the server with nothing installed. The files are read again when
// they change (a renewal), looked at no more than every 30 seconds; a
// pair that does not load then keeps the one that did, and says so in the
// log once.
type fileCert struct {
	certPath, keyPath string
	mu                sync.Mutex
	cert              *tls.Certificate
	stamp             [2]time.Time // the files' modification times when read
	looked            time.Time
	now               func() time.Time
	logf              func(string, ...any)
}

func loadFileCert(certPath, keyPath string, logf func(string, ...any)) (*fileCert, error) {
	if certPath == "" || keyPath == "" {
		return nil, errors.New("-tls-cert and -tls-key go together")
	}
	f := &fileCert{certPath: certPath, keyPath: keyPath, now: time.Now, logf: logf}
	if err := f.reload(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *fileCert) stamps() ([2]time.Time, error) {
	a, err := os.Stat(f.certPath)
	if err != nil {
		return [2]time.Time{}, err
	}
	b, err := os.Stat(f.keyPath)
	if err != nil {
		return [2]time.Time{}, err
	}
	return [2]time.Time{a.ModTime(), b.ModTime()}, nil
}

// reads the pair; called with mu held or before f is shared
func (f *fileCert) reload() error {
	st, err := f.stamps()
	if err != nil {
		return err
	}
	c, err := tls.LoadX509KeyPair(f.certPath, f.keyPath)
	if err != nil {
		return err
	}
	f.cert, f.stamp = &c, st
	return nil
}

func (f *fileCert) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if now.Sub(f.looked) >= 30*time.Second {
		f.looked = now
		if st, err := f.stamps(); err == nil && st != f.stamp {
			if err := f.reload(); err != nil {
				f.stamp = st // once per change, not on every look
				if f.logf != nil {
					f.logf("tls certificate %s: %v (the one read before is kept)", f.certPath, err)
				}
			}
		}
	}
	return f.cert, nil
}

func (f *fileCert) config() *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"http/1.1"},
		GetCertificate: f.get,
	}
}
