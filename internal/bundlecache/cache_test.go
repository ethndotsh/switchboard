package bundlecache

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethndotsh/switchboard/internal/bundle"
)

// makeCacheBundle builds a valid descriptor-backed bundle. The manifest ABI
// must be bundle.ABIVersion or Load's ParseManifest rejects the cache entry.
func makeCacheBundle(t *testing.T, name string, module, tests []byte) bundle.Bundle {
	return makeCacheBundleWithData(t, name, module, tests, nil)
}

func makeCacheBundleWithData(t *testing.T, name string, module, tests []byte, data map[string][]byte) bundle.Bundle {
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
	for dataName, value := range data {
		artifacts[dataName] = value
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
		Data:          data,
		Descriptor:    descriptor,
		DescriptorRaw: descriptorRaw,
	}
}

func TestStoreLoadData(t *testing.T) {
	cache := New(t.TempDir())
	data := map[string][]byte{"data/allowlist.txt": []byte("203.0.113.7\n")}
	b := makeCacheBundleWithData(t, "gate", []byte("\x00asm"), nil, data)
	if err := cache.Store("", "prod", b, testMetadata(b, "", "prod")); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, _, err := cache.Load("", "prod")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got.Data["data/allowlist.txt"]) != "203.0.113.7\n" {
		t.Fatalf("data round-trip mismatch: %q", got.Data["data/allowlist.txt"])
	}
}

func testMetadata(b bundle.Bundle, namespace, channel string) Metadata {
	return Metadata{
		BundleID:    b.ID,
		Checksum:    b.Checksum,
		Namespace:   namespace,
		Channel:     channel,
		ActivatedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func TestStoreLoadRoundTrip(t *testing.T) {
	cache := New(t.TempDir())
	b := makeCacheBundle(t, "cached-rules", []byte("cached module bytes"), []byte("cases: []\n"))
	meta := testMetadata(b, "", "prod")

	if err := cache.Store("", "prod", b, meta); err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, gotMeta, err := cache.Load("", "prod")
	if err != nil {
		t.Fatalf("Load: %v", err)
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
	if gotMeta.BundleID != meta.BundleID || gotMeta.Checksum != meta.Checksum ||
		gotMeta.Namespace != meta.Namespace || gotMeta.Channel != meta.Channel {
		t.Errorf("metadata = %+v, want %+v", gotMeta, meta)
	}
	if !gotMeta.ActivatedAt.Equal(meta.ActivatedAt) {
		t.Errorf("ActivatedAt = %v, want %v", gotMeta.ActivatedAt, meta.ActivatedAt)
	}
}

func TestLoadEmptyCache(t *testing.T) {
	cache := New(t.TempDir())
	if _, _, err := cache.Load("", "prod"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load on empty cache = %v, want fs.ErrNotExist", err)
	}
}

func TestLoadCorruptModule(t *testing.T) {
	dir := t.TempDir()
	cache := New(dir)
	b := makeCacheBundle(t, "corrupt-me", []byte("original module"), nil)
	if err := cache.Store("", "prod", b, testMetadata(b, "", "prod")); err != nil {
		t.Fatalf("Store: %v", err)
	}

	modulePath := filepath.Join(dir, "bundles", "_default", "prod", "current", "module.wasm")
	if err := os.WriteFile(modulePath, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("tamper module: %v", err)
	}
	if _, _, err := cache.Load("", "prod"); !errors.Is(err, bundle.ErrInvalid) {
		t.Fatalf("Load after tamper = %v, want bundle.ErrInvalid", err)
	}
}

func TestStoreReplacesCurrent(t *testing.T) {
	dir := t.TempDir()
	cache := New(dir)

	first := makeCacheBundle(t, "rules", []byte("first module"), []byte("cases: []\n"))
	if err := cache.Store("", "prod", first, testMetadata(first, "", "prod")); err != nil {
		t.Fatalf("Store first: %v", err)
	}

	second := makeCacheBundle(t, "rules", []byte("second module, different bytes"), nil)
	if err := cache.Store("", "prod", second, testMetadata(second, "", "prod")); err != nil {
		t.Fatalf("Store second: %v", err)
	}

	got, gotMeta, err := cache.Load("", "prod")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID != second.ID {
		t.Errorf("ID = %q, want %q (second store)", got.ID, second.ID)
	}
	if !bytes.Equal(got.Module, second.Module) {
		t.Errorf("Module = %q, want second store's module", got.Module)
	}
	if gotMeta.BundleID != second.ID {
		t.Errorf("meta BundleID = %q, want %q", gotMeta.BundleID, second.ID)
	}
}

func TestStoreLoadNamespaced(t *testing.T) {
	dir := t.TempDir()
	cache := New(dir)
	b := makeCacheBundle(t, "ns-rules", []byte("namespaced module"), nil)
	namespace := "customer-a/edge"
	if err := cache.Store(namespace, "prod", b, testMetadata(b, namespace, "prod")); err != nil {
		t.Fatalf("Store: %v", err)
	}

	wantDir := filepath.Join(dir, "bundles", "customer-a", "edge", "prod", "current")
	if _, err := os.Stat(filepath.Join(wantDir, "module.wasm")); err != nil {
		t.Fatalf("expected namespaced cache at %s: %v", wantDir, err)
	}

	got, gotMeta, err := cache.Load(namespace, "prod")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID != b.ID {
		t.Errorf("ID = %q, want %q", got.ID, b.ID)
	}
	if gotMeta.Namespace != namespace {
		t.Errorf("meta Namespace = %q, want %q", gotMeta.Namespace, namespace)
	}

	// The namespaced entry is invisible to the default scope, which lives
	// under _default.
	if _, _, err := cache.Load("", "prod"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load default scope = %v, want fs.ErrNotExist", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bundles", "customer-a", "edge", "prod")); err != nil {
		t.Fatalf("namespaced dir: %v", err)
	}
}

func TestStoreDefaultNamespaceDir(t *testing.T) {
	dir := t.TempDir()
	cache := New(dir)
	b := makeCacheBundle(t, "default-rules", []byte("default module"), nil)
	if err := cache.Store("", "prod", b, testMetadata(b, "", "prod")); err != nil {
		t.Fatalf("Store: %v", err)
	}
	wantPath := filepath.Join(dir, "bundles", "_default", "prod", "current", "module.wasm")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected default-namespace cache at %s: %v", wantPath, err)
	}
}
