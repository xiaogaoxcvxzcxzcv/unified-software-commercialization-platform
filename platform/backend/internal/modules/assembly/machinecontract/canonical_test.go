package machinecontract

import (
	"errors"
	"testing"
)

func TestCanonicalizeAndDigestAreIndependentOfObjectOrderAndWhitespace(t *testing.T) {
	first := []byte("{\n  \"b\": 1, \"a\": {\"z\": true, \"x\": \"值\"}\n}")
	second := []byte("{\"a\":{\"x\":\"值\",\"z\":true},\"b\":1}")

	firstCanonical, err := Canonicalize(first)
	if err != nil {
		t.Fatal(err)
	}
	secondCanonical, err := Canonicalize(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstCanonical) != string(secondCanonical) {
		t.Fatalf("canonical documents differ:\n%s\n%s", firstCanonical, secondCanonical)
	}
	firstDigest, err := Digest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := Digest(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest || len(firstDigest) != 64 {
		t.Fatalf("digests = %q and %q", firstDigest, secondDigest)
	}
}

func TestCanonicalizePreservesArrayOrderAndRejectsInvalidJSON(t *testing.T) {
	first, err := Digest([]byte(`{"items":["a","b"]}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Digest([]byte(`{"items":["b","a"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("array order was not preserved by canonicalization")
	}
	if _, err := Canonicalize([]byte(`{"broken":`)); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func TestDigestWithoutTopLevelFieldIsStableAndRequiresField(t *testing.T) {
	first := []byte(`{"name":"catalog entry","self_checksum":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","nested":{"b":2,"a":1}}`)
	second := []byte(`{"nested":{"a":1,"b":2},"self_checksum":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","name":"catalog entry"}`)

	firstDigest, err := DigestWithoutTopLevelField(first, "self_checksum")
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := DigestWithoutTopLevelField(second, "self_checksum")
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest || len(firstDigest) != len("sha256:")+64 {
		t.Fatalf("content digests = %q and %q", firstDigest, secondDigest)
	}
	if _, err := DigestWithoutTopLevelField([]byte(`{"name":"missing"}`), "self_checksum"); err == nil {
		t.Fatal("missing digest field was accepted")
	}
}

func TestValidateSafeRelativePath(t *testing.T) {
	for _, value := range []string{"generated/pages/login.tsx", "integration/auth/client.ts", "platform.lock"} {
		if err := ValidateSafeRelativePath(value); err != nil {
			t.Fatalf("safe path %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", "/absolute", "C:/escape", `..\\escape`, "../escape", "generated/../custom/file", "generated//file", "generated/NUL.txt", "generated/file. "} {
		if err := ValidateSafeRelativePath(value); !errors.Is(err, ErrUnsafeRelativePath) {
			t.Fatalf("unsafe path %q error = %v", value, err)
		}
	}
}
