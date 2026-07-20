package bundle

import (
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"
)

func testIdentity() ManifestIdentity {
	return ManifestIdentity{Name: "rules", ABI: ABIVersion, Entrypoint: "handle", Language: "go-tinygo"}
}

func TestCanonicalJSONIsDeterministic(t *testing.T) {
	a, err := CanonicalJSON(map[string]any{"b": 2, "a": 1, "nested": map[string]any{"y": true, "x": false}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalJSON(map[string]any{"nested": map[string]any{"x": false, "y": true}, "a": 1, "b": 2})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("canonical JSON differs: %s vs %s", a, b)
	}
	if string(a) != `{"a":1,"b":2,"nested":{"x":false,"y":true}}` {
		t.Fatalf("canonical form = %s", a)
	}
}

// TestBundleIDGoldenValue locks the hashing contract: schema + abi + manifest
// identity + artifact digests, canonical JSON, sha256. Changing any part of
// that derivation breaks every deployed bundle ID, so this test must only
// ever change alongside a descriptor schema bump.
func TestBundleIDGoldenValue(t *testing.T) {
	descriptor := NewDescriptor(testIdentity(), map[string][]byte{
		ArtifactModule: []byte("fake module bytes"),
	})
	id, err := descriptor.BundleID()
	if err != nil {
		t.Fatal(err)
	}
	// Locked to ABI switchboard/v4; the v3→v4 bump deliberately re-derives it.
	const golden = "sha256-c88790f29c3bcbf35d493e4af73796b1feac2851efa95f95c5ac1b756d34a68e"
	if id != golden {
		t.Fatalf("bundle id = %s, want %s (hashing contract changed!)", id, golden)
	}
}

func TestBundleIDIgnoresProvenanceAndSignatures(t *testing.T) {
	base := NewDescriptor(testIdentity(), map[string][]byte{ArtifactModule: []byte("module")})
	baseID, err := base.BundleID()
	if err != nil {
		t.Fatal(err)
	}

	annotated := base
	annotated.Provenance = Provenance{
		BuiltAt:      time.Now(),
		Builder:      "switchboard/0.1.0",
		SourceCommit: "deadbeef",
		SourceDirty:  true,
		CIRun:        "12345",
	}
	annotated.Signatures = []json.RawMessage{json.RawMessage(`{"sig":"zzz"}`)}
	annotatedID, err := annotated.BundleID()
	if err != nil {
		t.Fatal(err)
	}
	if annotatedID != baseID {
		t.Fatal("provenance/signature changes must not change the bundle ID")
	}
}

func TestBundleIDChangesWithContent(t *testing.T) {
	base := NewDescriptor(testIdentity(), map[string][]byte{ArtifactModule: []byte("module-a")})
	baseID, _ := base.BundleID()

	differentModule := NewDescriptor(testIdentity(), map[string][]byte{ArtifactModule: []byte("module-b")})
	differentModuleID, _ := differentModule.BundleID()
	if differentModuleID == baseID {
		t.Fatal("module change must change the bundle ID")
	}

	withTests := NewDescriptor(testIdentity(), map[string][]byte{
		ArtifactModule: []byte("module-a"),
		ArtifactTests:  []byte("cases: []"),
	})
	withTestsID, _ := withTests.BundleID()
	if withTestsID == baseID {
		t.Fatal("adding tests must change the bundle ID")
	}

	renamed := testIdentity()
	renamed.Name = "other"
	renamedDescriptor := NewDescriptor(renamed, map[string][]byte{ArtifactModule: []byte("module-a")})
	renamedID, _ := renamedDescriptor.BundleID()
	if renamedID == baseID {
		t.Fatal("manifest identity change must change the bundle ID")
	}
}

func TestBundleIDIsKeySafe(t *testing.T) {
	descriptor := NewDescriptor(testIdentity(), map[string][]byte{ArtifactModule: []byte("x")})
	id, err := descriptor.BundleID()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^sha256-[0-9a-f]{64}$`).MatchString(id) {
		t.Fatalf("bundle id %q is not key-safe", id)
	}
}

func TestDescriptorVerify(t *testing.T) {
	module := []byte("module bytes")
	tests := []byte("cases: []")
	descriptor := NewDescriptor(testIdentity(), map[string][]byte{
		ArtifactModule: module,
		ArtifactTests:  tests,
	})

	ok := map[string][]byte{ArtifactModule: module, ArtifactTests: tests}
	if err := descriptor.Verify(ok); err != nil {
		t.Fatalf("verify: %v", err)
	}

	tampered := map[string][]byte{ArtifactModule: []byte("evil"), ArtifactTests: tests}
	if err := descriptor.Verify(tampered); err == nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected digest mismatch, got %v", err)
	}

	truncated := map[string][]byte{ArtifactModule: module[:4], ArtifactTests: tests}
	if err := descriptor.Verify(truncated); err == nil {
		t.Fatal("expected size mismatch")
	}

	missing := map[string][]byte{ArtifactModule: module}
	if err := descriptor.Verify(missing); err == nil {
		t.Fatal("expected missing artifact error")
	}
}

func TestParseDescriptorRoundTrip(t *testing.T) {
	descriptor := NewDescriptor(testIdentity(), map[string][]byte{ArtifactModule: []byte("module")})
	descriptor.Provenance = Provenance{Builder: "switchboard/0.1.0"}
	raw, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsedID, err := parsed.BundleID()
	if err != nil {
		t.Fatal(err)
	}
	originalID, _ := descriptor.BundleID()
	if parsedID != originalID {
		t.Fatal("round-tripped descriptor produced a different bundle ID")
	}
	if DescriptorDigest(raw) == "" {
		t.Fatal("descriptor digest empty")
	}

	if _, err := ParseDescriptor([]byte(`{"schema":"bogus/v9"}`)); err == nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected schema rejection, got %v", err)
	}
	if _, err := ParseDescriptor([]byte(`{"schema":"switchboard.descriptor/v1","abi":"switchboard/v3","manifest":{"name":"x","entrypoint":"handle"},"artifacts":{}}`)); err == nil {
		t.Fatal("expected missing module.wasm rejection")
	}
}
