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
	return loadBenchmarkRuntimeWithBackend(b, NewWasmRuntime, poolCfg)
}

func loadBenchmarkRuntimeWithBackend(b *testing.B, newRuntime func(context.Context) (WasmRuntime, error), poolCfg PoolConfig) (*Runtime, context.Context, func()) {
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
	wasmRuntime, err := newRuntime(ctx)
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

func loadBenchmarkBundle(b *testing.B) bundle.Bundle {
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
	return bundle.Bundle{ID: manifest.Version, Module: module, Manifest: manifest, Checksum: checksum}
}

func benchmarkCompileAndWarm(b *testing.B, newRuntime func(context.Context) (WasmRuntime, error), poolSize int) {
	b.Helper()
	benchBundle := loadBenchmarkBundle(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wasmRuntime, err := newRuntime(ctx)
		if err != nil {
			b.Fatal(err)
		}
		runtime, err := NewRuntimeWithPoolConfig(ctx, wasmRuntime, benchBundle, 500*time.Millisecond, PoolConfig{MinSize: poolSize, MaxSize: poolSize, Autoscale: false}, nil)
		if err != nil {
			_ = wasmRuntime.Close(ctx)
			b.Fatal(err)
		}
		_ = runtime.Close(ctx)
		_ = wasmRuntime.Close(ctx)
	}
}

func benchmarkCompileAndWarmSharedRuntime(b *testing.B, wasmRuntime WasmRuntime, poolSize int) {
	b.Helper()
	benchBundle := loadBenchmarkBundle(b)
	ctx := context.Background()
	defer wasmRuntime.Close(ctx)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtime, err := NewRuntimeWithPoolConfig(ctx, wasmRuntime, benchBundle, 500*time.Millisecond, PoolConfig{MinSize: poolSize, MaxSize: poolSize, Autoscale: false}, nil)
		if err != nil {
			b.Fatal(err)
		}
		_ = runtime.Close(ctx)
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

func BenchmarkRuntimeInvokeWarmPoolWithHeaders(b *testing.B) {
	runtime, ctx, cleanup := loadBenchmarkRuntime(b, 1)
	defer cleanup()

	req := benchmarkRequestWithHeaders("/")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runtime.Invoke(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimeInvokeHeaderReadWarmPool(b *testing.B) {
	runtime, ctx, cleanup := loadBenchmarkRuntime(b, 1)
	defer cleanup()

	req := switchboard.Request{Path: "/", Method: "GET", Headers: map[string][]string{"x-switchboard-deny": {"yes"}}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runtime.Invoke(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimeInvokeHeaderOpsWarmPool(b *testing.B) {
	runtime, ctx, cleanup := loadBenchmarkRuntime(b, 1)
	defer cleanup()

	req := switchboard.Request{Path: "/headers", Method: "GET", Headers: map[string][]string{}}
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

func benchmarkRequestWithHeaders(path string) switchboard.Request {
	return switchboard.Request{
		Path:   path,
		Method: "GET",
		Headers: map[string][]string{
			"accept":            {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
			"accept-encoding":   {"gzip, deflate, br"},
			"accept-language":   {"en-US,en;q=0.9"},
			"cache-control":     {"no-cache"},
			"user-agent":        {"switchboard-benchmark/1.0"},
			"x-forwarded-for":   {"203.0.113.10"},
			"x-forwarded-host":  {"example.com"},
			"x-forwarded-proto": {"https"},
		},
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

func BenchmarkRuntimeCompileAndWarmWazero16(b *testing.B) {
	benchmarkCompileAndWarm(b, NewWasmRuntime, 16)
}

func BenchmarkRuntimeCompileAndWarmSharedWazero16(b *testing.B) {
	ctx := context.Background()
	wasmRuntime, err := NewWasmRuntime(ctx)
	if err != nil {
		b.Fatal(err)
	}
	benchmarkCompileAndWarmSharedRuntime(b, wasmRuntime, 16)
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
