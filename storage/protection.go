// Package storage defines production data-protection and retention contracts
// shared by durable storage adapters.
package storage

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

type DocumentProtector interface {
	Protect(context.Context, []byte) ([]byte, error)
	Unprotect(context.Context, []byte) ([]byte, error)
}

type ProtectedDocumentDetector interface {
	IsProtected([]byte) bool
}

type EncryptionKey struct {
	ID    string
	Bytes []byte
}

type TenantKeyProvider interface {
	CurrentKey(context.Context, string) (EncryptionKey, error)
	Key(context.Context, string, string) (EncryptionKey, error)
}

type StaticKeyProvider struct {
	mu   sync.RWMutex
	keys map[string]map[string][]byte
	now  map[string]string
}

func NewStaticKeyProvider(
	current map[string]EncryptionKey,
) (*StaticKeyProvider, error) {
	provider := &StaticKeyProvider{
		keys: make(map[string]map[string][]byte, len(current)),
		now:  make(map[string]string, len(current)),
	}
	for tenantID, key := range current {
		tenantID = strings.TrimSpace(tenantID)
		if tenantID == "" || !validEncryptionKey(key) {
			return nil, core.NewConfigError(
				"static storage encryption key is invalid",
			)
		}
		provider.keys[tenantID] = map[string][]byte{
			key.ID: append([]byte(nil), key.Bytes...),
		}
		provider.now[tenantID] = key.ID
	}
	return provider, nil
}

func (p *StaticKeyProvider) CurrentKey(
	ctx context.Context,
	tenantID string,
) (EncryptionKey, error) {
	if err := ctx.Err(); err != nil {
		return EncryptionKey{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	keyID := p.now[tenantID]
	key := p.keys[tenantID][keyID]
	if keyID == "" || len(key) == 0 {
		return EncryptionKey{}, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "storage encryption key not found",
		}
	}
	return EncryptionKey{
		ID: keyID, Bytes: append([]byte(nil), key...),
	}, nil
}

func (p *StaticKeyProvider) Key(
	ctx context.Context,
	tenantID string,
	keyID string,
) (EncryptionKey, error) {
	if err := ctx.Err(); err != nil {
		return EncryptionKey{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	key := p.keys[tenantID][keyID]
	if len(key) == 0 {
		return EncryptionKey{}, &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "storage encryption key not found",
		}
	}
	return EncryptionKey{
		ID: keyID, Bytes: append([]byte(nil), key...),
	}, nil
}

// Rotate installs a new current key for a tenant while retaining historical
// keys for decryption. Callers can then re-protect durable documents before
// retiring the old key in their external key manager.
func (p *StaticKeyProvider) Rotate(
	tenantID string,
	key EncryptionKey,
) error {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" || !validEncryptionKey(key) {
		return core.NewConfigError(
			"static storage encryption rotation key is invalid",
		)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.keys[tenantID] == nil {
		p.keys[tenantID] = make(map[string][]byte)
	}
	if existing := p.keys[tenantID][key.ID]; len(existing) > 0 &&
		!bytes.Equal(existing, key.Bytes) {
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "storage encryption key id cannot be reused with different key material",
		}
	}
	p.keys[tenantID][key.ID] = append([]byte(nil), key.Bytes...)
	p.now[tenantID] = key.ID
	return nil
}

// Retire removes a historical key. The current key cannot be retired. Durable
// data must be re-protected and verified before this method is called.
func (p *StaticKeyProvider) Retire(tenantID, keyID string) error {
	tenantID = strings.TrimSpace(tenantID)
	keyID = strings.TrimSpace(keyID)
	if tenantID == "" || keyID == "" {
		return core.NewConfigError(
			"static storage encryption retirement identity is invalid",
		)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.now[tenantID] == keyID {
		return &core.SkawldError{
			Kind:    core.ErrorConflict,
			Message: "current storage encryption key cannot be retired",
		}
	}
	if len(p.keys[tenantID][keyID]) == 0 {
		return &core.SkawldError{
			Kind:    core.ErrorNotFound,
			Message: "storage encryption key not found",
		}
	}
	delete(p.keys[tenantID], keyID)
	return nil
}

type AESGCMProtector struct {
	keys TenantKeyProvider
	rand io.Reader
}

func NewAESGCMProtector(
	keys TenantKeyProvider,
) (*AESGCMProtector, error) {
	if keys == nil {
		return nil, core.NewConfigError(
			"AES-GCM storage protection requires tenant keys",
		)
	}
	return &AESGCMProtector{keys: keys, rand: rand.Reader}, nil
}

type encryptedDocument struct {
	Version    int    `json:"version"`
	KeyID      string `json:"key_id"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func (p *AESGCMProtector) Protect(
	ctx context.Context,
	plaintext []byte,
) ([]byte, error) {
	principal, err := storagePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	key, err := p.keys.CurrentKey(ctx, principal.TenantID)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(p.rand, nonce); err != nil {
		return nil, fmt.Errorf("generate storage encryption nonce: %w", err)
	}
	ciphertext := aead.Seal(
		nil, nonce, plaintext, storageAAD(principal.TenantID),
	)
	return json.Marshal(encryptedDocument{
		Version: 1, KeyID: key.ID,
		Nonce: nonce, Ciphertext: ciphertext,
	})
}

func (p *AESGCMProtector) Unprotect(
	ctx context.Context,
	protected []byte,
) ([]byte, error) {
	principal, err := storagePrincipal(ctx)
	if err != nil {
		return nil, err
	}
	var document encryptedDocument
	if err := json.Unmarshal(protected, &document); err != nil {
		return nil, &core.SkawldError{
			Kind:    core.ErrorValidation,
			Message: "stored document is not a protected envelope",
			Cause:   err,
		}
	}
	if document.Version != 1 || strings.TrimSpace(document.KeyID) == "" {
		return nil, core.NewConfigError(
			"stored document protection envelope is unsupported",
		)
	}
	key, err := p.keys.Key(ctx, principal.TenantID, document.KeyID)
	if err != nil {
		return nil, &core.SkawldError{
			Kind:    core.ErrorPermissionDenied,
			Message: "stored document key is unavailable for this tenant",
			Cause:   err,
		}
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(document.Nonce) != aead.NonceSize() {
		return nil, core.NewConfigError(
			"stored document encryption nonce is invalid",
		)
	}
	plaintext, err := aead.Open(
		nil, document.Nonce, document.Ciphertext,
		storageAAD(principal.TenantID),
	)
	if err != nil {
		return nil, &core.SkawldError{
			Kind:    core.ErrorPermissionDenied,
			Message: "stored document authentication failed",
			Cause:   err,
		}
	}
	return plaintext, nil
}

func (p *AESGCMProtector) IsProtected(raw []byte) bool {
	var document encryptedDocument
	return json.Unmarshal(raw, &document) == nil &&
		document.Version == 1 &&
		strings.TrimSpace(document.KeyID) != "" &&
		len(document.Nonce) > 0 &&
		len(document.Ciphertext) > 0
}

func newAEAD(key EncryptionKey) (cipher.AEAD, error) {
	if !validEncryptionKey(key) {
		return nil, core.NewConfigError(
			"storage encryption requires a named 256-bit key",
		)
	}
	block, err := aes.NewCipher(key.Bytes)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func validEncryptionKey(key EncryptionKey) bool {
	return strings.TrimSpace(key.ID) != "" &&
		len(key.ID) <= 256 && len(key.Bytes) == 32
}

func storagePrincipal(ctx context.Context) (core.Principal, error) {
	principal, ok := core.PrincipalFromContext(ctx)
	if !ok || !principal.Authenticated() {
		return core.Principal{}, core.NewPermissionError(
			"protected storage requires authenticated tenant and actor identities",
		)
	}
	return principal, nil
}

func storageAAD(tenantID string) []byte {
	return []byte("skawld.storage.document.v1\x00" + tenantID)
}

var _ DocumentProtector = (*AESGCMProtector)(nil)
var _ ProtectedDocumentDetector = (*AESGCMProtector)(nil)
