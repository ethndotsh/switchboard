package engine

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/ethndotsh/switchboard/registry"
)

// stubRegistry provides a full Registry with per-method overrides.
type stubRegistry struct {
	channel        bundle.ChannelPointer
	channelErr     error
	bundleErr      error
	getChannelHits int
	getBundleHits  int
}

func (r *stubRegistry) GetChannel(context.Context, registry.Scope, string) (bundle.ChannelPointer, error) {
	r.getChannelHits++
	return r.channel, r.channelErr
}

func (r *stubRegistry) GetBundle(context.Context, registry.Scope, string) (bundle.Bundle, error) {
	r.getBundleHits++
	if r.bundleErr != nil {
		return bundle.Bundle{}, r.bundleErr
	}
	return bundle.Bundle{}, fmt.Errorf("boom")
}

func (r *stubRegistry) HasBundle(context.Context, registry.Scope, string) (bool, error) {
	return false, nil
}
func (r *stubRegistry) PutBundle(context.Context, registry.Scope, bundle.Bundle) error { return nil }
func (r *stubRegistry) PutChannel(context.Context, registry.Scope, bundle.ChannelPointer) error {
	return nil
}
func (r *stubRegistry) GetRevision(context.Context, registry.Scope, string, uint64) (bundle.Revision, error) {
	return bundle.Revision{}, registry.ErrNotFound
}
func (r *stubRegistry) PutRevision(context.Context, registry.Scope, bundle.Revision) error {
	return nil
}
func (r *stubRegistry) ListRevisions(context.Context, registry.Scope, string, int) ([]bundle.Revision, error) {
	return nil, nil
}

func newTestReconciler(reg registry.Registry, manager *RuntimeManager, wasmRuntime WasmRuntime) *Reconciler {
	return NewReconciler(reg, manager, wasmRuntime, ResolvedConfig{Channel: "prod"}, nil)
}

func TestRuntimeManagerActivateKeepsPreviousAsLastGood(t *testing.T) {
	manager := &RuntimeManager{}
	first := &Runtime{id: "v1"}
	second := &Runtime{id: "v2"}

	manager.Activate(first)
	manager.Activate(second)

	if manager.Current().ID() != "v2" {
		t.Fatalf("current runtime = %q", manager.Current().ID())
	}
	if manager.LastGood().ID() != "v1" {
		t.Fatalf("last good runtime = %q", manager.LastGood().ID())
	}
}

func TestRuntimeManagerActivateRetiresDisplacedLastGood(t *testing.T) {
	manager := &RuntimeManager{}
	first := newIdleTestRuntime("v1")
	second := newIdleTestRuntime("v2")
	third := newIdleTestRuntime("v3")

	manager.Activate(first)
	manager.Activate(second)
	manager.Activate(third)

	if manager.Current().ID() != "v3" || manager.LastGood().ID() != "v2" {
		t.Fatalf("current = %q last good = %q", manager.Current().ID(), manager.LastGood().ID())
	}
	waitFor(t, time.Second, func() bool { return first.IsClosed() })
	if second.IsClosed() || third.IsClosed() {
		t.Fatal("active or last-good runtime was closed prematurely")
	}
	_ = manager.Close(context.Background())
}

func TestRuntimeManagerRetireWaitsForInflight(t *testing.T) {
	manager := &RuntimeManager{}
	first := newIdleTestRuntime("v1")
	first.inflight.Store(1)

	manager.Activate(first)
	manager.Activate(newIdleTestRuntime("v2"))
	manager.Activate(newIdleTestRuntime("v3"))

	time.Sleep(4 * retirePollInterval)
	if first.IsClosed() {
		t.Fatal("runtime with in-flight work was closed before draining")
	}
	first.inflight.Store(0)
	waitFor(t, time.Second, func() bool { return first.IsClosed() })
	_ = manager.Close(context.Background())
}

func TestRuntimeManagerCloseCancelsPendingRetirements(t *testing.T) {
	manager := &RuntimeManager{}
	first := newIdleTestRuntime("v1")
	first.inflight.Store(1)

	manager.Activate(first)
	manager.Activate(newIdleTestRuntime("v2"))
	manager.Activate(newIdleTestRuntime("v3"))

	done := make(chan struct{})
	go func() {
		_ = manager.Close(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("manager.Close blocked on a pending retirement")
	}
	if !first.IsClosed() {
		t.Fatal("displaced runtime not closed on shutdown")
	}
}

// newIdleTestRuntime builds a Runtime with no compiled module, close-safe
// for lifecycle tests.
func newIdleTestRuntime(id string) *Runtime {
	return &Runtime{
		id:     id,
		module: closableModule{},
		pool:   make(chan RuleInstance, 1),
	}
}

type closableModule struct{}

func (closableModule) Instantiate(context.Context) (RuleInstance, error) {
	return nil, fmt.Errorf("not instantiable")
}
func (closableModule) Close(context.Context) error { return nil }

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestReconcilerDoesNotReplaceActiveOnBundleFailure(t *testing.T) {
	ctx := context.Background()
	wasmRuntime, err := NewWasmRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer wasmRuntime.Close(ctx)

	manager := &RuntimeManager{}
	manager.active.Store(&Runtime{id: "v1"})
	reconciler := newTestReconciler(
		&stubRegistry{channel: bundle.ChannelPointer{Channel: "prod", BundleID: "v2", Checksum: "sha256:x"}},
		manager, wasmRuntime)
	reconciler.reconcile(ctx)
	if manager.Current().ID() != "v1" {
		t.Fatalf("active runtime changed to %q", manager.Current().ID())
	}
	state := reconciler.State()
	if state.DesiredBundleID != "v2" {
		t.Fatalf("desired bundle = %q", state.DesiredBundleID)
	}
	if state.LastFailedActivation != "v2" {
		t.Fatalf("last failed activation = %q", state.LastFailedActivation)
	}
	if state.LastError == "" {
		t.Fatal("expected last error to be recorded")
	}
}

func TestReconcilerQuarantinesPermanentFailure(t *testing.T) {
	ctx := context.Background()
	wasmRuntime, err := NewWasmRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer wasmRuntime.Close(ctx)

	reg := &stubRegistry{
		channel:   bundle.ChannelPointer{Channel: "prod", BundleID: "v2", Checksum: "sha256:x"},
		bundleErr: fmt.Errorf("%w: checksum mismatch", bundle.ErrInvalid),
	}
	reconciler := newTestReconciler(reg, &RuntimeManager{}, wasmRuntime)

	reconciler.reconcile(ctx)
	if reg.getBundleHits != 1 {
		t.Fatalf("bundle fetches = %d", reg.getBundleHits)
	}
	state := reconciler.State()
	if state.QuarantinedBundleID != "v2" || state.QuarantineReason == "" {
		t.Fatalf("quarantine state = %#v", state)
	}

	// Same pointer: no re-download, no recompile.
	reconciler.reconcile(ctx)
	reconciler.reconcile(ctx)
	if reg.getBundleHits != 1 {
		t.Fatalf("quarantined bundle was refetched: %d fetches", reg.getBundleHits)
	}

	// New pointer content: quarantine lifts.
	reg.channel.Checksum = "sha256:y"
	reconciler.reconcile(ctx)
	if reg.getBundleHits != 2 {
		t.Fatalf("expected refetch after pointer change, fetches = %d", reg.getBundleHits)
	}
}

func TestReconcilerTransientFailureBacksOff(t *testing.T) {
	ctx := context.Background()
	wasmRuntime, err := NewWasmRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer wasmRuntime.Close(ctx)

	reg := &stubRegistry{channelErr: fmt.Errorf("connection refused")}
	reconciler := newTestReconciler(reg, &RuntimeManager{}, wasmRuntime)

	reconciler.reconcile(ctx)
	if reg.getChannelHits != 1 {
		t.Fatalf("channel fetches = %d", reg.getChannelHits)
	}
	state := reconciler.State()
	if state.TransientFailures != 1 || state.NextRetryAt.IsZero() {
		t.Fatalf("state = %#v", state)
	}

	// Inside the backoff window: no registry traffic at all.
	reconciler.reconcile(ctx)
	if reg.getChannelHits != 1 {
		t.Fatalf("reconcile ignored backoff: %d fetches", reg.getChannelHits)
	}

	// Force the window past and let a successful check reset the counters.
	reconciler.nextRetryAt = time.Now().Add(-time.Second)
	reg.channelErr = nil
	reg.channel = bundle.ChannelPointer{Channel: "prod", BundleID: "v1", Checksum: "sha256:x"}
	reg.bundleErr = fmt.Errorf("still broken")
	reconciler.reconcile(ctx)
	if reg.getChannelHits != 2 {
		t.Fatalf("channel fetches = %d", reg.getChannelHits)
	}
	if got := reconciler.State().TransientFailures; got != 1 {
		t.Fatalf("transient failures after successful GetChannel = %d", got)
	}
}

func TestReconcilerRecordsConfigurationFailure(t *testing.T) {
	reconciler := &Reconciler{channel: "prod"}
	reconciler.reconcile(context.Background())

	state := reconciler.State()
	if state.Channel != "prod" {
		t.Fatalf("channel = %q", state.Channel)
	}
	if state.LastError == "" {
		t.Fatal("expected configuration failure to be recorded")
	}
	if state.LastCheckedAt.IsZero() {
		t.Fatal("expected last checked timestamp")
	}
}

func TestResolveConfigDefaults(t *testing.T) {
	cfg, err := ResolveConfig(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Channel != "prod" {
		t.Fatalf("channel = %q", cfg.Channel)
	}
	if cfg.FailMode != FailModeOpen {
		t.Fatalf("fail mode = %q", cfg.FailMode)
	}
	if cfg.PoolSize != DefaultPoolSize {
		t.Fatalf("pool size = %d", cfg.PoolSize)
	}
	if !cfg.PoolAutoscale {
		t.Fatal("expected pool autoscale to default on")
	}
	if cfg.MinPoolSize != DefaultPoolSize {
		t.Fatalf("min pool size = %d", cfg.MinPoolSize)
	}
	if cfg.MaxPoolSize != DefaultPoolSize*4 {
		t.Fatalf("max pool size = %d", cfg.MaxPoolSize)
	}
	if cfg.MemoryLimitBytes != 32<<20 {
		t.Fatalf("memory limit = %d", cfg.MemoryLimitBytes)
	}
	if cfg.MaxActionBytes != DefaultMaxActionBytes || cfg.MaxHeaderOps != DefaultMaxHeaderOps || cfg.MaxResponseBody != DefaultMaxResponseBody {
		t.Fatalf("limits = %#v", cfg)
	}
	if cfg.BootstrapFromCache {
		t.Fatal("bootstrap_from_cache should default off without cache_dir")
	}
}

func TestResolveConfigLimits(t *testing.T) {
	cfg, err := ResolveConfig(Config{
		FailMode:         "last_good",
		FallbackFailMode: "closed",
		MemoryLimit:      "64mb",
		MaxActionBytes:   "16kb",
		MaxHeaderOps:     8,
		MaxResponseBody:  "4kb",
		CacheDir:         "/tmp/sb-cache",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FailMode != FailModeLastGood || cfg.FallbackFailMode != FailModeClosed {
		t.Fatalf("fail modes = %q %q", cfg.FailMode, cfg.FallbackFailMode)
	}
	if cfg.MemoryLimitBytes != 64<<20 || cfg.MaxActionBytes != 16<<10 || cfg.MaxHeaderOps != 8 || cfg.MaxResponseBody != 4<<10 {
		t.Fatalf("limits = %#v", cfg)
	}
	if !cfg.BootstrapFromCache {
		t.Fatal("bootstrap_from_cache should default on with cache_dir")
	}
}

func TestResolveConfigRejectsInvalid(t *testing.T) {
	tests := []Config{
		{PoolAutoscale: "maybe"},
		{PoolSize: -1},
		{MinPoolSize: -1},
		{MaxPoolSize: -1},
		{MinPoolSize: 16, MaxPoolSize: 8},
		{FailMode: "sometimes"},
		{FallbackFailMode: "closed"}, // requires last_good
		{FailMode: "last_good", FallbackFailMode: "last_good"},
		{MemoryLimit: "half"},
		{MemoryLimit: "64kb"}, // below 1 MiB floor
		{MemoryLimit: "8gb"},
		{MaxActionBytes: "2mb"},
		{MaxHeaderOps: 4096},
		{MaxResponseBody: "2mb"},
		{BootstrapFromCache: "on"}, // requires cache_dir
		{BootstrapFromCache: "maybe", CacheDir: "/tmp/x"},
		{Registry: "ftp"},
	}
	for _, cfg := range tests {
		if _, err := ResolveConfig(cfg); err == nil {
			t.Fatalf("expected error for config %#v", cfg)
		}
	}
}

func TestResolveConfigAcceptsNewRegistries(t *testing.T) {
	for _, name := range []string{"", "s3", "file", "https"} {
		if _, err := ResolveConfig(Config{Registry: name}); err != nil {
			t.Fatalf("registry %q rejected: %v", name, err)
		}
	}
}

func TestBackoffDelayGrowsAndCaps(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	previous := time.Duration(0)
	for attempts := 1; attempts <= 12; attempts++ {
		delay := backoffDelay(attempts, rng)
		if delay <= 0 {
			t.Fatalf("attempt %d: non-positive delay", attempts)
		}
		if delay > time.Duration(float64(backoffCap)*1.2) {
			t.Fatalf("attempt %d: delay %v exceeds cap", attempts, delay)
		}
		if attempts <= 6 && delay < previous/4 {
			t.Fatalf("attempt %d: delay %v shrank too much from %v", attempts, delay, previous)
		}
		previous = delay
	}
}

func TestParseByteSize(t *testing.T) {
	cases := map[string]int64{
		"32mb":    32 << 20,
		"64kb":    64 << 10,
		"1gb":     1 << 30,
		"512kib":  512 << 10,
		"1048576": 1 << 20,
		" 8MB ":   8 << 20,
	}
	for input, want := range cases {
		got, err := ParseByteSize(input)
		if err != nil {
			t.Fatalf("ParseByteSize(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseByteSize(%q) = %d, want %d", input, got, want)
		}
	}
	for _, invalid := range []string{"", "-1mb", "0", "lots", "1.5mb"} {
		if _, err := ParseByteSize(invalid); err == nil {
			t.Fatalf("ParseByteSize(%q) should fail", invalid)
		}
	}
	if pages := bytesToWasmPages(32 << 20); pages != 512 {
		t.Fatalf("pages for 32 MiB = %d", pages)
	}
	if pages := bytesToWasmPages(1); pages != 1 {
		t.Fatalf("pages for 1 byte = %d", pages)
	}
	if pages := bytesToWasmPages(5 << 30); pages != 65536 {
		t.Fatalf("pages for 5 GiB = %d", pages)
	}
}
