package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type wazeroRuntime struct {
	runtime wazero.Runtime
}

type wazeroCompiledRule struct {
	runtime wazero.Runtime
	module  wazero.CompiledModule
}

type wazeroInstance struct {
	module     api.Module
	handleName string
	handle     api.Function
	actionData []byte
}

type invocationState struct {
	requestData []byte
	instance    *wazeroInstance
}

type invocationStateKey struct{}

func NewWasmRuntime(ctx context.Context) (WasmRuntime, error) {
	return NewWazeroRuntime(ctx)
}

func NewWazeroRuntime(ctx context.Context) (WasmRuntime, error) {
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
		if state == nil || state.instance == nil {
			return
		}
		if length == 0 {
			state.instance.actionData = nil
			return
		}
		data, ok := mod.Memory().Read(ptr, length)
		if ok {
			state.instance.actionData = append(state.instance.actionData[:0], data...)
		}
	}).Export("write_action").
		Instantiate(ctx); err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, err
	}
	return &wazeroRuntime{runtime: wasmRuntime}, nil
}

func (r *wazeroRuntime) Compile(ctx context.Context, module []byte) (CompiledRule, error) {
	compiled, err := r.runtime.CompileModule(ctx, module)
	if err != nil {
		return nil, err
	}
	return &wazeroCompiledRule{runtime: r.runtime, module: compiled}, nil
}

func (r *wazeroRuntime) Close(ctx context.Context) error {
	if r == nil || r.runtime == nil {
		return nil
	}
	return r.runtime.Close(ctx)
}

func (r *wazeroCompiledRule) Instantiate(ctx context.Context) (RuleInstance, error) {
	mod, err := r.runtime.InstantiateModule(ctx, r.module, wazero.NewModuleConfig().WithName("").WithStartFunctions())
	if err != nil {
		return nil, err
	}
	return &wazeroInstance{module: mod}, nil
}

func (r *wazeroCompiledRule) Close(ctx context.Context) error {
	if r == nil || r.module == nil {
		return nil
	}
	return r.module.Close(ctx)
}

func (i *wazeroInstance) Invoke(ctx context.Context, entrypoint string, requestData []byte, timeout time.Duration) ([]byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if i.handle == nil || i.handleName != entrypoint {
		handle := i.module.ExportedFunction(entrypoint)
		if handle == nil {
			return nil, fmt.Errorf("entrypoint %q not found", entrypoint)
		}
		i.handle = handle
		i.handleName = entrypoint
	}
	i.actionData = i.actionData[:0]
	ctx = context.WithValue(ctx, invocationStateKey{}, &invocationState{requestData: requestData, instance: i})
	results, err := i.handle.Call(ctx)
	if err != nil {
		return nil, err
	}
	if len(results) > 0 && results[0] != 0 {
		return nil, fmt.Errorf("rule returned error code %d", results[0])
	}
	return i.actionData, nil
}

func (i *wazeroInstance) Close(ctx context.Context) error {
	if i == nil || i.module == nil {
		return nil
	}
	return i.module.Close(ctx)
}
