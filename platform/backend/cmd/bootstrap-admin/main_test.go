package main

import (
	"bytes"
	"errors"
	"testing"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func TestReadBootstrapPassword(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    string
		wantErr bool
	}{
		{name: "raw bytes", input: []byte("correct-password-123"), want: "correct-password-123"},
		{name: "terminal newline", input: []byte("correct-password-123\r\n"), want: "correct-password-123"},
		{name: "PowerShell UTF-8 BOM", input: append([]byte{0xef, 0xbb, 0xbf}, []byte("correct-password-123\r\n")...), want: "correct-password-123"},
		{name: "preserves spaces", input: []byte(" correct-password-123 \r\n"), want: " correct-password-123 "},
		{name: "empty", input: nil, wantErr: true},
		{name: "only BOM and newline", input: []byte{0xef, 0xbb, 0xbf, '\r', '\n'}, wantErr: true},
		{name: "too long", input: bytes.Repeat([]byte("a"), 4097), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := readBootstrapPassword(bytes.NewReader(test.input))
			if test.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("read password: %v", err)
			}
			if string(got) != test.want {
				t.Fatalf("password mismatch: got length %d, want length %d", len(got), len(test.want))
			}
		})
	}
}

func TestReadBootstrapPasswordPropagatesReaderFailure(t *testing.T) {
	_, err := readBootstrapPassword(failingReader{})
	if err == nil {
		t.Fatal("expected reader error")
	}
}
