package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const DefaultPoolSize = 16

var ErrRuntimePoolExhausted = errors.New("switchboard runtime pool exhausted")

type Runtime struct {
	id          string
	manifest    bundle.Manifest
	wasmRuntime wazero.Runtime
	module      wazero.CompiledModule
	timeout     time.Duration
	pool        chan api.Module
	closed      atomic.Bool
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

type invocationState struct {
	requestData []byte
	actionData  []byte
}

type invocationStateKey struct{}

func NewWasmRuntime(ctx context.Context) (wazero.Runtime, error) {
	wasmRuntime := wazero.NewRuntime(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, wasmRuntime); err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, err
	}
	if _, err := wasmRuntime.NewHostModuleBuilder("switchboard").
		NewFunctionBuilder().WithFunc(func(ctx context.Context) uint32 {
		state, _ := ctx.Value(invocationStateKey{}).(*invocationState)
		if state == nil {
			return 0
		}
		return uint32(len(state.requestData))
	}).Export("request_len").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, ptr uint32) {
		state, _ := ctx.Value(invocationStateKey{}).(*invocationState)
		if state == nil || len(state.requestData) == 0 {
			return
		}
		_ = mod.Memory().Write(ptr, state.requestData)
	}).Export("read_request").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, ptr uint32, length uint32) {
		state, _ := ctx.Value(invocationStateKey{}).(*invocationState)
		if state == nil {
			return
		}
		if length == 0 {
			state.actionData = nil
			return
		}
		data, ok := mod.Memory().Read(ptr, length)
		if ok {
			state.actionData = append(state.actionData[:0], data...)
		}
	}).Export("write_action").
		Instantiate(ctx); err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, err
	}
	return wasmRuntime, nil
}

func NewRuntime(ctx context.Context, wasmRuntime wazero.Runtime, b bundle.Bundle, timeout time.Duration, poolSize int) (*Runtime, error) {
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}
	if poolSize <= 0 {
		poolSize = DefaultPoolSize
	}
	compiled, err := wasmRuntime.CompileModule(ctx, b.Module)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		id:          b.ID,
		manifest:    b.Manifest,
		wasmRuntime: wasmRuntime,
		module:      compiled,
		timeout:     timeout,
		pool:        make(chan api.Module, poolSize),
	}
	if err := runtime.Warm(ctx, poolSize); err != nil {
		_ = runtime.Close(ctx)
		return nil, err
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

func (r *Runtime) PoolSize() int {
	if r == nil || r.pool == nil {
		return 0
	}
	return cap(r.pool)
}

func (r *Runtime) Warm(ctx context.Context, count int) error {
	if r.closed.Load() {
		return fmt.Errorf("runtime %s is closed", r.id)
	}
	for i := 0; i < count; i++ {
		mod, err := r.wasmRuntime.InstantiateModule(ctx, r.module, wazero.NewModuleConfig().WithName("").WithStartFunctions())
		if err != nil {
			return err
		}
		select {
		case r.pool <- mod:
		default:
			_ = mod.Close(ctx)
			return nil
		}
	}
	return nil
}

func (r *Runtime) Invoke(ctx context.Context, req switchboard.Request) (switchboard.Action, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	reqData, err := json.Marshal(req)
	if err != nil {
		return switchboard.Action{}, err
	}

	mod, err := r.acquireModule(ctx)
	if err != nil {
		return switchboard.Action{}, err
	}
	healthy := false
	defer func() {
		r.releaseModule(context.Background(), mod, healthy)
	}()

	handle := mod.ExportedFunction(r.manifest.Entrypoint)
	if handle == nil {
		return switchboard.Action{}, fmt.Errorf("entrypoint %q not found", r.manifest.Entrypoint)
	}
	state := &invocationState{requestData: reqData}
	ctx = context.WithValue(ctx, invocationStateKey{}, state)
	results, err := handle.Call(ctx)
	if err != nil {
		return switchboard.Action{}, err
	}
	if len(results) > 0 && results[0] != 0 {
		return switchboard.Action{}, fmt.Errorf("rule returned error code %d", results[0])
	}
	healthy = true
	if len(state.actionData) == 0 {
		return switchboard.Action{Type: switchboard.ActionNext}, nil
	}
	var action switchboard.Action
	if err := json.Unmarshal(state.actionData, &action); err != nil {
		return switchboard.Action{}, err
	}
	if action.Type == "" {
		action.Type = switchboard.ActionNext
	}
	return action, nil
}

func (r *Runtime) acquireModule(ctx context.Context) (api.Module, error) {
	if r.closed.Load() {
		return nil, fmt.Errorf("runtime %s is closed", r.id)
	}
	select {
	case mod := <-r.pool:
		return mod, nil
	default:
		return nil, ErrRuntimePoolExhausted
	}
}

func (r *Runtime) releaseModule(ctx context.Context, mod api.Module, healthy bool) {
	if mod == nil {
		return
	}
	if !healthy || r.closed.Load() {
		_ = mod.Close(ctx)
		return
	}
	select {
	case r.pool <- mod:
	default:
		_ = mod.Close(ctx)
	}
}

func (r *Runtime) Close(ctx context.Context) error {
	if r == nil || r.closed.Swap(true) {
		return nil
	}
	for {
		select {
		case mod := <-r.pool:
			_ = mod.Close(ctx)
		default:
			return r.module.Close(ctx)
		}
	}
}
