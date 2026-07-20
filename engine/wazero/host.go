package wazero

import (
	"context"

	"github.com/ethndotsh/switchboard/internal/bundle"
	wazeroapi "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// maxLookupNameLen bounds guest-supplied lookup keys; exceeding it reads as
// "not found" rather than a violation.
const maxLookupNameLen = 256

const maxMetadataEntries = 64

func instantiateSwitchboardHostModule(ctx context.Context, runtime wazeroapi.Runtime) error {
	builder := runtime.NewHostModuleBuilder("switchboard")
	exportRequestFunctions(builder)
	exportActionFunctions(builder)
	exportDataFunctions(builder)
	_, err := builder.Instantiate(ctx)
	return err
}

// exportDataFunctions serves read-only bundled data files to the guest. The
// guest names a file relative to the data dir (e.g. "allowlist.txt"); the host
// resolves it against the bundle's data artifacts. Values are immutable for
// the life of the instance, so the guest caches them after the first read.
func exportDataFunctions(builder wazeroapi.HostModuleBuilder) {
	lookup := func(state *invocationState, name string) []byte {
		if state == nil || state.data == nil {
			return nil
		}
		return state.data[bundle.DataPrefix+name]
	}

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32) uint32 {
		state := invocationFromContext(ctx)
		name, ok := readGuestString(mod, namePtr, nameLen, maxLookupNameLen)
		if !ok {
			return 0
		}
		return uint32(len(lookup(state, name)))
	}).Export("data_read_len")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32, valuePtr uint32) {
		state := invocationFromContext(ctx)
		name, ok := readGuestString(mod, namePtr, nameLen, maxLookupNameLen)
		if !ok {
			return
		}
		if value := lookup(state, name); len(value) > 0 {
			_ = mod.Memory().Write(valuePtr, value)
		}
	}).Export("read_data")
}

func exportRequestFunctions(builder wazeroapi.HostModuleBuilder) {
	exportStringField(builder, "method", func(s *invocationState) string { return s.request.Method })
	exportStringField(builder, "path", func(s *invocationState) string { return s.request.Path })
	exportStringField(builder, "host", func(s *invocationState) string { return s.request.Host })
	exportStringField(builder, "raw_query", func(s *invocationState) string { return s.request.RawQuery })
	exportStringField(builder, "scheme", func(s *invocationState) string { return s.request.Scheme })
	exportStringField(builder, "protocol", func(s *invocationState) string { return s.request.Protocol })
	exportStringField(builder, "remote_addr", func(s *invocationState) string { return s.request.RemoteAddr })
	exportStringField(builder, "client_ip", func(s *invocationState) string { return s.request.ClientIP })

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context) uint32 {
		state := invocationFromContext(ctx)
		if state == nil || !state.request.TLS {
			return 0
		}
		return 1
	}).Export("request_tls")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32) uint32 {
		state := invocationFromContext(ctx)
		if state == nil {
			return 0
		}
		name, ok := readGuestString(mod, namePtr, nameLen, maxLookupNameLen)
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
		name, ok := readGuestString(mod, namePtr, nameLen, maxLookupNameLen)
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
		name, ok := readGuestString(mod, namePtr, nameLen, maxLookupNameLen)
		if !ok {
			return
		}
		values := headerValues(state.request.Headers, name)
		if int(index) >= len(values) {
			return
		}
		_ = mod.Memory().Write(valuePtr, []byte(values[index]))
	}).Export("read_request_header_value")

	exportLookupField(builder, "request_query_value_len", "read_request_query_value", (*invocationState).queryValue)
	exportLookupField(builder, "request_cookie_len", "read_request_cookie", (*invocationState).cookieValue)
}

func exportStringField(builder wazeroapi.HostModuleBuilder, name string, get func(*invocationState) string) {
	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context) uint32 {
		state := invocationFromContext(ctx)
		if state == nil {
			return 0
		}
		return uint32(len(get(state)))
	}).Export("request_" + name + "_len")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, ptr uint32) {
		state := invocationFromContext(ctx)
		if state == nil {
			return
		}
		value := get(state)
		if value == "" {
			return
		}
		_ = mod.Memory().Write(ptr, []byte(value))
	}).Export("read_request_" + name)
}

func exportLookupField(builder wazeroapi.HostModuleBuilder, lenExport, readExport string, get func(*invocationState, string) string) {
	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32) uint32 {
		state := invocationFromContext(ctx)
		if state == nil {
			return 0
		}
		name, ok := readGuestString(mod, namePtr, nameLen, maxLookupNameLen)
		if !ok {
			return 0
		}
		return uint32(len(get(state, name)))
	}).Export(lenExport)

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32, valuePtr uint32) {
		state := invocationFromContext(ctx)
		if state == nil {
			return
		}
		name, ok := readGuestString(mod, namePtr, nameLen, maxLookupNameLen)
		if !ok {
			return
		}
		if value := get(state, name); value != "" {
			_ = mod.Memory().Write(valuePtr, []byte(value))
		}
	}).Export(readExport)
}
