// Package secret seals the passwords and OAuth tokens of database connections with
// AES-256-GCM under DATA_DIR/secret.key (32 random bytes, made on first
// start). Losing the key means signing in to the accounts again; it is never
// returned by the API and never stored in the database.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const prefix = "v1:"

// Box seals and opens secrets with one key.
type Box struct{ key []byte }

// Load reads DATA_DIR/secret.key, making it on first start. created reports a new key.
func Load(dir string) (b *Box, created bool, err error) {
	if dir == "" {
		dir = "."
	}
	p := filepath.Join(dir, "secret.key")
	key, err := os.ReadFile(p)
	switch {
	case err == nil:
		if len(key) != 32 {
			return nil, false, fmt.Errorf("%s must hold exactly 32 bytes (it has %d); move it away to make a new one (connection passwords must be entered again then)", p, len(key))
		}
	case errors.Is(err, os.ErrNotExist):
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, false, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, false, err
		}
		if err := os.WriteFile(p, key, 0o600); err != nil {
			return nil, false, fmt.Errorf("write %s: %w", p, err)
		}
		created = true
	default:
		return nil, false, err
	}
	return &Box{key: key}, created, nil
}

// New makes a box from a key (tests).
func New(key []byte) *Box { return &Box{key: key} }

func (b *Box) gcm() (cipher.AEAD, error) {
	if b == nil || len(b.key) != 32 {
		return nil, errors.New("the secret key is not loaded")
	}
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts a secret for the database ("" stays "").
func (b *Box) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	g, err := b.gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return prefix + base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Open decrypts what Seal made ("" stays "").
func (b *Box) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if !strings.HasPrefix(sealed, prefix) {
		return "", errors.New("the stored secret is not sealed")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, prefix))
	if err != nil {
		return "", err
	}
	g, err := b.gcm()
	if err != nil {
		return "", err
	}
	if len(raw) < g.NonceSize() {
		return "", errors.New("the stored secret is damaged")
	}
	plain, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], nil)
	if err != nil {
		return "", errors.New("the stored secret cannot be opened (was DATA_DIR/secret.key replaced?) — enter the connection password again")
	}
	return string(plain), nil
}

// Sign makes an HMAC of a value (OAuth state).
func (b *Box) Sign(v string) string {
	if b == nil {
		return ""
	}
	m := hmac.New(sha256.New, b.key)
	m.Write([]byte("pocket-database:" + v))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
