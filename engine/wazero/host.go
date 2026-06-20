package wazero

import (
	"context"
	"net/textproto"
	"strings"

	"github.com/ethndotsh/switchboard"
	wazeroapi "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

func instantiateSwitchboardHostModule(ctx context.Context, runtime wazeroapi.Runtime) error {
	builder := runtime.NewHostModuleBuilder("switchboard")
	exportRequestFunctions(builder)
	exportActionFunctions(builder)
	_, err := builder.Instantiate(ctx)
	return err
}

func exportRequestFunctions(builder wazeroapi.HostModuleBuilder) {
	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context) uint32 {
		state := invocationFromContext(ctx)
		if state == nil {
			return 0
		}
		return uint32(len(state.request.Method))
	}).Export("request_method_len")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, ptr uint32) {
		state := invocationFromContext(ctx)
		if state == nil || state.request.Method == "" {
			return
		}
		_ = mod.Memory().Write(ptr, []byte(state.request.Method))
	}).Export("read_request_method")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context) uint32 {
		state := invocationFromContext(ctx)
		if state == nil {
			return 0
		}
		return uint32(len(state.request.Path))
	}).Export("request_path_len")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, ptr uint32) {
		state := invocationFromContext(ctx)
		if state == nil || state.request.Path == "" {
			return
		}
		_ = mod.Memory().Write(ptr, []byte(state.request.Path))
	}).Export("read_request_path")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32) uint32 {
		state := invocationFromContext(ctx)
		if state == nil {
			return 0
		}
		name, ok := readGuestString(mod, namePtr, nameLen)
		if !ok {
			return 0
		}
		return uint32(len(headerValues(state.request.Headers, name)))
	}).Export("request_header_value_count")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32, index uint32) uint32 {
		state := invocationFromContext(ctx)
		if state == nil {
			return 0
		}
		name, ok := readGuestString(mod, namePtr, nameLen)
		if !ok {
			return 0
		}
		values := headerValues(state.request.Headers, name)
		if int(index) >= len(values) {
			return 0
		}
		return uint32(len(values[index]))
	}).Export("request_header_value_len")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32, index uint32, valuePtr uint32) {
		state := invocationFromContext(ctx)
		if state == nil {
			return
		}
		name, ok := readGuestString(mod, namePtr, nameLen)
		if !ok {
			return
		}
		values := headerValues(state.request.Headers, name)
		if int(index) >= len(values) {
			return
		}
		_ = mod.Memory().Write(valuePtr, []byte(values[index]))
	}).Export("read_request_header_value")
}

func exportActionFunctions(builder wazeroapi.HostModuleBuilder) {
	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context) {
		if state := invocationFromContext(ctx); state != nil {
			state.action.Type = switchboard.ActionNext
		}
	}).Export("action_next")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, status int32) {
		if state := invocationFromContext(ctx); state != nil {
			state.action.Type = switchboard.ActionDeny
			state.action.StatusCode = int(status)
		}
	}).Export("action_deny")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, status int32, locationPtr uint32, locationLen uint32) {
		state := invocationFromContext(ctx)
		if state == nil {
			return
		}
		location, ok := readGuestString(mod, locationPtr, locationLen)
		if !ok {
			return
		}
		state.action.Type = switchboard.ActionRedirect
		state.action.StatusCode = int(status)
		state.action.Location = location
	}).Export("action_redirect")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, pathPtr uint32, pathLen uint32) {
		state := invocationFromContext(ctx)
		if state == nil {
			return
		}
		path, ok := readGuestString(mod, pathPtr, pathLen)
		if !ok {
			return
		}
		state.action.Type = switchboard.ActionRewrite
		state.action.RewritePath = path
	}).Export("action_rewrite")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32, valuePtr uint32, valueLen uint32) {
		appendHeaderOp(ctx, mod, switchboard.HeaderOpSet, namePtr, nameLen, valuePtr, valueLen)
	}).Export("action_header_set")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32, valuePtr uint32, valueLen uint32) {
		appendHeaderOp(ctx, mod, switchboard.HeaderOpAdd, namePtr, nameLen, valuePtr, valueLen)
	}).Export("action_header_add")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32) {
		state := invocationFromContext(ctx)
		if state == nil {
			return
		}
		name, ok := readGuestString(mod, namePtr, nameLen)
		if !ok {
			return
		}
		state.action.HeaderOps = append(state.action.HeaderOps, switchboard.HeaderOp{Op: switchboard.HeaderOpDelete, Name: name})
	}).Export("action_header_delete")
}

func invocationFromContext(ctx context.Context) *invocationState {
	state, _ := ctx.Value(invocationStateKey{}).(*invocationState)
	return state
}

func readGuestString(mod api.Module, ptr uint32, length uint32) (string, bool) {
	if length == 0 {
		return "", true
	}
	data, ok := mod.Memory().Read(ptr, length)
	if !ok {
		return "", false
	}
	return string(data), true
}

func appendHeaderOp(ctx context.Context, mod api.Module, op switchboard.HeaderOpType, namePtr uint32, nameLen uint32, valuePtr uint32, valueLen uint32) {
	state := invocationFromContext(ctx)
	if state == nil {
		return
	}
	name, ok := readGuestString(mod, namePtr, nameLen)
	if !ok {
		return
	}
	value, ok := readGuestString(mod, valuePtr, valueLen)
	if !ok {
		return
	}
	state.action.HeaderOps = append(state.action.HeaderOps, switchboard.HeaderOp{Op: op, Name: name, Value: value})
}

func headerValues(headers map[string][]string, name string) []string {
	if len(headers) == 0 || name == "" {
		return nil
	}
	if values, ok := headers[name]; ok {
		return values
	}
	if values, ok := headers[textproto.CanonicalMIMEHeaderKey(name)]; ok {
		return values
	}
	for key, values := range headers {
		if strings.EqualFold(key, name) {
			return values
		}
	}
	return nil
}
