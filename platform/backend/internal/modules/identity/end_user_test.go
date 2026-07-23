package identity

import (
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestStrictIdentifierNormalizer(t *testing.T) {
	normalizer := StrictIdentifierNormalizer{}
	email, err := normalizer.Normalize(IdentifierEmail, "  User@Example.COM ")
	if err != nil || email.Value != "user@example.com" || email.NormalizationVersion != 1 {
		t.Fatalf("email = %+v, err = %v", email, err)
	}
	phone, err := normalizer.Normalize(IdentifierPhone, "+8613812345678")
	if err != nil || phone.Value != "+8613812345678" {
		t.Fatalf("phone = %+v, err = %v", phone, err)
	}
	for _, invalid := range []struct {
		kind  IdentifierType
		value string
	}{
		{IdentifierEmail, "display <user@example.com>"},
		{IdentifierEmail, "missing-at.example.com"},
		{IdentifierPhone, "13812345678"},
		{IdentifierPhone, "+012345678"},
	} {
		if _, err := normalizer.Normalize(invalid.kind, invalid.value); !errors.Is(err, ErrInvalidEndUserIdentifier) {
			t.Fatalf("Normalize(%q) error = %v", invalid.value, err)
		}
	}
}

func TestValidateAdaptivePasswordHashParsesEncoding(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("test-only password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAdaptivePasswordHash("bcrypt", hash); err != nil {
		t.Fatalf("valid bcrypt rejected: %v", err)
	}
	for _, test := range []struct {
		algorithm string
		hash      []byte
	}{{"bcrypt", []byte("not-a-bcrypt-hash")}, {"argon2id", []byte("$argon2id$v=19$m=65536,t=3,p=2$fake$fake")}} {
		if err := ValidateAdaptivePasswordHash(test.algorithm, test.hash); err == nil {
			t.Fatalf("accepted unverifiable %s hash", test.algorithm)
		}
	}
	weak, err := bcrypt.GenerateFromPassword([]byte("test-only password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAdaptivePasswordHash("bcrypt", weak); err == nil {
		t.Fatal("accepted bcrypt hash below DefaultCost")
	}
}
