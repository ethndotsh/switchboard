package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ethndotsh/switchboard/internal/bundle"
)

// FileRegistry stores the standard registry layout on the local filesystem,
// for development, tests, and single-machine deployments.
type FileRegistry struct {
	root string
}

func NewFile(root string) (*FileRegistry, error) {
	if root == "" {
		return nil, fmt.Errorf("file registry requires a directory")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &FileRegistry{root: abs}, nil
}

func (r *FileRegistry) path(scope Scope, parts ...string) string {
	all := []string{r.root}
	if scope.Namespace != "" {
		all = append(all, "namespaces")
		all = append(all, strings.Split(scope.Namespace, "/")...)
	}
	all = append(all, parts...)
	return filepath.Join(all...)
}

func (r *FileRegistry) GetChannel(ctx context.Context, scope Scope, channel string) (bundle.ChannelPointer, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.ChannelPointer{}, err
	}
	data, err := os.ReadFile(r.path(scope, "channels", channel+".json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return bundle.ChannelPointer{}, fmt.Errorf("channel %s: %w", channel, ErrNotFound)
		}
		return bundle.ChannelPointer{}, err
	}
	return bundle.ParseChannelPointer(data)
}

func (r *FileRegistry) GetBundle(ctx context.Context, scope Scope, id string) (bundle.Bundle, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.Bundle{}, err
	}
	return assembleBundle(id, func(name string) ([]byte, bool, error) {
		data, err := os.ReadFile(r.path(scope, "bundles", id, name))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, false, nil
			}
			return nil, false, err
		}
		return data, true, nil
	})
}

func (r *FileRegistry) HasBundle(ctx context.Context, scope Scope, id string) (bool, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return false, err
	}
	if _, err := os.Stat(r.path(scope, "bundles", id, "descriptor.json")); err == nil {
		return true, nil
	}
	_, err := os.Stat(r.path(scope, "bundles", id, "checksum.txt"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (r *FileRegistry) PutBundle(ctx context.Context, scope Scope, b bundle.Bundle) error {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return err
	}
	files, err := bundleFiles(b)
	if err != nil {
		return err
	}
	dir := r.path(scope, "bundles", b.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range BundleFileNames {
		data, ok := files[name]
		if !ok {
			continue
		}
		if err := writeFileAtomic(filepath.Join(dir, name), data); err != nil {
			return err
		}
	}
	return nil
}

func (r *FileRegistry) PutChannel(ctx context.Context, scope Scope, pointer bundle.ChannelPointer) error {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return err
	}
	pointer.Namespace = scope.Namespace
	data, err := json.MarshalIndent(pointer, "", "  ")
	if err != nil {
		return err
	}
	path := r.path(scope, "channels", pointer.Channel+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

func (r *FileRegistry) GetRevision(ctx context.Context, scope Scope, channel string, generation uint64) (bundle.Revision, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.Revision{}, err
	}
	data, err := os.ReadFile(r.path(scope, "revisions", channel, revisionFileName(generation)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return bundle.Revision{}, fmt.Errorf("revision %d: %w", generation, ErrNotFound)
		}
		return bundle.Revision{}, err
	}
	return bundle.ParseRevision(data)
}

func (r *FileRegistry) PutRevision(ctx context.Context, scope Scope, rev bundle.Revision) error {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rev, "", "  ")
	if err != nil {
		return err
	}
	path := r.path(scope, "revisions", rev.Channel, revisionFileName(rev.Generation))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("generation %d: %w", rev.Generation, ErrRevisionExists)
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (r *FileRegistry) ListRevisions(ctx context.Context, scope Scope, channel string, limit int) ([]bundle.Revision, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return nil, err
	}
	dir := r.path(scope, "revisions", channel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var revisions []bundle.Revision
	for _, name := range names {
		if limit > 0 && len(revisions) >= limit {
			break
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		revision, err := bundle.ParseRevision(data)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, nil
}

func bundleFiles(b bundle.Bundle) (map[string][]byte, error) {
	if err := bundle.VerifyModuleChecksum(b.Module, b.Checksum); err != nil {
		return nil, err
	}
	manifestData, err := json.MarshalIndent(b.Manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		"module.wasm":   b.Module,
		"manifest.json": manifestData,
		"checksum.txt":  []byte(b.Checksum + "\n"),
	}
	if len(b.Tests) > 0 {
		files["tests.yaml"] = b.Tests
	}
	if len(b.DescriptorRaw) > 0 {
		files["descriptor.json"] = b.DescriptorRaw
	}
	return files, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
