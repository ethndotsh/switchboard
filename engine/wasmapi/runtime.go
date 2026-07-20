package wasmapi

import (
	"context"
	"errors"
	"time"

	"github.com/ethndotsh/switchboard"
)

// ErrInvalidAction marks a guest invocation whose output violated host
// limits or validation rules; it flows through the operator's fail_mode.
var ErrInvalidAction = errors.New("switchboard rule produced invalid action")

type RuntimeOptions struct {
	CloseOnContextDone bool
	MemoryLimitPages   uint32
	CacheDir           string
}

type InvokeLimits struct {
	Timeout         time.Duration
	MaxActionBytes  int
	MaxHeaderOps    int
	MaxResponseBody int
	// MaxDataBytes bounds the total size of a bundle's data artifacts. It is
	// an activation-time gate, not a per-invocation limit.
	MaxDataBytes int
}

type WasmRuntime interface {
	// Compile builds a rule from its module bytes. data holds read-only
	// bundled data files (keyed by artifact name) exposed to the guest at
	// invocation time; it is immutable for the life of the compiled rule.
	Compile(ctx context.Context, module []byte, data map[string][]byte) (CompiledRule, error)
	Close(ctx context.Context) error
}

type CompiledRule interface {
	Instantiate(ctx context.Context) (RuleInstance, error)
	Close(ctx context.Context) error
}

type RuleInstance interface {
	Invoke(ctx context.Context, entrypoint string, req switchboard.Request, limits InvokeLimits) (switchboard.Action, error)
	Close(ctx context.Context) error
}
