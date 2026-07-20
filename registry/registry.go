package registry

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ethndotsh/switchboard/internal/bundle"
)

var safePathSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var (
	ErrNotFound       = errors.New("not found")
	ErrReadOnly       = errors.New("registry is read-only")
	ErrRevisionExists = errors.New("revision generation already exists")
)

type Scope struct {
	Namespace string
}

type Registry interface {
	GetChannel(ctx context.Context, scope Scope, channel string) (bundle.ChannelPointer, error)
	GetBundle(ctx context.Context, scope Scope, id string) (bundle.Bundle, error)
	HasBundle(ctx context.Context, scope Scope, id string) (bool, error)
	PutBundle(ctx context.Context, scope Scope, b bundle.Bundle) error
	PutChannel(ctx context.Context, scope Scope, pointer bundle.ChannelPointer) error
	GetRevision(ctx context.Context, scope Scope, channel string, generation uint64) (bundle.Revision, error)
	PutRevision(ctx context.Context, scope Scope, rev bundle.Revision) error
	ListRevisions(ctx context.Context, scope Scope, channel string, limit int) ([]bundle.Revision, error)
}

func ValidateNamespace(namespace string) error {
	if namespace == "" {
		return nil
	}
	if strings.HasPrefix(namespace, "/") || strings.HasSuffix(namespace, "/") {
		return fmt.Errorf("namespace %q must not start or end with /", namespace)
	}
	for _, segment := range strings.Split(namespace, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("namespace %q contains invalid segment %q", namespace, segment)
		}
		if !safePathSegment.MatchString(segment) {
			return fmt.Errorf("namespace %q contains unsafe segment %q", namespace, segment)
		}
	}
	return nil
}

func revisionFileName(generation uint64) string {
	return fmt.Sprintf("%010d.json", generation)
}

// BundleFileNames are the fixed objects under bundles/{id}/, in upload order;
// descriptor.json goes last so its presence marks a complete bundle. Data
// artifacts are named dynamically, so use bundleWriteOrder for the full set.
var BundleFileNames = []string{"module.wasm", "manifest.json", "tests.yaml", "checksum.txt", "descriptor.json"}

// bundleWriteOrder returns every file name in files ordered so descriptor.json
// is written last, marking the bundle complete only once every other object
// (including data artifacts) is durably in place.
func bundleWriteOrder(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	hasDescriptor := false
	for name := range files {
		if name == "descriptor.json" {
			hasDescriptor = true
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if hasDescriptor {
		names = append(names, "descriptor.json")
	}
	return names
}

type fetchFunc func(name string) ([]byte, bool, error)

// assembleBundle builds and verifies a bundle from per-file reads; every
// registry implementation shares it so verification is identical everywhere.
func assembleBundle(id string, fetch fetchFunc) (bundle.Bundle, error) {
	module, ok, err := fetch("module.wasm")
	if err != nil {
		return bundle.Bundle{}, err
	}
	if !ok {
		return bundle.Bundle{}, fmt.Errorf("bundle %s: module.wasm: %w", id, ErrNotFound)
	}
	manifestData, ok, err := fetch("manifest.json")
	if err != nil {
		return bundle.Bundle{}, err
	}
	if !ok {
		return bundle.Bundle{}, fmt.Errorf("bundle %s: manifest.json: %w", id, ErrNotFound)
	}
	manifest, err := bundle.ParseManifest(manifestData)
	if err != nil {
		return bundle.Bundle{}, err
	}

	b := bundle.Bundle{
		ID:       id,
		Module:   module,
		Manifest: manifest,
		Checksum: bundle.ModuleChecksum(module),
	}

	descriptorRaw, hasDescriptor, err := fetch("descriptor.json")
	if err != nil {
		return bundle.Bundle{}, err
	}
	checksumData, hasChecksum, err := fetch("checksum.txt")
	if err != nil {
		return bundle.Bundle{}, err
	}
	if hasChecksum {
		if err := bundle.VerifyModuleChecksum(module, strings.TrimSpace(string(checksumData))); err != nil {
			return bundle.Bundle{}, err
		}
	}

	if !hasDescriptor {
		if !hasChecksum {
			return bundle.Bundle{}, fmt.Errorf("%w: bundle %s has neither descriptor.json nor checksum.txt", bundle.ErrInvalid, id)
		}
		return b, nil
	}

	descriptor, err := bundle.ParseDescriptor(descriptorRaw)
	if err != nil {
		return bundle.Bundle{}, err
	}
	files := map[string][]byte{bundle.ArtifactModule: module}
	for name := range descriptor.Artifacts {
		if name == bundle.ArtifactModule {
			continue
		}
		data, ok, err := fetch(name)
		if err != nil {
			return bundle.Bundle{}, err
		}
		if !ok {
			continue
		}
		files[name] = data
		switch {
		case name == bundle.ArtifactTests:
			b.Tests = data
		case bundle.IsDataArtifact(name):
			if b.Data == nil {
				b.Data = map[string][]byte{}
			}
			b.Data[name] = data
		}
	}
	if err := descriptor.Verify(files); err != nil {
		return bundle.Bundle{}, err
	}
	derivedID, err := descriptor.BundleID()
	if err != nil {
		return bundle.Bundle{}, err
	}
	if derivedID != id {
		return bundle.Bundle{}, fmt.Errorf("%w: bundle id %s does not match descriptor-derived id %s", bundle.ErrInvalid, id, derivedID)
	}
	b.Descriptor = descriptor
	b.DescriptorRaw = descriptorRaw
	return b, nil
}

// listRevisionsByWalk walks generations backwards from the channel pointer,
// for registries that cannot enumerate objects.
func listRevisionsByWalk(ctx context.Context, reg Registry, scope Scope, channel string, limit int) ([]bundle.Revision, error) {
	pointer, err := reg.GetChannel(ctx, scope, channel)
	if err != nil {
		return nil, err
	}
	if pointer.Generation == 0 {
		return nil, nil
	}
	var revisions []bundle.Revision
	for generation := pointer.Generation; generation > 0; generation-- {
		if limit > 0 && len(revisions) >= limit {
			break
		}
		revision, err := reg.GetRevision(ctx, scope, channel, generation)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				break
			}
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, nil
}
