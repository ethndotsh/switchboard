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

func loadBenchmarkRuntime(b *testing.B, poolSize int) (*Runtime, context.Context, func()) {
	return loadBenchmarkRuntimeWithPoolConfig(b, PoolConfig{MinSize: poolSize, MaxSize: poolSize, Autoscale: false})
}

func loadBenchmarkRuntimeWithPoolConfig(b *testing.B, poolCfg PoolConfig) (*Runtime, context.Context, func()) {
	b.Helper()

	dist := os.Getenv("SWITCHBOARD_BENCH_DIST")
	if dist == "" {
		dist = filepath.Join("..", "dist")
	}
	module, err := os.ReadFile(filepath.Join(dist, "module.wasm"))
	if err != nil {
		if os.IsNotExist(err) {
			b.Skip("dist/module.wasm not present; run switchboard build for benchmark coverage")
		}
		b.Fatal(err)
	}
	manifestData, err := os.ReadFile(filepath.Join(dist, "manifest.json"))
	if err != nil {
		b.Fatal(err)
	}
	checksumData, err := os.ReadFile(filepath.Join(dist, "checksum.txt"))
	if err != nil {
		b.Fatal(err)
	}
	manifest, err := bundle.ParseManifest(manifestData)
	if err != nil {
		b.Fatal(err)
	}
	checksum := strings.TrimSpace(string(checksumData))
	if err := bundle.VerifyModuleChecksum(module, checksum); err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	wasmRuntime, err := NewWasmRuntime(ctx)
	if err != nil {
		b.Fatal(err)
	}
	runtime, err := NewRuntimeWithPoolConfig(ctx, wasmRuntime, bundle.Bundle{
		ID:       manifest.Version,
		Module:   module,
		Manifest: manifest,
		Checksum: checksum,
	}, 500*time.Millisecond, poolCfg, nil)
	if err != nil {
		_ = wasmRuntime.Close(ctx)
		b.Fatal(err)
	}
	return runtime, ctx, func() {
		_ = runtime.Close(ctx)
		_ = wasmRuntime.Close(ctx)
	}
}

func BenchmarkRuntimeInvokeWarmPool(b *testing.B) {
	runtime, ctx, cleanup := loadBenchmarkRuntime(b, 1)
	defer cleanup()

	req := switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runtime.Invoke(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimeInvokeAdaptiveSteadyState(b *testing.B) {
	runtime, ctx, cleanup := loadBenchmarkRuntimeWithPoolConfig(b, PoolConfig{MinSize: 1, MaxSize: 4, Autoscale: true})
	defer cleanup()

	req := switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runtime.Invoke(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimeInvokeBlockWarmPool(b *testing.B) {
	runtime, ctx, cleanup := loadBenchmarkRuntime(b, 1)
	defer cleanup()

	req := switchboard.Request{Path: "/blocked", Method: "GET", Headers: map[string][]string{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runtime.Invoke(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimeInvokeWarmPoolParallel(b *testing.B) {
	runtime, ctx, cleanup := loadBenchmarkRuntime(b, DefaultPoolSize)
	defer cleanup()

	req := switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{}}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := runtime.Invoke(ctx, req); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkRuntimePoolExhausted(b *testing.B) {
	runtime, ctx, cleanup := loadBenchmarkRuntime(b, 1)
	defer cleanup()

	held, err := runtime.acquireModule(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer runtime.releaseModule(ctx, held, true)

	req := switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runtime.Invoke(ctx, req); err != ErrRuntimePoolExhausted {
			b.Fatalf("expected pool exhaustion, got %v", err)
		}
	}
}

func BenchmarkRuntimeAdaptivePoolExhaustedSignal(b *testing.B) {
	runtime, ctx, cleanup := loadBenchmarkRuntimeWithPoolConfig(b, PoolConfig{MinSize: 1, MaxSize: 4, Autoscale: true})
	defer cleanup()
	if runtime.scaleCancel != nil {
		runtime.scaleCancel()
	}

	held, err := runtime.acquireModule(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer runtime.releaseModule(ctx, held, true)

	req := switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runtime.Invoke(ctx, req); err != ErrRuntimePoolExhausted {
			b.Fatalf("expected pool exhaustion, got %v", err)
		}
	}
}
