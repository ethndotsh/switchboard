package wasmapi

import (
	"context"
	"time"

	"github.com/ethndotsh/switchboard"
)

type WasmRuntime interface {
	Compile(ctx context.Context, module []byte) (CompiledRule, error)
	Close(ctx context.Context) error
}

type CompiledRule interface {
	Instantiate(ctx context.Context) (RuleInstance, error)
	Close(ctx context.Context) error
}

type RuleInstance interface {
	Invoke(ctx context.Context, entrypoint string, req switchboard.Request, timeout time.Duration) (switchboard.Action, error)
	Close(ctx context.Context) error
}
