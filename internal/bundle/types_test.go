package bundle

import "testing"

func TestVerifyModuleChecksum(t *testing.T) {
	module := []byte("hello")
	checksum := ModuleChecksum(module)
	if err := VerifyModuleChecksum(module, checksum); err != nil {
		t.Fatalf("expected checksum to pass: %v", err)
	}
	if err := VerifyModuleChecksum(module, "sha256:nope"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestParseChannelPointer(t *testing.T) {
	pointer, err := ParseChannelPointer([]byte(`{"channel":"prod","bundle_id":"v1","checksum":"sha256:abc","created_at":"2026-06-19T12:00:00Z"}`))
	if err != nil {
		t.Fatalf("parse channel pointer: %v", err)
	}
	if pointer.BundleID != "v1" {
		t.Fatalf("unexpected bundle id %q", pointer.BundleID)
	}
}
