package caddy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/engine"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type nextHandler struct {
	called bool
}

func (n *nextHandler) ServeHTTP(http.ResponseWriter, *http.Request) error {
	n.called = true
	return nil
}

func TestHandlerFailOpenWithoutRuntime(t *testing.T) {
	handler := &Switchboard{FailMode: "open", manager: &engine.RuntimeManager{}}
	next := &nextHandler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	if err := handler.ServeHTTP(res, req, next); err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	if !next.called {
		t.Fatal("expected fail-open handler to call next")
	}
}

func TestHandlerFailClosedWithoutRuntime(t *testing.T) {
	handler := &Switchboard{FailMode: "closed", manager: &engine.RuntimeManager{}}
	next := &nextHandler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	if err := handler.ServeHTTP(res, req, next); err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	if next.called {
		t.Fatal("did not expect fail-closed handler to call next")
	}
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestUnmarshalCaddyfileParsesCurrentFields(t *testing.T) {
	dispenser := caddyfile.NewTestDispenser(`switchboard {
		registry s3 s3://bucket/prefix
		namespace customer-a
		channel preview
		poll_interval 5s
		fail_mode last_good
		fallback_fail_mode closed
		invoke_timeout 75ms
		memory_limit 64mb
		max_action_bytes 32kb
		max_header_ops 16
		max_response_body 4kb
		cache_dir /var/lib/switchboard
		bootstrap_from_cache on
		pool_size 32
		pool_autoscale off
		min_pool_size 16
		max_pool_size 64
	}`)
	var handler Switchboard
	if err := handler.UnmarshalCaddyfile(dispenser); err != nil {
		t.Fatal(err)
	}
	if handler.Registry != "s3" || handler.RegistryURL != "s3://bucket/prefix" {
		t.Fatalf("registry = %q url = %q", handler.Registry, handler.RegistryURL)
	}
	if handler.Channel != "preview" {
		t.Fatalf("channel = %q", handler.Channel)
	}
	if handler.Namespace != "customer-a" {
		t.Fatalf("namespace = %q", handler.Namespace)
	}
	if handler.PollInterval != "5s" || handler.InvokeTimeout != "75ms" {
		t.Fatalf("durations = %q %q", handler.PollInterval, handler.InvokeTimeout)
	}
	if handler.FailMode != "last_good" || handler.FallbackFailMode != "closed" {
		t.Fatalf("fail modes = %q %q", handler.FailMode, handler.FallbackFailMode)
	}
	if handler.MemoryLimit != "64mb" || handler.MaxActionBytes != "32kb" || handler.MaxHeaderOps != 16 || handler.MaxResponseBody != "4kb" {
		t.Fatalf("limits = %q %q %d %q", handler.MemoryLimit, handler.MaxActionBytes, handler.MaxHeaderOps, handler.MaxResponseBody)
	}
	if handler.CacheDir != "/var/lib/switchboard" || handler.BootstrapFromCache != "on" {
		t.Fatalf("cache = %q %q", handler.CacheDir, handler.BootstrapFromCache)
	}
	if handler.PoolSize != 32 {
		t.Fatalf("pool size = %d", handler.PoolSize)
	}
	if handler.PoolAutoscale != "off" {
		t.Fatalf("pool autoscale = %q", handler.PoolAutoscale)
	}
	if handler.MinPoolSize != 16 || handler.MaxPoolSize != 64 {
		t.Fatalf("pool bounds = %d %d", handler.MinPoolSize, handler.MaxPoolSize)
	}
}

func TestUnmarshalCaddyfileRejectsInvalidCombos(t *testing.T) {
	cases := map[string]string{
		"fallback without last_good": `switchboard {
			fallback_fail_mode closed
		}`,
		"invalid fail_mode": `switchboard {
			fail_mode sometimes
		}`,
		"invalid fallback value": `switchboard {
			fail_mode last_good
			fallback_fail_mode last_good
		}`,
		"bootstrap without cache_dir": `switchboard {
			bootstrap_from_cache on
		}`,
		"invalid bootstrap value": `switchboard {
			cache_dir /tmp/x
			bootstrap_from_cache maybe
		}`,
		"invalid max_header_ops": `switchboard {
			max_header_ops zero
		}`,
	}
	for name, input := range cases {
		var handler Switchboard
		if err := handler.UnmarshalCaddyfile(caddyfile.NewTestDispenser(input)); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestEffectiveFailMode(t *testing.T) {
	cases := []struct {
		failMode, fallback, want string
	}{
		{"", "", engine.FailModeOpen},
		{"open", "", engine.FailModeOpen},
		{"closed", "", engine.FailModeClosed},
		{"last_good", "", engine.FailModeOpen},
		{"last_good", "open", engine.FailModeOpen},
		{"last_good", "closed", engine.FailModeClosed},
	}
	for _, c := range cases {
		handler := &Switchboard{FailMode: c.failMode, FallbackFailMode: c.fallback}
		if got := handler.effectiveFailMode(); got != c.want {
			t.Fatalf("effectiveFailMode(%q, %q) = %q, want %q", c.failMode, c.fallback, got, c.want)
		}
	}
}

func TestUnmarshalCaddyfileRejectsInvalidPoolAutoscale(t *testing.T) {
	dispenser := caddyfile.NewTestDispenser(`switchboard {
		pool_autoscale maybe
	}`)
	var handler Switchboard
	if err := handler.UnmarshalCaddyfile(dispenser); err == nil {
		t.Fatal("expected invalid pool_autoscale error")
	}
}

func TestUnmarshalCaddyfileRejectsInvalidPoolBounds(t *testing.T) {
	dispenser := caddyfile.NewTestDispenser(`switchboard {
		min_pool_size 16
		max_pool_size 8
	}`)
	var handler Switchboard
	if err := handler.UnmarshalCaddyfile(dispenser); err == nil {
		t.Fatal("expected invalid pool bounds error")
	}
}

var _ caddyhttp.Handler = (*nextHandler)(nil)

func BenchmarkExposeDecision(b *testing.B) {
	handler := &Switchboard{logger: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), caddyhttp.VarsCtxKey, map[string]any{})
	ctx = context.WithValue(ctx, caddyhttp.ExtraLogFieldsCtxKey, new(caddyhttp.ExtraLogFields))
	req = req.WithContext(ctx)
	result := engine.InvokeResult{
		BundleID: "sha256-abc",
		Action: switchboard.Action{
			Decision: switchboard.DecisionNext,
			Metadata: map[string]string{"backend": "v2"},
			Reason:   "v2-canary",
		},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		handler.exposeDecision(req, result)
	}
}

func BenchmarkExposeDecisionInfoLevelLogger(b *testing.B) {
	core, _ := observer.New(zap.InfoLevel)
	handler := &Switchboard{logger: zap.New(core)}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), caddyhttp.VarsCtxKey, map[string]any{})
	req = req.WithContext(ctx)
	result := engine.InvokeResult{
		BundleID: "sha256-abc",
		Action:   switchboard.Action{Decision: switchboard.DecisionNext},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		handler.exposeDecision(req, result)
	}
}
