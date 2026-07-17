package notification

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
	"time"
)

type AEADSecurityPayloadProtector struct {
	keyRef   string
	resolver SecretResolver
	random   io.Reader
}

func NewAEADSecurityPayloadProtector(keyRef string, resolver SecretResolver) (*AEADSecurityPayloadProtector, error) {
	if !validIdentifier(keyRef, 1, 256) || resolver == nil {
		return nil, ErrPayloadUnavailable
	}
	return &AEADSecurityPayloadProtector{keyRef: keyRef, resolver: resolver, random: rand.Reader}, nil
}

func (p *AEADSecurityPayloadProtector) Seal(ctx context.Context, securityContext SecurityPayloadContext, payload SecurityPayload) (ProtectedSecurityPayload, error) {
	if p == nil || p.resolver == nil || !validPayloadContext(securityContext) || payload.Destination == "" || payload.Proof == "" {
		return ProtectedSecurityPayload{}, ErrPayloadUnavailable
	}
	key, err := p.resolver.ResolveSecret(ctx, p.keyRef)
	if err != nil {
		return ProtectedSecurityPayload{}, ErrProviderUnavailable
	}
	defer clear(key)
	aead, err := securityAEAD(key)
	if err != nil {
		return ProtectedSecurityPayload{}, ErrPayloadUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(p.random, nonce); err != nil {
		return ProtectedSecurityPayload{}, ErrPayloadUnavailable
	}
	plain := encodeSecurityPayload(payload)
	aad := encodeSecurityPayloadAAD(securityContext, p.keyRef)
	ciphertext := aead.Seal(nil, nonce, plain, aad)
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write(aad)
	_, _ = digest.Write(plain)
	return ProtectedSecurityPayload{KeyRef: p.keyRef, Nonce: nonce, Ciphertext: ciphertext, Digest: digest.Sum(nil)}, nil
}

func (p *AEADSecurityPayloadProtector) Open(ctx context.Context, securityContext SecurityPayloadContext, protected ProtectedSecurityPayload) (SecurityPayload, error) {
	if p == nil || p.resolver == nil || !validPayloadContext(securityContext) || protected.KeyRef == "" || len(protected.Nonce) == 0 || len(protected.Ciphertext) == 0 {
		return SecurityPayload{}, ErrPayloadUnavailable
	}
	key, err := p.resolver.ResolveSecret(ctx, protected.KeyRef)
	if err != nil {
		return SecurityPayload{}, ErrProviderUnavailable
	}
	defer clear(key)
	aead, err := securityAEAD(key)
	if err != nil || len(protected.Nonce) != aead.NonceSize() {
		return SecurityPayload{}, ErrPayloadUnavailable
	}
	aad := encodeSecurityPayloadAAD(securityContext, protected.KeyRef)
	plain, err := aead.Open(nil, protected.Nonce, protected.Ciphertext, aad)
	if err != nil {
		return SecurityPayload{}, ErrPayloadUnavailable
	}
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write(aad)
	_, _ = digest.Write(plain)
	if len(protected.Digest) != sha256.Size || !hmac.Equal(protected.Digest, digest.Sum(nil)) {
		return SecurityPayload{}, ErrPayloadUnavailable
	}
	payload, ok := decodeSecurityPayload(plain)
	if !ok {
		return SecurityPayload{}, ErrPayloadUnavailable
	}
	return payload, nil
}

func validPayloadContext(value SecurityPayloadContext) bool {
	return value.DeliveryID != "" && value.Purpose != "" && value.ProductID != "" && value.ApplicationID != "" &&
		value.ProviderRef != "" && value.DestinationType != "" && !value.ExpiresAt.IsZero() && value.TraceID != ""
}

func encodeSecurityPayloadAAD(value SecurityPayloadContext, keyRef string) []byte {
	fields := []string{"notification.security.payload.v1", keyRef, value.DeliveryID, value.Purpose, value.ProductID,
		value.ApplicationID, optionalString(value.TenantID), value.ProviderRef, value.DestinationType,
		value.ExpiresAt.UTC().Format(time.RFC3339Nano), value.TraceID}
	var result bytes.Buffer
	for _, field := range fields {
		writeDigestField(&result, field)
	}
	return result.Bytes()
}

func securityAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("invalid notification security key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encodeSecurityPayload(payload SecurityPayload) []byte {
	result := make([]byte, 8+len(payload.Destination)+len(payload.Proof))
	binary.BigEndian.PutUint32(result[:4], uint32(len(payload.Destination)))
	copy(result[4:], payload.Destination)
	offset := 4 + len(payload.Destination)
	binary.BigEndian.PutUint32(result[offset:offset+4], uint32(len(payload.Proof)))
	copy(result[offset+4:], payload.Proof)
	return result
}

func decodeSecurityPayload(raw []byte) (SecurityPayload, bool) {
	if len(raw) < 8 {
		return SecurityPayload{}, false
	}
	destinationLength := int(binary.BigEndian.Uint32(raw[:4]))
	if destinationLength < 1 || destinationLength > len(raw)-8 {
		return SecurityPayload{}, false
	}
	offset := 4 + destinationLength
	proofLength := int(binary.BigEndian.Uint32(raw[offset : offset+4]))
	if proofLength < 1 || offset+4+proofLength != len(raw) {
		return SecurityPayload{}, false
	}
	return SecurityPayload{Destination: string(raw[4:offset]), Proof: string(raw[offset+4:])}, true
}
