package engine

import (
	"context"

	wazeroruntime "github.com/ethndotsh/switchboard/engine/wazero"
)

func NewWasmRuntime(ctx context.Context) (WasmRuntime, error) {
	return NewWazeroRuntime(ctx)
}

func NewWazeroRuntime(ctx context.Context) (WasmRuntime, error) {
	return wazeroruntime.NewRuntime(ctx)
}
