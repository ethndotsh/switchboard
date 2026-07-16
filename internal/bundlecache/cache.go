// Package bundlecache persists the last activated bundle per channel so a
// proxy can bootstrap after restart without reaching the registry.
package bundlecache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/ethndotsh/switchboard/registry"
)

type Metadata struct {
	BundleID    string    `json:"bundle_id"`
	Checksum    string    `json:"checksum"`
	Namespace   string    `json:"namespace,omitempty"`
	Channel     string    `json:"channel"`
	ActivatedAt time.Time `json:"activated_at"`
}

type Cache struct {
	dir string
}

func New(dir string) *Cache {
	return &Cache{dir: dir}
}

func WazeroCacheDir(dir string) string {
	return filepath.Join(dir, "wazero")
}

func (c *Cache) channelDir(namespace, channel string) (string, error) {
	if err := registry.ValidateNamespace(namespace); err != nil {
		return "", err
	}
	segment := "_default"
	if namespace != "" {
		segment = namespace
	}
	return filepath.Join(c.dir, "bundles", filepath.FromSlash(segment), channel), nil
}

// Store writes to a temp sibling directory, fsyncs, and replaces "current"
// with a rename, so a failed store never leaves a partial "current".
func (c *Cache) Store(namespace, channel string, b bundle.Bundle, meta Metadata) error {
	channelDir, err := c.channelDir(namespace, channel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(channelDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(channelDir, "tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	manifestJSON, err := json.MarshalIndent(b.Manifest, "", "  ")
	if err != nil {
		return err
	}
	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	files := map[string][]byte{
		"module.wasm":     b.Module,
		"manifest.json":   manifestJSON,
		"checksum.txt":    []byte(b.Checksum + "\n"),
		"activation.json": metaJSON,
	}
	if len(b.DescriptorRaw) > 0 {
		files["descriptor.json"] = b.DescriptorRaw
	}
	if len(b.Tests) > 0 {
		files["tests.yaml"] = b.Tests
	}
	for name, data := range files {
		if err := writeAndSync(filepath.Join(tmp, name), data); err != nil {
			return err
		}
	}
	if err := syncDir(tmp); err != nil {
		return err
	}

	current := filepath.Join(channelDir, "current")
	old := filepath.Join(channelDir, fmt.Sprintf("old-%d", time.Now().UnixNano()))
	if _, err := os.Stat(current); err == nil {
		if err := os.Rename(current, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, current); err != nil {
		return err
	}
	_ = syncDir(channelDir)
	_ = os.RemoveAll(old)
	return nil
}

// Load fully verifies the cached bundle: missing → fs.ErrNotExist, corrupt
// → bundle.ErrInvalid.
func (c *Cache) Load(namespace, channel string) (bundle.Bundle, Metadata, error) {
	channelDir, err := c.channelDir(namespace, channel)
	if err != nil {
		return bundle.Bundle{}, Metadata{}, err
	}
	current := filepath.Join(channelDir, "current")

	module, err := os.ReadFile(filepath.Join(current, "module.wasm"))
	if err != nil {
		return bundle.Bundle{}, Metadata{}, err
	}
	manifestData, err := os.ReadFile(filepath.Join(current, "manifest.json"))
	if err != nil {
		return bundle.Bundle{}, Metadata{}, err
	}
	manifest, err := bundle.ParseManifest(manifestData)
	if err != nil {
		return bundle.Bundle{}, Metadata{}, err
	}
	checksumData, err := os.ReadFile(filepath.Join(current, "checksum.txt"))
	if err != nil {
		return bundle.Bundle{}, Metadata{}, err
	}
	if err := bundle.VerifyModuleChecksum(module, string(checksumData)); err != nil {
		return bundle.Bundle{}, Metadata{}, err
	}

	b := bundle.Bundle{
		ID:       manifest.Version,
		Module:   module,
		Manifest: manifest,
		Checksum: bundle.ModuleChecksum(module),
	}
	if descriptorRaw, err := os.ReadFile(filepath.Join(current, "descriptor.json")); err == nil {
		descriptor, err := bundle.ParseDescriptor(descriptorRaw)
		if err != nil {
			return bundle.Bundle{}, Metadata{}, err
		}
		b.Descriptor = descriptor
		b.DescriptorRaw = descriptorRaw
	}
	if tests, err := os.ReadFile(filepath.Join(current, "tests.yaml")); err == nil {
		b.Tests = tests
	}
	files := map[string][]byte{
		"module.wasm": module,
	}
	if len(b.Tests) > 0 {
		files["tests.yaml"] = b.Tests
	}
	if len(b.DescriptorRaw) > 0 {
		if err := b.Descriptor.Verify(files); err != nil {
			return bundle.Bundle{}, Metadata{}, err
		}
	}

	var meta Metadata
	metaData, err := os.ReadFile(filepath.Join(current, "activation.json"))
	if err == nil {
		_ = json.Unmarshal(metaData, &meta)
	}
	return b, meta, nil
}

func writeAndSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	err = d.Sync()
	_ = d.Close()
	return err
}
