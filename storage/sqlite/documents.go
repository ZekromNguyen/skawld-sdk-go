package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	sdkstorage "github.com/ZekromNguyen/skawld-sdk-go/storage"
)

type documentCodec struct {
	protector             sdkstorage.DocumentProtector
	allowUnprotectedReads bool
}

func (c documentCodec) marshal(
	ctx context.Context,
	value interface{},
) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if c.protector == nil {
		return raw, nil
	}
	protected, err := c.protector.Protect(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("protect workflow storage document: %w", err)
	}
	return protected, nil
}

func (c documentCodec) unmarshal(
	ctx context.Context,
	raw []byte,
	target interface{},
) error {
	document := raw
	if c.protector != nil {
		unprotected, err := c.protector.Unprotect(ctx, raw)
		if err != nil {
			if detector, ok := c.protector.(sdkstorage.ProtectedDocumentDetector); ok &&
				detector.IsProtected(raw) {
				return fmt.Errorf(
					"unprotect workflow storage document: %w", err,
				)
			}
			if !c.allowUnprotectedReads ||
				!json.Valid(raw) {
				return fmt.Errorf(
					"unprotect workflow storage document: %w", err,
				)
			}
		} else {
			document = unprotected
		}
	}
	return json.Unmarshal(document, target)
}
