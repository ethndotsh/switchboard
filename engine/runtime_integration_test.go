package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/internal/bundle"
)

func TestRuntimeInvokesBuiltBundle(t *testing.T) {
	runtime, ctx, cleanup := loadRuntimeForTest(t, 2)
	defer cleanup()

	if runtime.PoolSize() != 2 {
		t.Fatalf("pool size = %d", runtime.PoolSize())
	}

	deny, err := runtime.Invoke(ctx, switchboard.Request{Path: "/blocked", Method: "GET", Headers: map[string][]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if deny.Type != switchboard.ActionDeny || deny.StatusCode != 403 {
		t.Fatalf("deny action = %#v", deny)
	}

	next, err := runtime.Invoke(ctx, switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if next.Type != switchboard.ActionNext || next.Headers["x-switchboard-rule"] != "basic" {
		t.Fatalf("next action = %#v", next)
	}
}

func TestRuntimePoolExhaustion(t *testing.T) {
	runtime, ctx, cleanup := loadRuntimeForTest(t, 1)
	defer cleanup()

	held, err := runtime.acquireModule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close(ctx)

	_, err = runtime.Invoke(ctx, switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{}})
	if err != ErrRuntimePoolExhausted {
		t.Fatalf("expected pool exhaustion, got %v", err)
	}
}

func loadRuntimeForTest(t testing.TB, poolSize int) (*Runtime, context.Context, func()) {
	t.Helper()
	dist := filepath.Join("..", "dist")
	module, err := os.ReadFile(filepath.Join(dist, "module.wasm"))
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("dist/module.wasm not present; run switchboard build for integration coverage")
		}
		t.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(dist, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	checksumData, err := os.ReadFile(filepath.Join(dist, "checksum.txt"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := bundle.ParseManifest(manifestData)
	if err != nil {
		t.Fatal(err)
	}
	checksum := strings.TrimSpace(string(checksumData))
	if err := bundle.VerifyModuleChecksum(module, checksum); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	wasmRuntime, err := NewWasmRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(ctx, wasmRuntime, bundle.Bundle{
		ID:       manifest.Version,
		Module:   module,
		Manifest: manifest,
		Checksum: checksum,
	}, 500*time.Millisecond, poolSize)
	if err != nil {
		_ = wasmRuntime.Close(ctx)
		t.Fatal(err)
	}
	return runtime, ctx, func() {
		_ = runtime.Close(ctx)
		_ = wasmRuntime.Close(ctx)
	}
}
