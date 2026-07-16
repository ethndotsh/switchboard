package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/ethndotsh/switchboard/internal/ruletest"
)

type bundleArtifactOptions struct {
	Name       string
	Tests      []byte
	Provenance bundle.Provenance
}

type bundleArtifactResult struct {
	BundleID  string
	Checksum  string
	TestCases int
}

// writeBundleArtifacts packages a compiled module into a dist directory;
// identical inputs always produce identical bundle IDs.
func writeBundleArtifacts(out string, module []byte, opts bundleArtifactOptions) (bundleArtifactResult, error) {
	identity := bundle.ManifestIdentity{
		Name:       opts.Name,
		ABI:        bundle.ABIVersion,
		Entrypoint: "handle",
		Language:   "go-tinygo",
	}
	artifacts := map[string][]byte{bundle.ArtifactModule: module}
	testCases := 0
	if len(opts.Tests) > 0 {
		suite, err := ruletest.ParseSuite(opts.Tests)
		if err != nil {
			return bundleArtifactResult{}, fmt.Errorf("invalid tests file: %w", err)
		}
		testCases = len(suite.Cases)
		artifacts[bundle.ArtifactTests] = opts.Tests
	}

	descriptor := bundle.NewDescriptor(identity, artifacts)
	bundleID, err := descriptor.BundleID()
	if err != nil {
		return bundleArtifactResult{}, err
	}
	descriptor.Provenance = opts.Provenance
	descriptorRaw, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return bundleArtifactResult{}, err
	}

	manifest := bundle.Manifest{
		Name:       identity.Name,
		Version:    bundleID,
		ABI:        identity.ABI,
		Entrypoint: identity.Entrypoint,
		Language:   identity.Language,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return bundleArtifactResult{}, err
	}
	checksum := bundle.ModuleChecksum(module)

	files := map[string][]byte{
		"manifest.json":   manifestData,
		"checksum.txt":    []byte(checksum + "\n"),
		"descriptor.json": descriptorRaw,
	}
	if len(opts.Tests) > 0 {
		files["tests.yaml"] = opts.Tests
	} else {
		// A stale tests.yaml would break descriptor verification.
		_ = os.Remove(filepath.Join(out, "tests.yaml"))
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
			return bundleArtifactResult{}, err
		}
	}
	return bundleArtifactResult{BundleID: bundleID, Checksum: checksum, TestCases: testCases}, nil
}

func readBundleDir(dir string) (bundle.Bundle, error) {
	module, err := os.ReadFile(filepath.Join(dir, "module.wasm"))
	if err != nil {
		return bundle.Bundle{}, err
	}
	manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return bundle.Bundle{}, err
	}
	manifest, err := bundle.ParseManifest(manifestData)
	if err != nil {
		return bundle.Bundle{}, err
	}
	if manifest.Version == "" {
		return bundle.Bundle{}, fs.ErrInvalid
	}
	b := bundle.Bundle{
		ID:       manifest.Version,
		Module:   module,
		Manifest: manifest,
		Checksum: bundle.ModuleChecksum(module),
	}

	descriptorRaw, err := os.ReadFile(filepath.Join(dir, "descriptor.json"))
	switch {
	case err == nil:
		descriptor, err := bundle.ParseDescriptor(descriptorRaw)
		if err != nil {
			return bundle.Bundle{}, err
		}
		files := map[string][]byte{bundle.ArtifactModule: module}
		if _, declared := descriptor.Artifacts[bundle.ArtifactTests]; declared {
			tests, err := os.ReadFile(filepath.Join(dir, "tests.yaml"))
			if err != nil {
				return bundle.Bundle{}, fmt.Errorf("descriptor declares tests.yaml but it is missing: %w", err)
			}
			files[bundle.ArtifactTests] = tests
			b.Tests = tests
		}
		if err := descriptor.Verify(files); err != nil {
			return bundle.Bundle{}, err
		}
		derivedID, err := descriptor.BundleID()
		if err != nil {
			return bundle.Bundle{}, err
		}
		if derivedID != manifest.Version {
			return bundle.Bundle{}, fmt.Errorf("%w: manifest version %s does not match descriptor-derived id %s", bundle.ErrInvalid, manifest.Version, derivedID)
		}
		b.Descriptor = descriptor
		b.DescriptorRaw = descriptorRaw
	case errors.Is(err, fs.ErrNotExist):
		checksumData, err := os.ReadFile(filepath.Join(dir, "checksum.txt"))
		if err != nil {
			return bundle.Bundle{}, err
		}
		if err := bundle.VerifyModuleChecksum(module, strings.TrimSpace(string(checksumData))); err != nil {
			return bundle.Bundle{}, err
		}
		fmt.Fprintf(os.Stderr, "warning: %s has no descriptor.json (built by an older CLI); rebuild to get content-addressed bundle IDs\n", dir)
	default:
		return bundle.Bundle{}, err
	}
	return b, nil
}

// buildProvenance is best-effort and never fails a build.
func buildProvenance(ctx context.Context) bundle.Provenance {
	provenance := bundle.Provenance{
		BuiltAt: time.Now().UTC(),
		Builder: builderString(ctx),
		CIRun:   os.Getenv("GITHUB_RUN_ID"),
	}
	if commit := os.Getenv("GITHUB_SHA"); commit != "" {
		provenance.SourceCommit = commit
		return provenance
	}
	if commit, dirty, ok := gitState(ctx); ok {
		provenance.SourceCommit = commit
		provenance.SourceDirty = dirty
	}
	return provenance
}

func builderString(ctx context.Context) string {
	builder := "switchboard/" + version
	out, err := exec.CommandContext(ctx, "tinygo", "version").Output()
	if err != nil {
		return builder
	}
	fields := strings.Fields(string(out))
	if len(fields) >= 3 && fields[0] == "tinygo" {
		return builder + " tinygo/" + fields[2]
	}
	return builder
}

func gitState(ctx context.Context) (string, bool, bool) {
	commitOut, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", false, false
	}
	commit := strings.TrimSpace(string(commitOut))
	statusOut, err := exec.CommandContext(ctx, "git", "status", "--porcelain").Output()
	if err != nil {
		return commit, false, true
	}
	return commit, len(strings.TrimSpace(string(statusOut))) > 0, true
}

func abbreviateBundleID(id string) string {
	if hexPart, ok := strings.CutPrefix(id, "sha256-"); ok && len(hexPart) > 12 {
		return "sha256-" + hexPart[:12]
	}
	return id
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
