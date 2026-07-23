package notification

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

type HMACSecurityDigester struct {
	keyRef   string
	resolver SecretResolver
}

func NewHMACSecurityDigester(keyRef string, resolver SecretResolver) (*HMACSecurityDigester, error) {
	if !validIdentifier(keyRef, 1, 256) || resolver == nil {
		return nil, ErrProviderUnavailable
	}
	return &HMACSecurityDigester{keyRef: keyRef, resolver: resolver}, nil
}

func (d *HMACSecurityDigester) Digest(ctx context.Context, domain string, fields ...string) ([]byte, error) {
	if d == nil || d.resolver == nil || !validIdentifier(domain, 1, 160) {
		return nil, ErrProviderUnavailable
	}
	key, err := d.resolver.ResolveSecret(ctx, d.keyRef)
	if err != nil || len(key) < 32 {
		return nil, ErrProviderUnavailable
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	writeDigestField(mac, domain)
	for _, field := range fields {
		writeDigestField(mac, field)
	}
	return mac.Sum(nil), nil
}

type digestWriter interface {
	Write([]byte) (int, error)
}

func writeDigestField(writer digestWriter, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write([]byte(value))
}

var _ SecurityDigestPort = (*HMACSecurityDigester)(nil)
