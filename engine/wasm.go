package engine

import (
	"context"

	"github.com/ethndotsh/switchboard/engine/wasmapi"
	wazeroruntime "github.com/ethndotsh/switchboard/engine/wazero"
)

type RuntimeOptions = wasmapi.RuntimeOptions

// NewWasmRuntime builds a wasm runtime with safe defaults for tests and
// embedders that do not need memory limits or a compilation cache.
func NewWasmRuntime(ctx context.Context) (WasmRuntime, error) {
	return NewWazeroRuntime(ctx, RuntimeOptions{CloseOnContextDone: true})
}

func NewWazeroRuntime(ctx context.Context, opts RuntimeOptions) (WasmRuntime, error) {
	return wazeroruntime.NewRuntime(ctx, opts)
}
