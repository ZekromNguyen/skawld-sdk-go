package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// New returns a cryptographically random UUIDv4. Entropy failure is returned
// to the caller; security-sensitive identifiers must never fall back to time
// or another predictable source.
func New() (string, error) {
	return newFrom(rand.Reader)
}

func newFrom(reader io.Reader) (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(reader, b[:]); err != nil {
		return "", fmt.Errorf("generate secure identifier: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]), hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16])), nil
}
