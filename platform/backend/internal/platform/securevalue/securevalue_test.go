package securevalue

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestHasherUsesPepperAndDoesNotReturnPlaintext(t *testing.T) {
	h1, _ := NewHasher(strings.Repeat("a", 32))
	h2, _ := NewHasher(strings.Repeat("b", 32))
	one := h1.Digest("secret-token")
	two := h2.Digest("secret-token")
	if bytes.Equal(one, two) || bytes.Contains(one, []byte("secret-token")) {
		t.Fatal("digest did not isolate pepper or leaked plaintext")
	}
}

func TestTokenHasPrefixAndEntropy(t *testing.T) {
	one, err := Token("adm_at_")
	if err != nil {
		t.Fatal(err)
	}
	two, _ := Token("adm_at_")
	if one == two || !strings.HasPrefix(one, "adm_at_") || len(one) < 40 {
		t.Fatalf("unexpected generated tokens")
	}
}

func TestGeneratorPropagatesRandomSourceFailure(t *testing.T) {
	sourceErr := errors.New("random source unavailable")
	generator := NewGenerator(failingReader{err: sourceErr})

	if value, err := generator.ID("id_"); value != "" || !errors.Is(err, sourceErr) {
		t.Fatalf("ID() = %q, %v; want empty value and source error", value, err)
	}
	if value, err := generator.Token("token_"); value != "" || !errors.Is(err, sourceErr) {
		t.Fatalf("Token() = %q, %v; want empty value and source error", value, err)
	}
}

func TestGeneratorRejectsNilRandomSource(t *testing.T) {
	generator := NewGenerator(nil)
	if _, err := generator.ID("id_"); err == nil {
		t.Fatal("expected identifier generation to reject a nil random source")
	}
	if _, err := generator.Token("token_"); err == nil {
		t.Fatal("expected token generation to reject a nil random source")
	}
}
