package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const ABIVersion = "switchboard/v3"

// ErrInvalid marks a bundle that can never activate as-is; the reconciler
// quarantines it instead of retrying.
var ErrInvalid = errors.New("invalid bundle")

type Manifest struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	ABI        string `json:"abi_version"`
	Entrypoint string `json:"entrypoint"`
	Language   string `json:"language"`
}

type ChannelPointer struct {
	Namespace        string    `json:"namespace,omitempty"`
	Channel          string    `json:"channel"`
	BundleID         string    `json:"bundle_id"`
	Checksum         string    `json:"checksum"`
	Generation       uint64    `json:"generation,omitempty"`
	DescriptorDigest string    `json:"descriptor_digest,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// Revision is an append-only deployment record; generations only ever grow.
type Revision struct {
	Schema             string    `json:"schema"`
	Namespace          string    `json:"namespace,omitempty"`
	Channel            string    `json:"channel"`
	Generation         uint64    `json:"generation"`
	BundleID           string    `json:"bundle_id"`
	DescriptorDigest   string    `json:"descriptor_digest,omitempty"`
	PreviousGeneration uint64    `json:"previous_generation,omitempty"`
	DeployedAt         time.Time `json:"deployed_at"`
	DeployedBy         string    `json:"deployed_by,omitempty"`
	SourceCommit       string    `json:"source_commit,omitempty"`
	CIRun              string    `json:"ci_run,omitempty"`
	Message            string    `json:"message,omitempty"`
}

const RevisionSchema = "switchboard.revision/v1"

type Bundle struct {
	ID            string
	Module        []byte
	Manifest      Manifest
	Checksum      string
	Tests         []byte
	Descriptor    Descriptor
	DescriptorRaw []byte
}

func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: manifest: %v", ErrInvalid, err)
	}
	if manifest.Name == "" {
		return Manifest{}, fmt.Errorf("%w: manifest missing name", ErrInvalid)
	}
	if manifest.Version == "" {
		return Manifest{}, fmt.Errorf("%w: manifest missing version", ErrInvalid)
	}
	if manifest.ABI != ABIVersion {
		return Manifest{}, fmt.Errorf("%w: unsupported abi version %q (this host requires %s; rebuild the bundle with the current SDK)", ErrInvalid, manifest.ABI, ABIVersion)
	}
	if manifest.Entrypoint == "" {
		return Manifest{}, fmt.Errorf("%w: manifest missing entrypoint", ErrInvalid)
	}
	return manifest, nil
}

func ParseChannelPointer(data []byte) (ChannelPointer, error) {
	var pointer ChannelPointer
	if err := json.Unmarshal(data, &pointer); err != nil {
		return ChannelPointer{}, fmt.Errorf("%w: channel pointer: %v", ErrInvalid, err)
	}
	if pointer.Channel == "" {
		return ChannelPointer{}, fmt.Errorf("%w: channel pointer missing channel", ErrInvalid)
	}
	if pointer.BundleID == "" {
		return ChannelPointer{}, fmt.Errorf("%w: channel pointer missing bundle_id", ErrInvalid)
	}
	if pointer.Checksum == "" {
		return ChannelPointer{}, fmt.Errorf("%w: channel pointer missing checksum", ErrInvalid)
	}
	return pointer, nil
}

func ParseRevision(data []byte) (Revision, error) {
	var revision Revision
	if err := json.Unmarshal(data, &revision); err != nil {
		return Revision{}, fmt.Errorf("%w: revision: %v", ErrInvalid, err)
	}
	if revision.Channel == "" {
		return Revision{}, fmt.Errorf("%w: revision missing channel", ErrInvalid)
	}
	if revision.Generation == 0 {
		return Revision{}, fmt.Errorf("%w: revision missing generation", ErrInvalid)
	}
	if revision.BundleID == "" {
		return Revision{}, fmt.Errorf("%w: revision missing bundle_id", ErrInvalid)
	}
	return revision, nil
}

func ModuleChecksum(module []byte) string {
	sum := sha256.Sum256(module)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func VerifyModuleChecksum(module []byte, expected string) error {
	actual := ModuleChecksum(module)
	if strings.TrimSpace(expected) != actual {
		return fmt.Errorf("%w: checksum mismatch: expected %s got %s", ErrInvalid, expected, actual)
	}
	return nil
}
