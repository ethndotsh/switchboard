package engine

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/engine/wasmapi"
	"github.com/ethndotsh/switchboard/internal/bundle"
	"go.uber.org/zap"
)

const DefaultPoolSize = 16

type WasmRuntime = wasmapi.WasmRuntime
type CompiledRule = wasmapi.CompiledRule
type RuleInstance = wasmapi.RuleInstance

type Runtime struct {
	id          string
	manifest    bundle.Manifest
	wasmRuntime WasmRuntime
	module      CompiledRule
	timeout     time.Duration
	pool        chan RuleInstance
	closed      atomic.Bool
	logger      *zap.Logger

	minPoolSize       int
	maxPoolSize       int
	autoscale         bool
	scaleInterval     time.Duration
	idleWindowLimit   int
	idleWindows       int
	scaleCancel       context.CancelFunc
	targetPoolSize    atomic.Int64
	totalInstances    atomic.Int64
	inflight          atomic.Int64
	inflightHighWater atomic.Int64
	exhaustions       atomic.Int64
}

type RuntimeManager struct {
	active   atomic.Pointer[Runtime]
	lastGood atomic.Pointer[Runtime]
}

func (m *RuntimeManager) Current() *Runtime {
	return m.active.Load()
}

func (m *RuntimeManager) LastGood() *Runtime {
	return m.lastGood.Load()
}

func (m *RuntimeManager) TryActivate(ctx context.Context, candidate *Runtime) error {
	if err := candidate.Validate(ctx); err != nil {
		return err
	}
	m.Activate(candidate)
	return nil
}

func (m *RuntimeManager) Activate(candidate *Runtime) {
	previous := m.active.Load()
	if previous != nil {
		m.lastGood.Store(previous)
	}
	m.active.Store(candidate)
}

func (m *RuntimeManager) Close(ctx context.Context) error {
	seen := map[*Runtime]bool{}
	for _, runtime := range []*Runtime{m.active.Load(), m.lastGood.Load()} {
		if runtime == nil || seen[runtime] {
			continue
		}
		seen[runtime] = true
		if err := runtime.Close(ctx); err != nil {
			return err
		}
	}
	return nil
}

func NewRuntime(ctx context.Context, wasmRuntime WasmRuntime, b bundle.Bundle, timeout time.Duration, poolSize int) (*Runtime, error) {
	return NewRuntimeWithPoolConfig(ctx, wasmRuntime, b, timeout, PoolConfig{MinSize: poolSize, MaxSize: poolSize, Autoscale: false}, nil)
}

func NewRuntimeWithPoolConfig(ctx context.Context, wasmRuntime WasmRuntime, b bundle.Bundle, timeout time.Duration, poolCfg PoolConfig, logger *zap.Logger) (*Runtime, error) {
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}
	minPoolSize := poolCfg.MinSize
	if minPoolSize <= 0 {
		minPoolSize = DefaultPoolSize
	}
	maxPoolSize := poolCfg.MaxSize
	if maxPoolSize <= 0 {
		maxPoolSize = minPoolSize
	}
	if maxPoolSize < minPoolSize {
		maxPoolSize = minPoolSize
	}
	compiled, err := wasmRuntime.Compile(ctx, b.Module)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		id:          b.ID,
		manifest:    b.Manifest,
		wasmRuntime: wasmRuntime,
		module:      compiled,
		timeout:     timeout,
		pool:        make(chan RuleInstance, maxPoolSize),
		logger:      logger,
		minPoolSize: minPoolSize,
		maxPoolSize: maxPoolSize,
		autoscale:   poolCfg.Autoscale && maxPoolSize > minPoolSize,

		scaleInterval:   defaultPoolScaleInterval,
		idleWindowLimit: defaultPoolIdleWindows,
	}
	runtime.targetPoolSize.Store(int64(minPoolSize))
	if err := runtime.Warm(ctx, minPoolSize); err != nil {
		_ = runtime.Close(ctx)
		return nil, err
	}
	if runtime.autoscale {
		scaleCtx, cancel := context.WithCancel(context.Background())
		runtime.scaleCancel = cancel
		go runtime.runPoolController(scaleCtx)
	}
	return runtime, nil
}

func (r *Runtime) ID() string {
	if r == nil {
		return ""
	}
	return r.id
}

func (r *Runtime) Validate(ctx context.Context) error {
	_, err := r.Invoke(ctx, switchboard.Request{Method: http.MethodGet, Path: "/__switchboard_validate", Headers: map[string][]string{}})
	return err
}

func (r *Runtime) Invoke(ctx context.Context, req switchboard.Request) (switchboard.Action, error) {
	mod, err := r.acquireModule(ctx)
	if err != nil {
		return switchboard.Action{}, err
	}
	healthy := false
	defer func() {
		r.releaseModule(context.Background(), mod, healthy)
	}()

	action, err := mod.Invoke(ctx, r.manifest.Entrypoint, req, r.timeout)
	if err != nil {
		return switchboard.Action{}, err
	}
	healthy = true
	if action.Type == "" {
		action.Type = switchboard.ActionNext
	}
	return action, nil
}

func (r *Runtime) Close(ctx context.Context) error {
	if r == nil || r.closed.Swap(true) {
		return nil
	}
	if r.scaleCancel != nil {
		r.scaleCancel()
	}
	for {
		select {
		case mod := <-r.pool:
			_ = mod.Close(ctx)
			r.totalInstances.Add(-1)
		default:
			return r.module.Close(ctx)
		}
	}
}
