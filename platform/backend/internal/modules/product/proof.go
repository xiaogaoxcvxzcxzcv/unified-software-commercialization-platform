package product

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"

	"platform.local/capability-platform/backend/internal/platform/securevalue"
)

type VersionedProofVerifier struct{ hasher securevalue.Hasher }

func NewVersionedProofVerifier(hasher securevalue.Hasher) VersionedProofVerifier {
	return VersionedProofVerifier{hasher: hasher}
}

func (v VersionedProofVerifier) SharedSecretDigest(secret string) string {
	return "sha256:" + v.hasher.DigestHex("product-client-proof:"+secret)
}

func (v VersionedProofVerifier) VerifyClientProof(_ context.Context, authentication ClientAuthentication, proof ClientProof) error {
	if proof.Type != authentication.Credential.ProofType {
		return ErrCredentialUnavailable
	}
	switch proof.Type {
	case "hmac_sha256_v1":
		if proof.Value == "" || !DigestsEqual(authentication.Credential.ProofDigest, v.SharedSecretDigest(proof.Value)) {
			return ErrCredentialUnavailable
		}
		return nil
	case "ed25519_signature_v1":
		publicKey, err := base64.RawURLEncoding.DecodeString(authentication.Credential.PublicKey)
		if err != nil || len(publicKey) != ed25519.PublicKeySize || len(proof.Payload) == 0 {
			return ErrCredentialUnavailable
		}
		signature := proof.Signature
		if len(signature) == 0 {
			signature, err = base64.RawURLEncoding.DecodeString(proof.Value)
		}
		if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), proof.Payload, signature) {
			return ErrCredentialUnavailable
		}
		return nil
	default:
		return errors.Join(ErrCredentialUnavailable, ErrInvalidCommand)
	}
}
