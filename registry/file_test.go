package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ethndotsh/switchboard/internal/bundle"
)

// makeTestBundle builds a fully valid descriptor-backed bundle the same way
// the CLI does: content-addressed ID from the descriptor identity zone,
// manifest version set to the bundle ID after hashing.
func makeTestBundle(t *testing.T, name string, module, tests []byte) bundle.Bundle {
	t.Helper()
	identity := bundle.ManifestIdentity{
		Name:       name,
		ABI:        bundle.ABIVersion,
		Entrypoint: "handle",
		Language:   "go-tinygo",
	}
	artifacts := map[string][]byte{bundle.ArtifactModule: module}
	if len(tests) > 0 {
		artifacts[bundle.ArtifactTests] = tests
	}
	descriptor := bundle.NewDescriptor(identity, artifacts)
	id, err := descriptor.BundleID()
	if err != nil {
		t.Fatalf("BundleID: %v", err)
	}
	descriptorRaw, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	return bundle.Bundle{
		ID:     id,
		Module: module,
		Manifest: bundle.Manifest{
			Name:       name,
			Version:    id,
			ABI:        bundle.ABIVersion,
			Entrypoint: "handle",
			Language:   "go-tinygo",
		},
		Checksum:      bundle.ModuleChecksum(module),
		Tests:         tests,
		Descriptor:    descriptor,
		DescriptorRaw: descriptorRaw,
	}
}

func newFileRegistry(t *testing.T) (*FileRegistry, string) {
	t.Helper()
	root := t.TempDir()
	reg, err := NewFile(root)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	return reg, root
}

func TestFileBundleRoundTrip(t *testing.T) {
	ctx := context.Background()
	reg, root := newFileRegistry(t)
	module := []byte("\x00asm-module-bytes")
	tests := []byte("cases:\n  - name: ok\n")
	b := makeTestBundle(t, "checkout-rules", module, tests)

	if err := reg.PutBundle(ctx, Scope{}, b); err != nil {
		t.Fatalf("PutBundle: %v", err)
	}

	got, err := reg.GetBundle(ctx, Scope{}, b.ID)
	if err != nil {
		t.Fatalf("GetBundle: %v", err)
	}
	if got.ID != b.ID {
		t.Errorf("ID = %q, want %q", got.ID, b.ID)
	}
	if !bytes.Equal(got.Module, b.Module) {
		t.Errorf("Module mismatch")
	}
	if !bytes.Equal(got.Tests, b.Tests) {
		t.Errorf("Tests = %q, want %q", got.Tests, b.Tests)
	}
	if !bytes.Equal(got.DescriptorRaw, b.DescriptorRaw) {
		t.Errorf("DescriptorRaw mismatch")
	}
	if got.Manifest != b.Manifest {
		t.Errorf("Manifest = %+v, want %+v", got.Manifest, b.Manifest)
	}
	if got.Checksum != b.Checksum {
		t.Errorf("Checksum = %q, want %q", got.Checksum, b.Checksum)
	}
	if len(got.Descriptor.Artifacts) != 2 {
		t.Errorf("Descriptor artifacts = %d, want 2", len(got.Descriptor.Artifacts))
	}

	// Tampering with the stored module must fail verification.
	modulePath := filepath.Join(root, "bundles", b.ID, "module.wasm")
	if err := os.WriteFile(modulePath, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper module: %v", err)
	}
	if _, err := reg.GetBundle(ctx, Scope{}, b.ID); !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("GetBundle after tamper = %v, want bundle.ErrInvalid", err)
	}
}

func TestFileGetBundleLegacy(t *testing.T) {
	ctx := context.Background()
	reg, root := newFileRegistry(t)
	module := []byte("legacy module bytes")
	id := "legacy-1"
	manifest := bundle.Manifest{
		Name:       "legacy",
		Version:    id,
		ABI:        bundle.ABIVersion,
		Entrypoint: "handle",
		Language:   "go-tinygo",
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	dir := filepath.Join(root, "bundles", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files := map[string][]byte{
		"module.wasm":   module,
		"manifest.json": manifestData,
		"checksum.txt":  []byte(bundle.ModuleChecksum(module) + "\n"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got, err := reg.GetBundle(ctx, Scope{}, id)
	if err != nil {
		t.Fatalf("GetBundle legacy: %v", err)
	}
	if !bytes.Equal(got.Module, module) {
		t.Errorf("Module mismatch")
	}
	if got.Manifest != manifest {
		t.Errorf("Manifest = %+v, want %+v", got.Manifest, manifest)
	}
	if len(got.DescriptorRaw) != 0 {
		t.Errorf("legacy bundle should have no descriptor, got %q", got.DescriptorRaw)
	}

	has, err := reg.HasBundle(ctx, Scope{}, id)
	if err != nil {
		t.Fatalf("HasBundle legacy: %v", err)
	}
	if !has {
		t.Errorf("HasBundle legacy = false, want true")
	}
}

func TestFileGetBundleIDMismatch(t *testing.T) {
	ctx := context.Background()
	reg, _ := newFileRegistry(t)
	b := makeTestBundle(t, "mismatch", []byte("module bytes"), nil)
	// Store the bundle under a directory name that is not its
	// descriptor-derived ID.
	b.ID = "sha256-0000000000000000000000000000000000000000000000000000000000000000"
	if err := reg.PutBundle(ctx, Scope{}, b); err != nil {
		t.Fatalf("PutBundle: %v", err)
	}
	if _, err := reg.GetBundle(ctx, Scope{}, b.ID); !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("GetBundle = %v, want bundle.ErrInvalid", err)
	}
}

func TestFileHasBundle(t *testing.T) {
	ctx := context.Background()
	reg, _ := newFileRegistry(t)
	b := makeTestBundle(t, "present", []byte("module"), nil)
	if err := reg.PutBundle(ctx, Scope{}, b); err != nil {
		t.Fatalf("PutBundle: %v", err)
	}
	has, err := reg.HasBundle(ctx, Scope{}, b.ID)
	if err != nil {
		t.Fatalf("HasBundle: %v", err)
	}
	if !has {
		t.Errorf("HasBundle(%s) = false, want true", b.ID)
	}
	has, err = reg.HasBundle(ctx, Scope{}, "sha256-missing")
	if err != nil {
		t.Fatalf("HasBundle missing: %v", err)
	}
	if has {
		t.Errorf("HasBundle(missing) = true, want false")
	}
}

func TestFileChannelRoundTrip(t *testing.T) {
	ctx := context.Background()
	reg, root := newFileRegistry(t)
	pointer := bundle.ChannelPointer{
		Channel:    "prod",
		BundleID:   "sha256-abc",
		Checksum:   "sha256:def",
		Generation: 4,
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}

	cases := []struct {
		name      string
		namespace string
		wantPath  string
	}{
		{"global", "", filepath.Join(root, "channels", "prod.json")},
		{"namespaced", "customer-a/edge", filepath.Join(root, "namespaces", "customer-a", "edge", "channels", "prod.json")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope := Scope{Namespace: tc.namespace}
			if err := reg.PutChannel(ctx, scope, pointer); err != nil {
				t.Fatalf("PutChannel: %v", err)
			}
			if _, err := os.Stat(tc.wantPath); err != nil {
				t.Fatalf("expected channel file at %s: %v", tc.wantPath, err)
			}
			got, err := reg.GetChannel(ctx, scope, "prod")
			if err != nil {
				t.Fatalf("GetChannel: %v", err)
			}
			if got.Namespace != tc.namespace {
				t.Errorf("Namespace = %q, want %q", got.Namespace, tc.namespace)
			}
			if got.Channel != pointer.Channel || got.BundleID != pointer.BundleID ||
				got.Checksum != pointer.Checksum || got.Generation != pointer.Generation {
				t.Errorf("pointer = %+v, want %+v", got, pointer)
			}
			if !got.CreatedAt.Equal(pointer.CreatedAt) {
				t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, pointer.CreatedAt)
			}
		})
	}

	// A namespaced pointer must not leak into a different scope.
	if _, err := reg.GetChannel(ctx, Scope{Namespace: "customer-b"}, "prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannel other namespace = %v, want ErrNotFound", err)
	}
}

func TestFileGetChannelMissing(t *testing.T) {
	ctx := context.Background()
	reg, _ := newFileRegistry(t)
	if _, err := reg.GetChannel(ctx, Scope{}, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannel = %v, want ErrNotFound", err)
	}
}

func testRevision(channel string, generation uint64) bundle.Revision {
	return bundle.Revision{
		Schema:     bundle.RevisionSchema,
		Channel:    channel,
		Generation: generation,
		BundleID:   "sha256-abc",
		DeployedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func TestFileRevisions(t *testing.T) {
	ctx := context.Background()
	reg, _ := newFileRegistry(t)
	scope := Scope{}

	for _, generation := range []uint64{1, 2, 3} {
		if err := reg.PutRevision(ctx, scope, testRevision("prod", generation)); err != nil {
			t.Fatalf("PutRevision(%d): %v", generation, err)
		}
	}

	got, err := reg.GetRevision(ctx, scope, "prod", 2)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if got.Generation != 2 || got.Channel != "prod" {
		t.Errorf("GetRevision = %+v", got)
	}
	if _, err := reg.GetRevision(ctx, scope, "prod", 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRevision(9) = %v, want ErrNotFound", err)
	}

	revisions, err := reg.ListRevisions(ctx, scope, "prod", 0)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revisions) != 3 {
		t.Fatalf("ListRevisions len = %d, want 3", len(revisions))
	}
	for i, want := range []uint64{3, 2, 1} {
		if revisions[i].Generation != want {
			t.Errorf("revisions[%d].Generation = %d, want %d", i, revisions[i].Generation, want)
		}
	}

	limited, err := reg.ListRevisions(ctx, scope, "prod", 2)
	if err != nil {
		t.Fatalf("ListRevisions limit: %v", err)
	}
	if len(limited) != 2 || limited[0].Generation != 3 || limited[1].Generation != 2 {
		t.Errorf("limited = %+v, want generations [3 2]", limited)
	}

	empty, err := reg.ListRevisions(ctx, scope, "unknown", 0)
	if err != nil {
		t.Fatalf("ListRevisions unknown channel: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ListRevisions unknown channel = %+v, want empty", empty)
	}

	if err := reg.PutRevision(ctx, scope, testRevision("prod", 2)); !errors.Is(err, ErrRevisionExists) {
		t.Fatalf("duplicate PutRevision = %v, want ErrRevisionExists", err)
	}
}

func TestFilePutRevisionConcurrent(t *testing.T) {
	ctx := context.Background()
	reg, _ := newFileRegistry(t)
	scope := Scope{}

	const writers = 10
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = reg.PutRevision(ctx, scope, testRevision("prod", 7))
		}(i)
	}
	wg.Wait()

	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrRevisionExists):
		default:
			t.Errorf("writer %d: unexpected error %v", i, err)
		}
	}
	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1", wins)
	}
}
