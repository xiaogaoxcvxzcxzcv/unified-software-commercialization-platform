package securevalue

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

type Hasher struct {
	pepper []byte
}

func (h Hasher) Configured() bool {
	return len(h.pepper) >= 32
}

func NewHasher(pepper string) (Hasher, error) {
	if len(pepper) < 32 {
		return Hasher{}, fmt.Errorf("token pepper must be at least 32 bytes")
	}
	return Hasher{pepper: []byte(pepper)}, nil
}

func (h Hasher) Digest(value string) []byte {
	mac := hmac.New(sha256.New, h.pepper)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func (h Hasher) DigestHex(value string) string {
	return hex.EncodeToString(h.Digest(value))
}

type Generator struct {
	reader io.Reader
}

func NewGenerator(reader io.Reader) Generator {
	return Generator{reader: reader}
}

func DefaultGenerator() Generator {
	return NewGenerator(rand.Reader)
}

func (g Generator) Token(prefix string) (string, error) {
	if g.reader == nil {
		return "", fmt.Errorf("generate secure token: random source is nil")
	}
	var raw [32]byte
	if _, err := io.ReadFull(g.reader, raw[:]); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (g Generator) ID(prefix string) (string, error) {
	if g.reader == nil {
		return "", fmt.Errorf("generate identifier: random source is nil")
	}
	var raw [16]byte
	if _, err := io.ReadFull(g.reader, raw[:]); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func Token(prefix string) (string, error) {
	return DefaultGenerator().Token(prefix)
}

func ID(prefix string) (string, error) {
	return DefaultGenerator().ID(prefix)
}
