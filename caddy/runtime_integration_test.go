package caddy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/engine"
	"github.com/ethndotsh/switchboard/internal/bundle"
)

func TestHandlerInvokesBuiltBundle(t *testing.T) {
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
	wasmRuntime, err := engine.NewWasmRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer wasmRuntime.Close(ctx)

	runtime, err := engine.NewRuntime(ctx, wasmRuntime, bundle.Bundle{
		ID:       manifest.Version,
		Module:   module,
		Manifest: manifest,
		Checksum: checksum,
	}, 500*time.Millisecond, 2)
	if err != nil {
		t.Fatal(err)
	}
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

	handler := &Switchboard{FailMode: "open", manager: &engine.RuntimeManager{}}
	handler.manager.Activate(runtime)

	blockedReq := httptest.NewRequest(http.MethodGet, "/blocked", nil)
	blockedRes := httptest.NewRecorder()
	if err := handler.ServeHTTP(blockedRes, blockedReq, &nextHandler{}); err != nil {
		t.Fatal(err)
	}
	if blockedRes.Code != http.StatusForbidden {
		t.Fatalf("blocked status = %d", blockedRes.Code)
	}

	passReq := httptest.NewRequest(http.MethodGet, "/", nil)
	passRes := httptest.NewRecorder()
	nextHandler := &nextHandler{}
	if err := handler.ServeHTTP(passRes, passReq, nextHandler); err != nil {
		t.Fatal(err)
	}
	if !nextHandler.called {
		t.Fatal("expected handler to call next")
	}
	if passReq.Header.Get("x-switchboard-rule") != "basic" {
		t.Fatalf("header was not applied: %q", passReq.Header.Get("x-switchboard-rule"))
	}
}

func TestHandlerInvocationErrorUsesFailMode(t *testing.T) {
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

	ctx := context.Background()
	wasmRuntime, err := engine.NewWasmRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer wasmRuntime.Close(ctx)

	runtime, err := engine.NewRuntime(ctx, wasmRuntime, bundle.Bundle{
		ID:       manifest.Version,
		Module:   module,
		Manifest: manifest,
		Checksum: checksum,
	}, 500*time.Millisecond, 1)
	if err != nil {
		t.Fatal(err)
	}
	_ = runtime.Close(ctx)

	failOpen := &Switchboard{FailMode: "open", manager: &engine.RuntimeManager{}}
	failOpen.manager.Activate(runtime)
	next := &nextHandler{}
	if err := failOpen.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), next); err != nil {
		t.Fatal(err)
	}
	if !next.called {
		t.Fatal("expected fail-open invocation error to call next")
	}

	failClosed := &Switchboard{FailMode: "closed", manager: &engine.RuntimeManager{}}
	failClosed.manager.Activate(runtime)
	res := httptest.NewRecorder()
	if err := failClosed.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil), &nextHandler{}); err != nil {
		t.Fatal(err)
	}
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("fail-closed status = %d", res.Code)
	}
}
