package engine

import (
	"context"
	"fmt"
	"testing"

	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/ethndotsh/switchboard/registry"
)

type fakeRuntime struct{ id string }

func TestRuntimeManagerTryActivateKeepsPreviousAsLastGood(t *testing.T) {
	manager := &RuntimeManager{}
	first := &Runtime{id: "v1"}
	second := &Runtime{id: "v2"}

	manager.active.Store(first)
	manager.lastGood.Store(nil)
	manager.active.Store(second)
	manager.lastGood.Store(first)

	if manager.Current().ID() != "v2" {
		t.Fatalf("current runtime = %q", manager.Current().ID())
	}
	if manager.LastGood().ID() != "v1" {
		t.Fatalf("last good runtime = %q", manager.LastGood().ID())
	}
}

type brokenRegistry struct {
	channel bundle.ChannelPointer
}

func (r brokenRegistry) GetChannel(context.Context, registry.Scope, string) (bundle.ChannelPointer, error) {
	return r.channel, nil
}

func (r brokenRegistry) GetBundle(context.Context, registry.Scope, string) (bundle.Bundle, error) {
	return bundle.Bundle{}, fmt.Errorf("boom")
}

func (r brokenRegistry) PutBundle(context.Context, registry.Scope, bundle.Bundle) error { return nil }
func (r brokenRegistry) PutChannel(context.Context, registry.Scope, bundle.ChannelPointer) error {
	return nil
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
	reconciler := &Reconciler{
		registry: brokenRegistry{channel: bundle.ChannelPointer{Channel: "prod", BundleID: "v2", Checksum: "sha256:x"}},
		manager:  manager,
		runtime:  wasmRuntime,
		channel:  "prod",
	}
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
	if cfg.FailMode != "open" {
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
}

func TestResolveConfigPoolSizeIsDefaultMinimum(t *testing.T) {
	cfg, err := ResolveConfig(Config{PoolSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinPoolSize != 32 {
		t.Fatalf("min pool size = %d", cfg.MinPoolSize)
	}
	if cfg.MaxPoolSize != 128 {
		t.Fatalf("max pool size = %d", cfg.MaxPoolSize)
	}
}

func TestResolveConfigPoolAutoscaleOffUsesFixedPool(t *testing.T) {
	cfg, err := ResolveConfig(Config{PoolSize: 8, PoolAutoscale: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PoolAutoscale {
		t.Fatal("expected autoscale off")
	}
	if cfg.MinPoolSize != 8 || cfg.MaxPoolSize != 8 {
		t.Fatalf("pool bounds = %d %d", cfg.MinPoolSize, cfg.MaxPoolSize)
	}
}

func TestResolveConfigRejectsInvalidPoolConfig(t *testing.T) {
	tests := []Config{
		{PoolAutoscale: "maybe"},
		{PoolSize: -1},
		{MinPoolSize: -1},
		{MaxPoolSize: -1},
		{MinPoolSize: 16, MaxPoolSize: 8},
	}
	for _, cfg := range tests {
		if _, err := ResolveConfig(cfg); err == nil {
			t.Fatalf("expected error for config %#v", cfg)
		}
	}
}
