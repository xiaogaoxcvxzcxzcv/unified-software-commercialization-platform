package hostedinteraction

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

type SecretResolver interface {
	ResolveSecret(context.Context, string) ([]byte, error)
}

type AEADStateProtector struct {
	keyRef   string
	resolver SecretResolver
	random   io.Reader
}

func NewAEADStateProtector(keyRef string, resolver SecretResolver) (*AEADStateProtector, error) {
	if keyRef == "" || len(keyRef) > 256 || resolver == nil {
		return nil, ErrTemporarilyUnavailable
	}
	return &AEADStateProtector{keyRef: keyRef, resolver: resolver, random: rand.Reader}, nil
}

func (p *AEADStateProtector) Protect(ctx context.Context, securityContext StateContext, state string) (ProtectedState, error) {
	if p == nil || p.resolver == nil || !validStateContext(securityContext) || state == "" || len(state) > 512 {
		return ProtectedState{}, ErrStateMismatch
	}
	key, err := p.resolver.ResolveSecret(ctx, p.keyRef)
	if err != nil {
		return ProtectedState{}, ErrTemporarilyUnavailable
	}
	defer clear(key)
	aead, err := stateAEAD(key)
	if err != nil {
		return ProtectedState{}, ErrTemporarilyUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(p.random, nonce); err != nil {
		return ProtectedState{}, ErrTemporarilyUnavailable
	}
	aad := stateAAD(securityContext, p.keyRef)
	ciphertext := aead.Seal(nil, nonce, []byte(state), aad)
	protected := append(append([]byte(nil), nonce...), ciphertext...)
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write([]byte("hosted-interaction.state-digest.v1\x00"))
	_, _ = digest.Write(aad)
	_, _ = digest.Write([]byte(state))
	return ProtectedState{KeyRef: p.keyRef, Ciphertext: protected, Digest: digest.Sum(nil)}, nil
}

func (p *AEADStateProtector) Reveal(ctx context.Context, securityContext StateContext, protected ProtectedState) (string, error) {
	if p == nil || p.resolver == nil || !validStateContext(securityContext) || protected.KeyRef == "" {
		return "", ErrStateMismatch
	}
	key, err := p.resolver.ResolveSecret(ctx, protected.KeyRef)
	if err != nil {
		return "", ErrTemporarilyUnavailable
	}
	defer clear(key)
	aead, err := stateAEAD(key)
	if err != nil || len(protected.Ciphertext) <= aead.NonceSize() {
		return "", ErrStateMismatch
	}
	nonce, ciphertext := protected.Ciphertext[:aead.NonceSize()], protected.Ciphertext[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, stateAAD(securityContext, protected.KeyRef))
	if err != nil || len(plain) == 0 || len(plain) > 512 {
		return "", ErrStateMismatch
	}
	return string(plain), nil
}

func stateAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("hosted interaction state key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func validStateContext(value StateContext) bool {
	return value.InteractionID != "" && (value.Route == RouteAuth || value.Route == RouteAccount) && validScope(value.Scope) && value.TraceID != ""
}

func stateAAD(value StateContext, keyRef string) []byte {
	fields := []string{"hosted-interaction.state.v1", keyRef, value.InteractionID, string(value.Route), value.Scope.ProductID,
		value.Scope.ApplicationID, optional(value.Scope.TenantID), value.Scope.Environment, string(value.Scope.Channel), value.TraceID}
	var result bytes.Buffer
	for _, field := range fields {
		_ = binary.Write(&result, binary.BigEndian, uint32(len(field)))
		_, _ = result.WriteString(field)
	}
	return result.Bytes()
}
