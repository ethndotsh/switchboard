package caddy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/ethndotsh/switchboard/engine"
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
		fail_mode closed
		invoke_timeout 75ms
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
	if handler.FailMode != "closed" {
		t.Fatalf("fail mode = %q", handler.FailMode)
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
