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

	httpadapter "github.com/ethndotsh/switchboard/adapters/http"
	"github.com/ethndotsh/switchboard/engine"
	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/prometheus/client_golang/prometheus"
)

// TestHandlerRecordsMetrics locks the Track 2 fix: the Caddy ServeHTTP path,
// which previously bypassed the shared middleware, now records switchboard_*
// invocation metrics.
func TestHandlerRecordsMetrics(t *testing.T) {
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
	manifest, err := bundle.ParseManifest(manifestData)
	if err != nil {
		t.Fatal(err)
	}
	checksumData, err := os.ReadFile(filepath.Join(dist, "checksum.txt"))
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
	}, engine.InvokeLimits{Timeout: 500 * time.Millisecond}, 1)
	if err != nil {
		t.Fatal(err)
	}

	reg := prometheus.NewRegistry()
	metrics, err := httpadapter.NewMetrics(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := &Switchboard{FailMode: "open", manager: &engine.RuntimeManager{}, metrics: metrics}
	handler.manager.Activate(runtime)

	req := httptest.NewRequest(http.MethodGet, "/blocked", nil)
	if err := handler.ServeHTTP(httptest.NewRecorder(), req, &nextHandler{}); err != nil {
		t.Fatal(err)
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, family := range families {
		if family.GetName() != "switchboard_invocations_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			total += metric.GetCounter().GetValue()
		}
	}
	if total == 0 {
		t.Fatal("expected switchboard_invocations_total to be recorded via the Caddy handler")
	}
}
