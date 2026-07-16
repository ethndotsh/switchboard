package engine

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/registry"
)

func TestReconcilerActivatesS3Bundle(t *testing.T) {
	if os.Getenv("SWITCHBOARD_S3_ENDPOINT") == "" ||
		os.Getenv("SWITCHBOARD_S3_ACCESS_KEY") == "" ||
		os.Getenv("SWITCHBOARD_S3_SECRET_KEY") == "" ||
		os.Getenv("SWITCHBOARD_S3_BUCKET") == "" {
		t.Skip("S3 registry env vars not present")
	}

	ctx := context.Background()
	reg, err := registry.NewS3(ctx, registry.S3ConfigFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	wasmRuntime, err := NewWasmRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer wasmRuntime.Close(ctx)

	manager := &RuntimeManager{}
	reconciler := &Reconciler{
		registry:  reg,
		manager:   manager,
		runtime:   wasmRuntime,
		namespace: os.Getenv("SWITCHBOARD_NAMESPACE"),
		channel:   "prod",
		limits:    InvokeLimits{Timeout: 500 * time.Millisecond},
		poolCfg:   PoolConfig{MinSize: 2, MaxSize: 2, Autoscale: false},
	}
	reconciler.reconcile(ctx)

	active := manager.Current()
	if active == nil {
		t.Fatal("expected reconciler to activate runtime")
	}
	action, err := active.Invoke(ctx, switchboard.Request{Path: "/blocked", Method: "GET", Headers: map[string][]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if action.Decision != switchboard.DecisionDeny || action.Response.Status != 403 {
		t.Fatalf("action = %#v", action)
	}
}
