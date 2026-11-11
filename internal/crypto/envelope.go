// Package crypto implements envelope encryption: each tenant has its own data
// key (AES-256-GCM), stored wrapped by the platform master key. In production
// the master key lives in Cloud KMS; here it comes from APEXX_MASTER_KEY.
// Deleting a tenant's wrapped key makes its personal data unreadable
// (crypto-shredding).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
)

// Keyring wraps and unwraps tenant keys and caches unwrapped AEADs.
type Keyring struct {
	master cipher.AEAD
	mu     sync.RWMutex
	cache  map[string]cipher.AEAD
	blind  map[string][]byte // per-tenant key for blind indexes, derived from the data key
}

// NewKeyring builds a keyring from a 64-hex-character master key.
func NewKeyring(masterHex string) (*Keyring, error) {
	k, err := hex.DecodeString(masterHex)
	if err != nil || len(k) != 32 {
		return nil, errors.New("crypto: master key must be 64 hex characters (32 bytes)")
	}
	a, err := newAEAD(k)
	if err != nil {
		return nil, err
	}
	return &Keyring{master: a, cache: map[string]cipher.AEAD{}, blind: map[string][]byte{}}, nil
}

func newAEAD(k []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func seal(a cipher.AEAD, plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plain, aad), nil
}

func open(a cipher.AEAD, ct, aad []byte) ([]byte, error) {
	if len(ct) < a.NonceSize() {
		return nil, errors.New("crypto: ciphertext too short")
	}
	return a.Open(nil, ct[:a.NonceSize()], ct[a.NonceSize():], aad)
}

// NewWrappedKey creates a fresh tenant data key wrapped by the master key.
func (k *Keyring) NewWrappedKey(tenant string) ([]byte, error) {
	dk := make([]byte, 32)
	if _, err := rand.Read(dk); err != nil {
		return nil, err
	}
	return seal(k.master, dk, []byte("tenant:"+tenant))
}

// Load unwraps and caches the data key of tenant.
func (k *Keyring) Load(tenant string, wrapped []byte) error {
	dk, err := open(k.master, wrapped, []byte("tenant:"+tenant))
	if err != nil {
		return fmt.Errorf("crypto: unwrap key of %s: %w", tenant, err)
	}
	a, err := newAEAD(dk)
	if err != nil {
		return err
	}
	m := hmac.New(sha256.New, dk)
	m.Write([]byte("apexx/blind-index/v1"))
	k.mu.Lock()
	k.cache[tenant] = a
	k.blind[tenant] = m.Sum(nil)
	k.mu.Unlock()
	return nil
}

// BlindIndex returns a keyed, deterministic hex digest of value. It lets the
// database match personal data such as an email (for per-customer limits)
// without storing it in clear; destroying the tenant key makes it meaningless.
func (k *Keyring) BlindIndex(tenant, value string) (string, error) {
	b, err := k.Blind(tenant, "index", value)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Blind returns a keyed hash (HMAC-SHA256) of value under a key derived from
// the tenant data key. It lets encrypted fields (email, phone) be looked up by
// exact value without storing them in clear text; the same value in another
// tenant gives a different hash, and without the tenant key it cannot be
// brute-forced from the database alone.
func (k *Keyring) Blind(tenant, scope, value string) ([]byte, error) {
	k.mu.RLock()
	key := k.blind[tenant]
	k.mu.RUnlock()
	if key == nil {
		return nil, ErrNoKey
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(scope))
	m.Write([]byte{0})
	m.Write([]byte(value))
	return m.Sum(nil), nil
}

// ErrNoKey means the tenant key was not loaded.
var ErrNoKey = errors.New("crypto: tenant key not loaded")

// Encrypt seals plain with the tenant key; aad binds it to a record id.
func (k *Keyring) Encrypt(tenant string, plain []byte, aad string) ([]byte, error) {
	k.mu.RLock()
	a := k.cache[tenant]
	k.mu.RUnlock()
	if a == nil {
		return nil, ErrNoKey
	}
	return seal(a, plain, []byte(tenant+"|"+aad))
}

// Decrypt opens ct with the tenant key.
func (k *Keyring) Decrypt(tenant string, ct []byte, aad string) ([]byte, error) {
	k.mu.RLock()
	a := k.cache[tenant]
	k.mu.RUnlock()
	if a == nil {
		return nil, ErrNoKey
	}
	return open(a, ct, []byte(tenant+"|"+aad))
}

// Rewrap re-encrypts a tenant data key from the old master key to the new one.
// The data key itself does not change, so no personal data needs re-encryption.
func Rewrap(oldKR, newKR *Keyring, tenant string, wrapped []byte) ([]byte, error) {
	dk, err := open(oldKR.master, wrapped, []byte("tenant:"+tenant))
	if err != nil {
		return nil, fmt.Errorf("crypto: unwrap with old master key: %w", err)
	}
	return seal(newKR.master, dk, []byte("tenant:"+tenant))
}

// Hash returns a keyed hash of data for a purpose. It lets analytics group by
// a personal value, such as an e-mail, without storing it.
func (k *Keyring) Hash(tenant, purpose, data string) ([]byte, error) {
	return k.Blind(tenant, purpose, data)
}
