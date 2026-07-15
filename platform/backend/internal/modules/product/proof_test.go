package product

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

func TestVersionedProofVerifierSupportsPepperedSecretAndEd25519(t *testing.T) {
	hasher, err := securevalue.NewHasher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	verifier := NewVersionedProofVerifier(hasher)
	secret := "client_secret_abcdefghijklmnopqrstuvwxyz0123456789"
	authentication := ClientAuthentication{Credential: ClientCredential{ProofType: "hmac_sha256_v1", ProofDigest: verifier.SharedSecretDigest(secret)}}
	if err := verifier.VerifyClientProof(context.Background(), authentication, ClientProof{Type: "hmac_sha256_v1", Value: secret}); err != nil {
		t.Fatalf("VerifyClientProof(hmac) error = %v", err)
	}
	if err := verifier.VerifyClientProof(context.Background(), authentication, ClientProof{Type: "hmac_sha256_v1", Value: secret + "x"}); err == nil {
		t.Fatal("VerifyClientProof(hmac) accepted the wrong secret")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("client-1\ncredential-1\nnonce\n1.0.0\n2026-07-13T00:00:00Z")
	signature := ed25519.Sign(privateKey, payload)
	authentication.Credential = ClientCredential{ProofType: "ed25519_signature_v1", PublicKey: base64.RawURLEncoding.EncodeToString(publicKey)}
	if err := verifier.VerifyClientProof(context.Background(), authentication, ClientProof{Type: "ed25519_signature_v1", Payload: payload, Signature: signature}); err != nil {
		t.Fatalf("VerifyClientProof(ed25519) error = %v", err)
	}
}
