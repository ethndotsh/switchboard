package wazero

import (
	"context"
	"strings"

	"github.com/ethndotsh/switchboard"
	wazeroapi "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type headerTarget int

const (
	headerTargetRequest headerTarget = iota
	headerTargetResponse
)

func exportActionFunctions(builder wazeroapi.HostModuleBuilder) {
	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context) {
		state := invocationFromContext(ctx)
		if state == nil || state.violation != nil {
			return
		}
		state.action.Decision = switchboard.DecisionNext
	}).Export("action_next")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, status int32) {
		state := invocationFromContext(ctx)
		if state == nil || state.violation != nil {
			return
		}
		if status < 100 || status > 599 {
			state.fail("deny status %d out of range 100-599", status)
			return
		}
		state.action.Decision = switchboard.DecisionDeny
		state.action.Response.Status = int(status)
	}).Export("action_deny")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, status int32, locationPtr uint32, locationLen uint32) {
		state := invocationFromContext(ctx)
		if state == nil || state.violation != nil {
			return
		}
		if status < 300 || status > 399 {
			state.fail("redirect status %d out of range 300-399", status)
			return
		}
		location, ok := readGuestString(mod, locationPtr, locationLen, 0)
		if !ok {
			state.fail("redirect location out of guest memory bounds")
			return
		}
		if !validTextValue(location) || location == "" {
			state.fail("redirect location is empty or contains control characters")
			return
		}
		if !state.chargeBytes(len(location)) {
			return
		}
		state.action.Decision = switchboard.DecisionRedirect
		state.action.Response.Status = int(status)
		state.action.Response.Location = location
	}).Export("action_redirect")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context) {
		state := invocationFromContext(ctx)
		if state == nil || state.violation != nil {
			return
		}
		state.action.Decision = switchboard.DecisionRewrite
	}).Export("action_rewrite")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, status int32, bodyPtr uint32, bodyLen uint32) {
		state := invocationFromContext(ctx)
		if state == nil || state.violation != nil {
			return
		}
		if status < 100 || status > 599 {
			state.fail("respond status %d out of range 100-599", status)
			return
		}
		if state.limits.MaxResponseBody > 0 && int(bodyLen) > state.limits.MaxResponseBody {
			state.fail("response body %d bytes exceeds max_response_body %d", bodyLen, state.limits.MaxResponseBody)
			return
		}
		var body []byte
		if bodyLen > 0 {
			data, ok := mod.Memory().Read(bodyPtr, bodyLen)
			if !ok {
				state.fail("response body out of guest memory bounds")
				return
			}
			body = append([]byte(nil), data...)
		}
		state.action.Decision = switchboard.DecisionRespond
		state.action.Response.Status = int(status)
		state.action.Response.Body = body
	}).Export("action_respond")

	exportRewriteField(builder, "action_rewrite_host", func(state *invocationState, value string) {
		if value == "" || !validTextValue(value) || strings.ContainsAny(value, " /") {
			state.fail("rewrite host %q is not a valid host", value)
			return
		}
		state.action.Patch.Host = &value
	})
	exportRewriteField(builder, "action_rewrite_path", func(state *invocationState, value string) {
		if !strings.HasPrefix(value, "/") || !validTextValue(value) {
			state.fail("rewrite path %q must start with / and contain no control characters", value)
			return
		}
		state.action.Patch.Path = &value
	})
	exportRewriteField(builder, "action_rewrite_query", func(state *invocationState, value string) {
		if !validTextValue(value) {
			state.fail("rewrite query contains control characters")
			return
		}
		state.action.Patch.Query = &value
	})

	exportHeaderOp(builder, "action_req_header_set", switchboard.HeaderOpSet, headerTargetRequest)
	exportHeaderOp(builder, "action_req_header_add", switchboard.HeaderOpAdd, headerTargetRequest)
	exportHeaderDelete(builder, "action_req_header_delete", headerTargetRequest)
	exportHeaderOp(builder, "action_resp_header_set", switchboard.HeaderOpSet, headerTargetResponse)
	exportHeaderOp(builder, "action_resp_header_add", switchboard.HeaderOpAdd, headerTargetResponse)
	exportHeaderDelete(builder, "action_resp_header_delete", headerTargetResponse)

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, keyPtr uint32, keyLen uint32, valuePtr uint32, valueLen uint32) {
		state := invocationFromContext(ctx)
		if state == nil || state.violation != nil {
			return
		}
		key, ok := readGuestString(mod, keyPtr, keyLen, 0)
		if !ok {
			state.fail("metadata key out of guest memory bounds")
			return
		}
		value, ok := readGuestString(mod, valuePtr, valueLen, 0)
		if !ok {
			state.fail("metadata value out of guest memory bounds")
			return
		}
		if key == "" || !validTextValue(key) || !validTextValue(value) {
			state.fail("metadata entry %q is empty or contains control characters", key)
			return
		}
		if state.action.Metadata == nil {
			state.action.Metadata = map[string]string{}
		}
		if _, exists := state.action.Metadata[key]; !exists && len(state.action.Metadata) >= maxMetadataEntries {
			state.fail("metadata entries exceed limit %d", maxMetadataEntries)
			return
		}
		if !state.chargeBytes(len(key) + len(value)) {
			return
		}
		state.action.Metadata[key] = value
	}).Export("action_set_metadata")

	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, ptr uint32, length uint32) {
		state := invocationFromContext(ctx)
		if state == nil || state.violation != nil {
			return
		}
		reason, ok := readGuestString(mod, ptr, length, 0)
		if !ok {
			state.fail("reason out of guest memory bounds")
			return
		}
		if !validTextValue(reason) {
			state.fail("reason contains control characters")
			return
		}
		if !state.chargeBytes(len(reason)) {
			return
		}
		state.action.Reason = reason
	}).Export("action_set_reason")
}

func exportRewriteField(builder wazeroapi.HostModuleBuilder, export string, apply func(*invocationState, string)) {
	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, ptr uint32, length uint32) {
		state := invocationFromContext(ctx)
		if state == nil || state.violation != nil {
			return
		}
		value, ok := readGuestString(mod, ptr, length, 0)
		if !ok {
			state.fail("rewrite value out of guest memory bounds")
			return
		}
		if !state.chargeBytes(len(value)) {
			return
		}
		apply(state, value)
	}).Export(export)
}

func exportHeaderOp(builder wazeroapi.HostModuleBuilder, export string, op switchboard.HeaderOpType, target headerTarget) {
	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32, valuePtr uint32, valueLen uint32) {
		state := invocationFromContext(ctx)
		if state == nil || state.violation != nil {
			return
		}
		name, ok := readGuestString(mod, namePtr, nameLen, 0)
		if !ok {
			state.fail("header name out of guest memory bounds")
			return
		}
		value, ok := readGuestString(mod, valuePtr, valueLen, 0)
		if !ok {
			state.fail("header value out of guest memory bounds")
			return
		}
		state.appendHeaderOp(target, switchboard.HeaderOp{Op: op, Name: name, Value: value})
	}).Export(export)
}

func exportHeaderDelete(builder wazeroapi.HostModuleBuilder, export string, target headerTarget) {
	builder.NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, namePtr uint32, nameLen uint32) {
		state := invocationFromContext(ctx)
		if state == nil || state.violation != nil {
			return
		}
		name, ok := readGuestString(mod, namePtr, nameLen, 0)
		if !ok {
			state.fail("header name out of guest memory bounds")
			return
		}
		state.appendHeaderOp(target, switchboard.HeaderOp{Op: switchboard.HeaderOpDelete, Name: name})
	}).Export(export)
}
