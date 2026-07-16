package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ethndotsh/switchboard/engine"
	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/ethndotsh/switchboard/registry"
)

// resolveBundleRef loads a bundle from a reference, which may be:
//   - a local dist directory (./dist)
//   - a channel name in the configured registry (prod)
//   - a content-addressed bundle ID or unique prefix (sha256-ab12…)
func resolveBundleRef(ctx context.Context, ref string, scope registry.Scope, registryURL string) (bundle.Bundle, error) {
	if info, err := os.Stat(ref); err == nil && info.IsDir() {
		return readBundleDir(ref)
	}
	reg, err := openRegistry(ctx, registryURL)
	if err != nil {
		return bundle.Bundle{}, fmt.Errorf("ref %q is not a local directory and the registry is unavailable: %w", ref, err)
	}
	if strings.HasPrefix(ref, "sha256-") {
		id, err := resolveBundleIDPrefix(ctx, reg, scope, ref)
		if err != nil {
			return bundle.Bundle{}, err
		}
		return reg.GetBundle(ctx, scope, id)
	}
	pointer, err := reg.GetChannel(ctx, scope, ref)
	if err != nil {
		return bundle.Bundle{}, fmt.Errorf("ref %q is neither a dist directory, a channel, nor a bundle id: %w", ref, err)
	}
	return reg.GetBundle(ctx, scope, pointer.BundleID)
}

func resolveBundleIDPrefix(ctx context.Context, reg registry.Registry, scope registry.Scope, ref string) (string, error) {
	const fullLen = len("sha256-") + 64
	if len(ref) == fullLen {
		return ref, nil
	}
	if len(ref) < len("sha256-")+8 {
		return "", fmt.Errorf("bundle id prefix %q is too short; use at least 8 hex characters", ref)
	}
	if exists, err := reg.HasBundle(ctx, scope, ref); err == nil && exists {
		return ref, nil
	}
	return "", fmt.Errorf("bundle %q not found; pass the full bundle id (prefix lookup requires the exact stored id)", ref)
}

// loadRuntimeForBundle compiles a bundle into a single-instance runtime for
// local execution.
func loadRuntimeForBundle(ctx context.Context, b bundle.Bundle, timeout string) (*engine.Runtime, func(), error) {
	limits := engine.InvokeLimits{}
	if timeout != "" {
		parsed, err := parseDurationFlag(timeout)
		if err != nil {
			return nil, nil, err
		}
		limits.Timeout = parsed
	}
	wasmRuntime, err := engine.NewWasmRuntime(ctx)
	if err != nil {
		return nil, nil, err
	}
	runtime, err := engine.NewRuntime(ctx, wasmRuntime, b, limits, 1)
	if err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, nil, fmt.Errorf("compile bundle %s: %w", abbreviateBundleID(b.ID), err)
	}
	cleanup := func() {
		_ = runtime.Close(context.Background())
		_ = wasmRuntime.Close(context.Background())
	}
	return runtime, cleanup, nil
}

func parseDurationFlag(value string) (time.Duration, error) {
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	if parsed <= 0 {
		return 0, errors.New("duration must be positive")
	}
	return parsed, nil
}
