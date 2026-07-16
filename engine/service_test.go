package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/ethndotsh/switchboard"
)

func fallbackTestRequest() switchboard.Request {
	return switchboard.Request{
		Method:  "GET",
		Path:    "/blocked",
		Headers: map[string][]string{},
	}
}

// newFailingTestRuntime builds a runtime whose every Invoke fails at pool
// acquire: the pool is empty and the module cannot instantiate replacements.
func newFailingTestRuntime(id string) *Runtime {
	return newIdleTestRuntime(id)
}

func TestInvokeWithFallbackNoActiveRuntimeFailOpen(t *testing.T) {
	s := &Service{manager: &RuntimeManager{}, failMode: FailModeOpen}
	_, err := s.InvokeWithFallback(context.Background(), fallbackTestRequest())
	if !errors.Is(err, ErrNoActiveRuntime) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvokeWithFallbackNilService(t *testing.T) {
	var s *Service
	if _, err := s.InvokeWithFallback(context.Background(), fallbackTestRequest()); !errors.Is(err, ErrNoActiveRuntime) {
		t.Fatalf("nil service err = %v", err)
	}
	empty := &Service{}
	if _, err := empty.InvokeWithFallback(context.Background(), fallbackTestRequest()); !errors.Is(err, ErrNoActiveRuntime) {
		t.Fatalf("nil manager err = %v", err)
	}
}

func TestInvokeWithFallbackActiveFailsFailOpen(t *testing.T) {
	manager := &RuntimeManager{}
	manager.active.Store(newFailingTestRuntime("broken"))
	s := &Service{manager: manager, failMode: FailModeOpen}

	result, err := s.InvokeWithFallback(context.Background(), fallbackTestRequest())
	if !errors.Is(err, ErrRuntimePoolExhausted) {
		t.Fatalf("err = %v", err)
	}
	if result.UsedLastGood {
		t.Fatal("fail_mode open consulted last-good runtime")
	}
	if result.BundleID != "broken" {
		t.Fatalf("bundle id = %q", result.BundleID)
	}
}

func TestInvokeWithFallbackLastGoodHealthy(t *testing.T) {
	lastGood, ctx, cleanup := loadRuntimeForTest(t, 2)
	defer cleanup()

	manager := &RuntimeManager{}
	manager.active.Store(newFailingTestRuntime("broken"))
	manager.lastGood.Store(lastGood)
	s := &Service{manager: manager, failMode: FailModeLastGood}

	result, err := s.InvokeWithFallback(ctx, fallbackTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !result.UsedLastGood {
		t.Fatal("expected UsedLastGood")
	}
	if result.BundleID != lastGood.ID() {
		t.Fatalf("bundle id = %q, want %q", result.BundleID, lastGood.ID())
	}
	if result.Action.Decision != switchboard.DecisionDeny || result.Action.Response.Status != 403 {
		t.Fatalf("action = %#v", result.Action)
	}
}

func TestInvokeWithFallbackHealthyActiveNeverFallsBack(t *testing.T) {
	active, ctx, cleanup := loadRuntimeForTest(t, 2)
	defer cleanup()

	manager := &RuntimeManager{}
	manager.active.Store(active)
	manager.lastGood.Store(newFailingTestRuntime("stale"))
	s := &Service{manager: manager, failMode: FailModeLastGood}

	result, err := s.InvokeWithFallback(ctx, fallbackTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.UsedLastGood || result.BundleID != active.ID() {
		t.Fatalf("result = %#v", result)
	}
}

func TestInvokeWithFallbackNoActiveUsesLastGood(t *testing.T) {
	lastGood, ctx, cleanup := loadRuntimeForTest(t, 2)
	defer cleanup()

	manager := &RuntimeManager{}
	manager.lastGood.Store(lastGood)
	s := &Service{manager: manager, failMode: FailModeLastGood}

	result, err := s.InvokeWithFallback(ctx, fallbackTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !result.UsedLastGood || result.BundleID != lastGood.ID() {
		t.Fatalf("result = %#v", result)
	}
	if result.Action.Decision != switchboard.DecisionDeny {
		t.Fatalf("action = %#v", result.Action)
	}
}

func TestInvokeWithFallbackLastGoodNil(t *testing.T) {
	manager := &RuntimeManager{}
	manager.active.Store(newFailingTestRuntime("broken"))
	s := &Service{manager: manager, failMode: FailModeLastGood}

	result, err := s.InvokeWithFallback(context.Background(), fallbackTestRequest())
	if !errors.Is(err, ErrRuntimePoolExhausted) {
		t.Fatalf("err = %v", err)
	}
	if result.UsedLastGood {
		t.Fatal("UsedLastGood set without a last-good runtime")
	}
}

func TestInvokeWithFallbackLastGoodClosed(t *testing.T) {
	ctx := context.Background()
	manager := &RuntimeManager{}
	manager.active.Store(newFailingTestRuntime("broken"))
	lastGood := newIdleTestRuntime("old")
	if err := lastGood.Close(ctx); err != nil {
		t.Fatal(err)
	}
	manager.lastGood.Store(lastGood)
	s := &Service{manager: manager, failMode: FailModeLastGood}

	result, err := s.InvokeWithFallback(ctx, fallbackTestRequest())
	if !errors.Is(err, ErrRuntimePoolExhausted) {
		t.Fatalf("err = %v", err)
	}
	if result.UsedLastGood {
		t.Fatal("closed last-good runtime was used")
	}
}

func TestInvokeWithFallbackLastGoodIsActive(t *testing.T) {
	manager := &RuntimeManager{}
	broken := newFailingTestRuntime("broken")
	manager.active.Store(broken)
	manager.lastGood.Store(broken)
	s := &Service{manager: manager, failMode: FailModeLastGood}

	result, err := s.InvokeWithFallback(context.Background(), fallbackTestRequest())
	if !errors.Is(err, ErrRuntimePoolExhausted) {
		t.Fatalf("err = %v", err)
	}
	if result.UsedLastGood {
		t.Fatal("last-good identical to active was retried")
	}
}

func TestInvokeWithFallbackLastGoodAlsoFails(t *testing.T) {
	manager := &RuntimeManager{}
	manager.active.Store(newFailingTestRuntime("broken"))
	manager.lastGood.Store(newFailingTestRuntime("also-broken"))
	s := &Service{manager: manager, failMode: FailModeLastGood}

	result, err := s.InvokeWithFallback(context.Background(), fallbackTestRequest())
	if !errors.Is(err, ErrRuntimePoolExhausted) {
		t.Fatalf("err = %v", err)
	}
	if result.UsedLastGood {
		t.Fatal("failed fallback runtime reported as used")
	}
	if result.BundleID != "broken" {
		t.Fatalf("bundle id = %q, want the active runtime's", result.BundleID)
	}
}
