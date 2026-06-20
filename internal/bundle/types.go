package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const ABIVersion = "switchboard/v0"

type Manifest struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	ABI        string `json:"abi_version"`
	Entrypoint string `json:"entrypoint"`
	Language   string `json:"language"`
}

type ChannelPointer struct {
	Namespace string    `json:"namespace,omitempty"`
	Channel   string    `json:"channel"`
	BundleID  string    `json:"bundle_id"`
	Checksum  string    `json:"checksum"`
	CreatedAt time.Time `json:"created_at"`
}

type Bundle struct {
	ID       string
	Module   []byte
	Manifest Manifest
	Checksum string
}

func ParseManifest(data []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.Name == "" {
		return Manifest{}, fmt.Errorf("manifest missing name")
	}
	if manifest.Version == "" {
		return Manifest{}, fmt.Errorf("manifest missing version")
	}
	if manifest.ABI != ABIVersion {
		return Manifest{}, fmt.Errorf("unsupported abi version %q", manifest.ABI)
	}
	if manifest.Entrypoint == "" {
		return Manifest{}, fmt.Errorf("manifest missing entrypoint")
	}
	return manifest, nil
}

func ParseChannelPointer(data []byte) (ChannelPointer, error) {
	var pointer ChannelPointer
	if err := json.Unmarshal(data, &pointer); err != nil {
		return ChannelPointer{}, err
	}
	if pointer.Channel == "" {
		return ChannelPointer{}, fmt.Errorf("channel pointer missing channel")
	}
	if pointer.BundleID == "" {
		return ChannelPointer{}, fmt.Errorf("channel pointer missing bundle_id")
	}
	if pointer.Checksum == "" {
		return ChannelPointer{}, fmt.Errorf("channel pointer missing checksum")
	}
	return pointer, nil
}

func ModuleChecksum(module []byte) string {
	sum := sha256.Sum256(module)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func VerifyModuleChecksum(module []byte, expected string) error {
	actual := ModuleChecksum(module)
	if strings.TrimSpace(expected) != actual {
		return fmt.Errorf("checksum mismatch: expected %s got %s", expected, actual)
	}
	return nil
}
