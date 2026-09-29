package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// THE KEYSLOT — the store's key, which seals the secrets it keeps (an embedding provider's API
// key), as AutoDB's service keyslot holds its master key (ADR 0087).
//
// It is a file beside the store, <store>.key, 32 random bytes, 0600: made the first time a secret
// is sealed, and refused when anyone but its owner can read it. A copied store without it opens no
// secret; anything running as the store's owner can read both, as it can the store.
//
// A secret is AES-256-GCM: a 12-byte nonce, then the ciphertext, bound by its additional data to
// what it is and to whose (the provider's id), so a sealed key moved to another row opens nowhere.

// keySize is the store key's length: AES-256.
const keySize = 32

// ErrKeyslotExposed is a keyslot file others can read: the store will not use it.
var ErrKeyslotExposed = errors.New("store: the keyslot file is readable by others; make it 0600")

// ErrSealed is a secret the store's key does not open: sealed under another key, or moved.
var ErrSealed = errors.New("store: the secret does not open with this store's key")

// keyslot is the store's key, read or made once.
type keyslot struct {
	path string
	once sync.Once
	key  []byte
	err  error
}

// get is the key: read from the file, or made there when there is none.
func (k *keyslot) get() ([]byte, error) {
	k.once.Do(func() { k.key, k.err = k.load() })
	return k.key, k.err
}

func (k *keyslot) load() ([]byte, error) {
	fi, err := os.Stat(k.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return k.make()
	case err != nil:
		return nil, err
	case fi.Mode().Perm()&0o077 != 0:
		return nil, fmt.Errorf("%w: %s is %v", ErrKeyslotExposed, k.path, fi.Mode().Perm())
	}
	key, err := os.ReadFile(k.path)
	if err != nil {
		return nil, err
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("store: the keyslot %s is %d bytes, want %d", k.path, len(key), keySize)
	}
	return key, nil
}

// make writes a new key, only when no file is there: two makers never both win.
func (k *keyslot) make() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(k.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return k.load() // another made it first
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		_ = os.Remove(k.path)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return key, f.Close()
}

// seal encrypts plaintext for what aad names.
func (k *keyslot) seal(plaintext []byte, aad string) ([]byte, error) {
	gcm, err := k.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, []byte(aad)), nil
}

// open decrypts a sealed secret for what aad names.
func (k *keyslot) open(sealed []byte, aad string) ([]byte, error) {
	gcm, err := k.aead()
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, ErrSealed
	}
	out, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte(aad))
	if err != nil {
		return nil, ErrSealed
	}
	return out, nil
}

func (k *keyslot) aead() (cipher.AEAD, error) {
	key, err := k.get()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
