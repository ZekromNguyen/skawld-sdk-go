package httpadapter

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ZekromNguyen/skawld-sdk-go/core"
)

const (
	HeaderTenantID  = "X-Skawld-Tenant-ID"
	HeaderActorID   = "X-Skawld-Actor-ID"
	HeaderTimestamp = "X-Skawld-Timestamp"
	HeaderSignature = "X-Skawld-Signature"
)

var ErrAuthentication = errors.New("observation request authentication failed")

type Authenticator interface {
	Authenticate(context.Context, *http.Request, []byte) (core.Principal, error)
}

type AuthenticatorFunc func(
	context.Context,
	*http.Request,
	[]byte,
) (core.Principal, error)

func (authenticate AuthenticatorFunc) Authenticate(
	ctx context.Context,
	request *http.Request,
	body []byte,
) (core.Principal, error) {
	return authenticate(ctx, request, body)
}

type SecretResolver interface {
	Secret(context.Context, string, string) ([]byte, bool, error)
}

type SecretResolverFunc func(context.Context, string, string) ([]byte, bool, error)

func (resolve SecretResolverFunc) Secret(
	ctx context.Context,
	tenantID string,
	actorID string,
) ([]byte, bool, error) {
	return resolve(ctx, tenantID, actorID)
}

type Identity struct {
	TenantID string
	ActorID  string
}

type staticSecrets struct {
	items map[Identity][]byte
}

func NewStaticSecrets(input map[Identity][]byte) SecretResolver {
	items := make(map[Identity][]byte, len(input))
	for identity, secret := range input {
		items[identity] = append([]byte(nil), secret...)
	}
	return staticSecrets{items: items}
}

func (secrets staticSecrets) Secret(
	ctx context.Context,
	tenantID string,
	actorID string,
) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	secret, exists := secrets.items[Identity{TenantID: tenantID, ActorID: actorID}]
	return append([]byte(nil), secret...), exists, nil
}

type HMACOptions struct {
	Secrets      SecretResolver
	MaxClockSkew time.Duration
	Now          func() time.Time
}

type HMACAuthenticator struct {
	secrets      SecretResolver
	maxClockSkew time.Duration
	now          func() time.Time
}

func NewHMACAuthenticator(options HMACOptions) (*HMACAuthenticator, error) {
	if options.Secrets == nil {
		return nil, core.NewConfigError("HTTP observation HMAC secrets are required")
	}
	if options.MaxClockSkew == 0 {
		options.MaxClockSkew = 5 * time.Minute
	}
	if options.MaxClockSkew < 0 || options.MaxClockSkew > time.Hour {
		return nil, core.NewConfigError(
			"HTTP observation HMAC clock skew must be between zero and one hour",
		)
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &HMACAuthenticator{
		secrets: options.Secrets, maxClockSkew: options.MaxClockSkew, now: options.Now,
	}, nil
}

func (authenticator *HMACAuthenticator) Authenticate(
	ctx context.Context,
	request *http.Request,
	body []byte,
) (core.Principal, error) {
	if authenticator == nil {
		return core.Principal{}, core.NewConfigError(
			"HTTP observation HMAC authenticator is nil",
		)
	}
	if err := ctx.Err(); err != nil {
		return core.Principal{}, err
	}
	tenantID := strings.TrimSpace(request.Header.Get(HeaderTenantID))
	actorID := strings.TrimSpace(request.Header.Get(HeaderActorID))
	timestampText := strings.TrimSpace(request.Header.Get(HeaderTimestamp))
	signatureText := strings.TrimSpace(request.Header.Get(HeaderSignature))
	if !validHeaderValue(tenantID) || !validHeaderValue(actorID) ||
		timestampText == "" || signatureText == "" {
		return core.Principal{}, ErrAuthentication
	}
	timestamp, err := time.Parse(time.RFC3339Nano, timestampText)
	if err != nil {
		return core.Principal{}, ErrAuthentication
	}
	difference := authenticator.now().Sub(timestamp)
	if difference < 0 {
		difference = -difference
	}
	if difference > authenticator.maxClockSkew {
		return core.Principal{}, ErrAuthentication
	}
	secret, exists, err := authenticator.secrets.Secret(ctx, tenantID, actorID)
	if err != nil {
		return core.Principal{}, fmt.Errorf("resolve HTTP observation HMAC secret: %w", err)
	}
	if !exists || len(secret) == 0 {
		return core.Principal{}, ErrAuthentication
	}
	provided, err := hex.DecodeString(signatureText)
	if err != nil {
		return core.Principal{}, ErrAuthentication
	}
	expected, err := hex.DecodeString(Signature(secret, timestampText, tenantID, actorID, body))
	if err != nil {
		return core.Principal{}, fmt.Errorf("decode HTTP observation HMAC signature: %w", err)
	}
	if !hmac.Equal(provided, expected) {
		return core.Principal{}, ErrAuthentication
	}
	return core.Principal{TenantID: tenantID, ActorID: actorID}, nil
}

func Signature(secret []byte, timestamp, tenantID, actorID string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(tenantID))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write([]byte(actorID))
	_, _ = mac.Write([]byte{'\n'})
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func validHeaderValue(value string) bool {
	return value != "" &&
		len(value) <= 256 &&
		!strings.ContainsAny(value, "\r\n")
}
