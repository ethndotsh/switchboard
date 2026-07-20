package engine

import (
	"errors"
	"testing"

	"github.com/ethndotsh/switchboard/internal/bundle"
)

func TestCheckDataSize(t *testing.T) {
	data := map[string][]byte{
		"data/a.txt": []byte("hello"),
		"data/b.txt": []byte("world!"),
	}
	if err := checkDataSize(data, 100); err != nil {
		t.Fatalf("within limit should pass: %v", err)
	}
	if err := checkDataSize(data, 0); err != nil {
		t.Fatalf("zero limit disables the check: %v", err)
	}
	err := checkDataSize(data, 5)
	if !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("over limit = %v, want bundle.ErrInvalid", err)
	}
}

func TestResolveConfigMaxDataBytes(t *testing.T) {
	resolved, err := ResolveConfig(Config{MaxDataBytes: "1mb"})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if resolved.MaxDataBytes != 1<<20 {
		t.Fatalf("MaxDataBytes = %d, want %d", resolved.MaxDataBytes, 1<<20)
	}
	if _, err := ResolveConfig(Config{MaxDataBytes: "128mb"}); err == nil {
		t.Fatal("expected error for oversize max_data_bytes")
	}
	def, err := ResolveConfig(Config{})
	if err != nil {
		t.Fatalf("ResolveConfig default: %v", err)
	}
	if def.MaxDataBytes != DefaultMaxDataBytes {
		t.Fatalf("default MaxDataBytes = %d, want %d", def.MaxDataBytes, DefaultMaxDataBytes)
	}
}
