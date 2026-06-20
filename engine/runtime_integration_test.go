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
	if next.Type != switchboard.ActionNext || !hasHeaderOp(next, switchboard.HeaderOpSet, "x-switchboard-rule", "basic") {
		t.Fatalf("next action = %#v", next)
	}

	headerDeny, err := runtime.Invoke(ctx, switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{"x-switchboard-deny": {"yes"}}})
	if err != nil {
		t.Fatal(err)
	}
	if headerDeny.Type != switchboard.ActionDeny || headerDeny.StatusCode != 418 {
		t.Fatalf("header deny action = %#v", headerDeny)
	}

	headerOps, err := runtime.Invoke(ctx, switchboard.Request{Path: "/headers", Method: "GET", Headers: map[string][]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if !hasHeaderOp(headerOps, switchboard.HeaderOpAdd, "x-switchboard-list", "one") ||
		!hasHeaderOp(headerOps, switchboard.HeaderOpAdd, "x-switchboard-list", "two") ||
		!hasHeaderOp(headerOps, switchboard.HeaderOpDelete, "x-switchboard-delete", "") {
		t.Fatalf("header ops action = %#v", headerOps)
	}
}

func TestRuntimePoolExhaustion(t *testing.T) {
	runtime, ctx, cleanup := loadRuntimeForTest(t, 1)
	defer cleanup()

	held, err := runtime.acquireModule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.releaseModule(ctx, held, true)

	_, err = runtime.Invoke(ctx, switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{}})
	if err != ErrRuntimePoolExhausted {
		t.Fatalf("expected pool exhaustion, got %v", err)
	}
}

func hasHeaderOp(action switchboard.Action, op switchboard.HeaderOpType, name string, value string) bool {
	for _, headerOp := range action.HeaderOps {
		if headerOp.Op == op && headerOp.Name == name && headerOp.Value == value {
			return true
		}
	}
	return false
}

func TestRuntimeAutoscaleIncreasesAfterExhaustion(t *testing.T) {
	runtime, ctx, cleanup := loadRuntimeForTestWithPoolConfig(t, PoolConfig{MinSize: 1, MaxSize: 2, Autoscale: true})
	defer cleanup()
	runtime.scaleInterval = time.Hour

	held, err := runtime.acquireModule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Invoke(ctx, switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{}})
	if err != ErrRuntimePoolExhausted {
		t.Fatalf("expected pool exhaustion, got %v", err)
	}
	if int(runtime.totalInstances.Load()) != 1 {
		t.Fatalf("request path instantiated a module; total instances = %d", runtime.totalInstances.Load())
	}
	runtime.adjustPool(ctx)
	if runtime.PoolSize() != 2 {
		t.Fatalf("pool target = %d", runtime.PoolSize())
	}
	if int(runtime.totalInstances.Load()) != 2 {
		t.Fatalf("total instances = %d", runtime.totalInstances.Load())
	}
	runtime.releaseModule(ctx, held, true)
}

func TestRuntimeAutoscaleDecreasesAfterSustainedIdle(t *testing.T) {
	runtime, ctx, cleanup := loadRuntimeForTestWithPoolConfig(t, PoolConfig{MinSize: 1, MaxSize: 4, Autoscale: true})
	defer cleanup()
	runtime.scaleInterval = time.Hour
	runtime.idleWindowLimit = 1
	runtime.targetPoolSize.Store(4)
	if err := runtime.Warm(ctx, 3); err != nil {
		t.Fatal(err)
	}

	runtime.adjustPool(ctx)
	if runtime.PoolSize() != 3 {
		t.Fatalf("pool target = %d", runtime.PoolSize())
	}
	if int(runtime.totalInstances.Load()) != 3 {
		t.Fatalf("total instances = %d", runtime.totalInstances.Load())
	}
}

func loadRuntimeForTest(t testing.TB, poolSize int) (*Runtime, context.Context, func()) {
	return loadRuntimeForTestWithPoolConfig(t, PoolConfig{MinSize: poolSize, MaxSize: poolSize, Autoscale: false})
}

func loadRuntimeForTestWithPoolConfig(t testing.TB, poolCfg PoolConfig) (*Runtime, context.Context, func()) {
	return loadRuntimeForTestWithBackend(t, NewWasmRuntime, poolCfg)
}

func loadRuntimeForTestWithBackend(t testing.TB, newRuntime func(context.Context) (WasmRuntime, error), poolCfg PoolConfig) (*Runtime, context.Context, func()) {
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
	wasmRuntime, err := newRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntimeWithPoolConfig(ctx, wasmRuntime, bundle.Bundle{
		ID:       manifest.Version,
		Module:   module,
		Manifest: manifest,
		Checksum: checksum,
	}, 500*time.Millisecond, poolCfg, nil)
	if err != nil {
		_ = wasmRuntime.Close(ctx)
		t.Fatal(err)
	}
	return runtime, ctx, func() {
		_ = runtime.Close(ctx)
		_ = wasmRuntime.Close(ctx)
	}
}
