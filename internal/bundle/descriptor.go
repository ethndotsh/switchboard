package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const DescriptorSchema = "switchboard.descriptor/v1"

const (
	ArtifactModule = "module.wasm"
	ArtifactTests  = "tests.yaml"
	// DataPrefix marks descriptor artifacts that are read-only data files
	// bundled with the rule and exposed to the guest via the data ABI.
	DataPrefix = "data/"
)

// IsDataArtifact reports whether an artifact name is a bundled data file.
func IsDataArtifact(name string) bool {
	return strings.HasPrefix(name, DataPrefix)
}

type ArtifactRef struct {
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type Provenance struct {
	BuiltAt      time.Time `json:"built_at,omitempty"`
	Builder      string    `json:"builder,omitempty"`
	SourceCommit string    `json:"source_commit,omitempty"`
	SourceDirty  bool      `json:"source_dirty,omitempty"`
	CIRun        string    `json:"ci_run,omitempty"`
}

// ManifestIdentity excludes the manifest version field because it is set to
// the bundle ID itself after hashing.
type ManifestIdentity struct {
	Name       string `json:"name"`
	ABI        string `json:"abi"`
	Entrypoint string `json:"entrypoint"`
	Language   string `json:"language"`
}

// Descriptor's identity zone (schema, abi, manifest, artifacts) derives the
// bundle ID; provenance and signatures are annotations that never change it.
type Descriptor struct {
	Schema     string                 `json:"schema"`
	ABI        string                 `json:"abi"`
	Manifest   ManifestIdentity       `json:"manifest"`
	Artifacts  map[string]ArtifactRef `json:"artifacts"`
	Provenance Provenance             `json:"provenance,omitempty"`
	// Signatures is reserved for future bundle signing; never verified yet.
	Signatures []json.RawMessage `json:"signatures"`
}

type descriptorIdentity struct {
	Schema    string                 `json:"schema"`
	ABI       string                 `json:"abi"`
	Manifest  ManifestIdentity       `json:"manifest"`
	Artifacts map[string]ArtifactRef `json:"artifacts"`
}

func ArtifactDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func DescriptorDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (d Descriptor) BundleID() (string, error) {
	if d.Schema != DescriptorSchema {
		return "", fmt.Errorf("%w: unsupported descriptor schema %q", ErrInvalid, d.Schema)
	}
	canonical, err := CanonicalJSON(descriptorIdentity{
		Schema:    d.Schema,
		ABI:       d.ABI,
		Manifest:  d.Manifest,
		Artifacts: d.Artifacts,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256-" + hex.EncodeToString(sum[:]), nil
}

func ParseDescriptor(data []byte) (Descriptor, error) {
	var descriptor Descriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return Descriptor{}, fmt.Errorf("%w: descriptor: %v", ErrInvalid, err)
	}
	if descriptor.Schema != DescriptorSchema {
		return Descriptor{}, fmt.Errorf("%w: unsupported descriptor schema %q (expected %s)", ErrInvalid, descriptor.Schema, DescriptorSchema)
	}
	if _, ok := descriptor.Artifacts[ArtifactModule]; !ok {
		return Descriptor{}, fmt.Errorf("%w: descriptor missing %s artifact", ErrInvalid, ArtifactModule)
	}
	if descriptor.Manifest.Name == "" || descriptor.Manifest.Entrypoint == "" {
		return Descriptor{}, fmt.Errorf("%w: descriptor manifest identity incomplete", ErrInvalid)
	}
	return descriptor, nil
}

func (d Descriptor) Verify(files map[string][]byte) error {
	for name, ref := range d.Artifacts {
		data, ok := files[name]
		if !ok {
			return fmt.Errorf("%w: artifact %s declared by descriptor is missing", ErrInvalid, name)
		}
		if int64(len(data)) != ref.Size {
			return fmt.Errorf("%w: artifact %s size mismatch: expected %d got %d", ErrInvalid, name, ref.Size, len(data))
		}
		if actual := ArtifactDigest(data); actual != ref.Digest {
			return fmt.Errorf("%w: artifact %s digest mismatch: expected %s got %s", ErrInvalid, name, ref.Digest, actual)
		}
	}
	return nil
}

func NewDescriptor(identity ManifestIdentity, artifacts map[string][]byte) Descriptor {
	refs := make(map[string]ArtifactRef, len(artifacts))
	for name, data := range artifacts {
		refs[name] = ArtifactRef{Digest: ArtifactDigest(data), Size: int64(len(data))}
	}
	return Descriptor{
		Schema:     DescriptorSchema,
		ABI:        identity.ABI,
		Manifest:   identity,
		Artifacts:  refs,
		Signatures: []json.RawMessage{},
	}
}
