package bundle

import (
	"errors"
	"testing"
)

func TestVerifyModuleChecksum(t *testing.T) {
	module := []byte("hello")
	checksum := ModuleChecksum(module)
	if err := VerifyModuleChecksum(module, checksum); err != nil {
		t.Fatalf("expected checksum to pass: %v", err)
	}
	err := VerifyModuleChecksum(module, "sha256:nope")
	if err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("checksum mismatch should be ErrInvalid, got %v", err)
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
	if pointer.Generation != 0 || pointer.DescriptorDigest != "" {
		t.Fatalf("legacy pointer should tolerate missing new fields: %#v", pointer)
	}

	withGeneration, err := ParseChannelPointer([]byte(`{"channel":"prod","bundle_id":"v1","checksum":"sha256:abc","generation":42,"descriptor_digest":"sha256:def","created_at":"2026-06-19T12:00:00Z"}`))
	if err != nil {
		t.Fatalf("parse channel pointer: %v", err)
	}
	if withGeneration.Generation != 42 || withGeneration.DescriptorDigest != "sha256:def" {
		t.Fatalf("pointer = %#v", withGeneration)
	}
}

func TestParseManifestRequiresABIV3(t *testing.T) {
	if _, err := ParseManifest([]byte(`{"name":"rules","version":"v3","abi_version":"switchboard/v4","entrypoint":"handle"}`)); err != nil {
		t.Fatalf("parse v3 manifest: %v", err)
	}
	for _, old := range []string{"switchboard/v0", "switchboard/v1", "switchboard/v2"} {
		_, err := ParseManifest([]byte(`{"name":"rules","version":"x","abi_version":"` + old + `","entrypoint":"handle"}`))
		if err == nil {
			t.Fatalf("expected %s manifest to be rejected", old)
		}
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("abi rejection should be ErrInvalid, got %v", err)
		}
	}
}

func TestParseRevision(t *testing.T) {
	revision, err := ParseRevision([]byte(`{"schema":"switchboard.revision/v1","channel":"prod","generation":7,"bundle_id":"sha256-abc","deployed_at":"2026-06-19T12:00:00Z"}`))
	if err != nil {
		t.Fatalf("parse revision: %v", err)
	}
	if revision.Generation != 7 || revision.BundleID != "sha256-abc" {
		t.Fatalf("revision = %#v", revision)
	}
	for _, invalid := range []string{
		`{"schema":"switchboard.revision/v1","generation":7,"bundle_id":"x","channel":""}`,
		`{"schema":"switchboard.revision/v1","channel":"prod","bundle_id":"x"}`,
		`{"schema":"switchboard.revision/v1","channel":"prod","generation":7}`,
	} {
		if _, err := ParseRevision([]byte(invalid)); err == nil {
			t.Fatalf("expected revision %s to be rejected", invalid)
		}
	}
}
