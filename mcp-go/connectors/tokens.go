// SPDX-License-Identifier: AGPL-3.0-or-later

package connectors

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Token is a person's sign-in to a service.
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// ms; 0 when the service gave no expiry
	Expires int64 `json:"expires,omitempty"`
	// the account at the service, when it could be read (e.g. a GitHub login)
	Account string `json:"account,omitempty"`
	At      int64  `json:"at"`
}

// Tokens keeps tokens under dir/tokens/<connector>/<sha256(person)>,
// encrypted (AES-256-GCM) with the key in dir/key, which is made on first
// use and readable only by the server's user. A copy of the folder without
// the key holds no usable token.
type Tokens struct {
	dir string
	mu  sync.Mutex
	gcm cipher.AEAD
}

// OpenTokens opens (and when needed makes) the key.
func OpenTokens(dir string) (*Tokens, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, "key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(key); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("connectors/key is not a 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Tokens{dir: dir, gcm: gcm}, nil
}

func (t *Tokens) path(connector, who string) string {
	h := sha256.Sum256([]byte(who))
	return filepath.Join(t.dir, "tokens", connector, hex.EncodeToString(h[:]))
}

// the person and connector are sealed in with the token, so a file moved
// to another person's place does not open
func aad(connector, who string) []byte { return []byte(connector + "\x00" + who) }

// Put keeps a person's token for a connector.
func (t *Tokens) Put(connector, who string, tok *Token) error {
	plain, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	nonce := make([]byte, t.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := t.gcm.Seal(nonce, nonce, plain, aad(connector, who))
	p := t.path(connector, who)
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(p+".new", sealed, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".new", p)
}

// Get is the person's token, or nil when they have not signed in.
func (t *Tokens) Get(connector, who string) (*Token, error) {
	t.mu.Lock()
	b, err := os.ReadFile(t.path(connector, who))
	t.mu.Unlock()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n := t.gcm.NonceSize()
	if len(b) < n {
		return nil, errors.New("token file is damaged")
	}
	plain, err := t.gcm.Open(nil, b[:n], b[n:], aad(connector, who))
	if err != nil {
		return nil, errors.New("token file does not open with this server's key")
	}
	var tok Token
	if err := json.Unmarshal(plain, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// Delete forgets the person's token.
func (t *Tokens) Delete(connector, who string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	err := os.Remove(t.path(connector, who))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
