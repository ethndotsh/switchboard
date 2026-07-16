package wazero

import (
	"context"
	"fmt"
	"net/url"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/engine/wasmapi"
	wazeroapi "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type Runtime struct {
	runtime wazeroapi.Runtime
	cache   wazeroapi.CompilationCache
}

type CompiledRule struct {
	runtime wazeroapi.Runtime
	module  wazeroapi.CompiledModule
}

type Instance struct {
	module     api.Module
	handleName string
	handle     api.Function
}

type invocationState struct {
	request     switchboard.Request
	action      switchboard.Action
	limits      wasmapi.InvokeLimits
	queryOnce   bool
	queryValues url.Values
	actionBytes int
	headerOps   int
	violation   error
}

type invocationStateKey struct{}

func NewRuntime(ctx context.Context, opts wasmapi.RuntimeOptions) (wasmapi.WasmRuntime, error) {
	cfg := wazeroapi.NewRuntimeConfig().WithCloseOnContextDone(opts.CloseOnContextDone)
	if opts.MemoryLimitPages > 0 {
		cfg = cfg.WithMemoryLimitPages(opts.MemoryLimitPages)
	}
	var cache wazeroapi.CompilationCache
	if opts.CacheDir != "" {
		var err error
		cache, err = wazeroapi.NewCompilationCacheWithDir(opts.CacheDir)
		if err != nil {
			return nil, fmt.Errorf("create compilation cache: %w", err)
		}
		cfg = cfg.WithCompilationCache(cache)
	}
	wasmRuntime := wazeroapi.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, wasmRuntime); err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, err
	}
	if err := instantiateSwitchboardHostModule(ctx, wasmRuntime); err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, err
	}
	return &Runtime{runtime: wasmRuntime, cache: cache}, nil
}

func (r *Runtime) Compile(ctx context.Context, module []byte) (wasmapi.CompiledRule, error) {
	compiled, err := r.runtime.CompileModule(ctx, module)
	if err != nil {
		return nil, err
	}
	return &CompiledRule{runtime: r.runtime, module: compiled}, nil
}

func (r *Runtime) Close(ctx context.Context) error {
	if r == nil || r.runtime == nil {
		return nil
	}
	err := r.runtime.Close(ctx)
	if r.cache != nil {
		if cacheErr := r.cache.Close(ctx); err == nil {
			err = cacheErr
		}
	}
	return err
}

func (r *CompiledRule) Instantiate(ctx context.Context) (wasmapi.RuleInstance, error) {
	mod, err := r.runtime.InstantiateModule(ctx, r.module, wazeroapi.NewModuleConfig().WithName("").WithStartFunctions())
	if err != nil {
		return nil, err
	}
	return &Instance{module: mod}, nil
}

func (r *CompiledRule) Close(ctx context.Context) error {
	if r == nil || r.module == nil {
		return nil
	}
	return r.module.Close(ctx)
}

func (i *Instance) Invoke(ctx context.Context, entrypoint string, req switchboard.Request, limits wasmapi.InvokeLimits) (switchboard.Action, error) {
	if limits.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limits.Timeout)
		defer cancel()
	}
	if i.handle == nil || i.handleName != entrypoint {
		handle := i.module.ExportedFunction(entrypoint)
		if handle == nil {
			return switchboard.Action{}, fmt.Errorf("entrypoint %q not found", entrypoint)
		}
		i.handle = handle
		i.handleName = entrypoint
	}
	state := &invocationState{
		request: req,
		action:  switchboard.Action{Decision: switchboard.DecisionNext},
		limits:  limits,
	}
	ctx = context.WithValue(ctx, invocationStateKey{}, state)
	results, err := i.handle.Call(ctx)
	// A recorded violation is the more precise diagnosis than any trap it
	// caused downstream.
	if state.violation != nil {
		return switchboard.Action{}, fmt.Errorf("%w: %v", wasmapi.ErrInvalidAction, state.violation)
	}
	if err != nil {
		return switchboard.Action{}, err
	}
	if len(results) > 0 && results[0] != 0 {
		return switchboard.Action{}, fmt.Errorf("rule returned error code %d", results[0])
	}
	return state.action, nil
}

func (i *Instance) Close(ctx context.Context) error {
	if i == nil || i.module == nil {
		return nil
	}
	return i.module.Close(ctx)
}
