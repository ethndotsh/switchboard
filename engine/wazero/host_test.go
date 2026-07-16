package wazero

import (
	"strings"
	"testing"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/engine/wasmapi"
)

func TestValidHeaderName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"x-a", true},
		{"X_b1!#$%&'*+-.^_`|~", true},
		{"Content-Type", true},
		{"x", true},
		{"1", true},
		{"", false},
		{"x a", false},
		{"x:y", false},
		{"héader", false},
		{"x\ny", false},
		{"x\ry", false},
		{"x\x00y", false},
		{"x/y", false},
		{"x(y)", false},
		{"x\ty", false},
	}
	for _, tt := range tests {
		if got := validHeaderName(tt.name); got != tt.valid {
			t.Errorf("validHeaderName(%q) = %v, want %v", tt.name, got, tt.valid)
		}
	}
}

func TestValidHeaderValue(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"", true},
		{"normal text", true},
		{"with\ttab", true},
		{"quoted \"value\"; charset=utf-8", true},
		{"non-ascii héader ok", true},
		{"high\x7fbyte allowed here", true},
		{"crlf\r\ninjection", false},
		{"bare\rcr", false},
		{"bare\nlf", false},
		{"nul\x00byte", false},
	}
	for _, tt := range tests {
		if got := validHeaderValue(tt.value); got != tt.valid {
			t.Errorf("validHeaderValue(%q) = %v, want %v", tt.value, got, tt.valid)
		}
	}
}

func TestValidTextValue(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"", true},
		{"plain text", true},
		{"tab\tis fine", true},
		{"unicode héader", true},
		{"\x01", false},
		{"\x08", false},
		{"\x1f", false},
		{"\r", false},
		{"\n", false},
		{"\x00", false},
		{"del\x7fbyte", false},
		{"trailing control\x1f", false},
	}
	for _, tt := range tests {
		if got := validTextValue(tt.value); got != tt.valid {
			t.Errorf("validTextValue(%q) = %v, want %v", tt.value, got, tt.valid)
		}
	}
}

func TestChargeBytesQuota(t *testing.T) {
	state := &invocationState{limits: wasmapi.InvokeLimits{MaxActionBytes: 10}}

	if !state.chargeBytes(6) {
		t.Fatal("first charge within quota failed")
	}
	if state.actionBytes != 6 {
		t.Fatalf("actionBytes = %d", state.actionBytes)
	}
	if !state.chargeBytes(4) {
		t.Fatal("charge exactly at quota failed")
	}
	if state.violation != nil {
		t.Fatalf("violation set at exact quota: %v", state.violation)
	}
	if state.chargeBytes(1) {
		t.Fatal("charge past quota succeeded")
	}
	if state.violation == nil || !strings.Contains(state.violation.Error(), "max_action_bytes 10") {
		t.Fatalf("violation = %v", state.violation)
	}

	// The first violation wins; later failures must not overwrite it.
	first := state.violation
	if state.chargeBytes(100) {
		t.Fatal("charge after violation succeeded")
	}
	state.fail("later unrelated failure %d", 42)
	if state.violation != first {
		t.Fatalf("violation overwritten: %v", state.violation)
	}
}

func TestChargeBytesZeroLimitIsUnlimited(t *testing.T) {
	state := &invocationState{limits: wasmapi.InvokeLimits{}}
	if !state.chargeBytes(1 << 20) {
		t.Fatal("unlimited quota rejected a charge")
	}
	if state.violation != nil {
		t.Fatalf("violation = %v", state.violation)
	}
}

func TestAppendHeaderOpTargetsAndSharedLimit(t *testing.T) {
	state := &invocationState{limits: wasmapi.InvokeLimits{MaxHeaderOps: 2, MaxActionBytes: 1024}}

	state.appendHeaderOp(headerTargetRequest, switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x-req", Value: "1"})
	state.appendHeaderOp(headerTargetResponse, switchboard.HeaderOp{Op: switchboard.HeaderOpAdd, Name: "x-resp", Value: "2"})
	if state.violation != nil {
		t.Fatalf("violation = %v", state.violation)
	}
	if len(state.action.Patch.Headers) != 1 || state.action.Patch.Headers[0].Name != "x-req" {
		t.Fatalf("request ops = %#v", state.action.Patch.Headers)
	}
	if len(state.action.Response.Headers) != 1 || state.action.Response.Headers[0].Name != "x-resp" {
		t.Fatalf("response ops = %#v", state.action.Response.Headers)
	}
	if state.headerOps != 2 {
		t.Fatalf("headerOps = %d", state.headerOps)
	}

	// Request and response ops share one budget: the third op violates.
	state.appendHeaderOp(headerTargetRequest, switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x-third", Value: "3"})
	if state.violation == nil || !strings.Contains(state.violation.Error(), "max_header_ops 2") {
		t.Fatalf("violation = %v", state.violation)
	}
	if len(state.action.Patch.Headers) != 1 || len(state.action.Response.Headers) != 1 {
		t.Fatal("op appended past the limit")
	}
}

func TestAppendHeaderOpRejectsInvalidName(t *testing.T) {
	state := &invocationState{limits: wasmapi.InvokeLimits{MaxHeaderOps: 8}}
	state.appendHeaderOp(headerTargetRequest, switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x header", Value: "v"})
	if state.violation == nil || !strings.Contains(state.violation.Error(), "not a valid token") {
		t.Fatalf("violation = %v", state.violation)
	}
	if len(state.action.Patch.Headers) != 0 || state.headerOps != 0 {
		t.Fatalf("invalid op recorded: %#v", state.action.Patch.Headers)
	}
}

func TestAppendHeaderOpRejectsInvalidValue(t *testing.T) {
	state := &invocationState{limits: wasmapi.InvokeLimits{MaxHeaderOps: 8}}
	state.appendHeaderOp(headerTargetResponse, switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x-ok", Value: "evil\r\nInjected: yes"})
	if state.violation == nil || !strings.Contains(state.violation.Error(), "control characters") {
		t.Fatalf("violation = %v", state.violation)
	}
	if len(state.action.Response.Headers) != 0 || state.headerOps != 0 {
		t.Fatalf("invalid op recorded: %#v", state.action.Response.Headers)
	}
}

func TestAppendHeaderOpAllowsDeleteWithoutValue(t *testing.T) {
	state := &invocationState{limits: wasmapi.InvokeLimits{MaxHeaderOps: 8}}
	state.appendHeaderOp(headerTargetRequest, switchboard.HeaderOp{Op: switchboard.HeaderOpDelete, Name: "x-gone"})
	if state.violation != nil {
		t.Fatalf("violation = %v", state.violation)
	}
	if len(state.action.Patch.Headers) != 1 || state.action.Patch.Headers[0].Op != switchboard.HeaderOpDelete {
		t.Fatalf("ops = %#v", state.action.Patch.Headers)
	}
}

func TestAppendHeaderOpChargesActionBytes(t *testing.T) {
	// name (4) + value (5) exceed the 8-byte quota; the op must not land.
	state := &invocationState{limits: wasmapi.InvokeLimits{MaxHeaderOps: 8, MaxActionBytes: 8}}
	state.appendHeaderOp(headerTargetRequest, switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x-ab", Value: "12345"})
	if state.violation == nil || !strings.Contains(state.violation.Error(), "max_action_bytes 8") {
		t.Fatalf("violation = %v", state.violation)
	}
	if len(state.action.Patch.Headers) != 0 {
		t.Fatalf("over-quota op recorded: %#v", state.action.Patch.Headers)
	}
}

func TestAppendHeaderOpNoOpAfterQuotaViolation(t *testing.T) {
	state := &invocationState{limits: wasmapi.InvokeLimits{MaxHeaderOps: 8, MaxActionBytes: 4}}
	if state.chargeBytes(5) {
		t.Fatal("expected quota violation")
	}
	first := state.violation

	state.appendHeaderOp(headerTargetRequest, switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x-a", Value: "1"})
	state.appendHeaderOp(headerTargetResponse, switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x-b", Value: "2"})
	if len(state.action.Patch.Headers) != 0 || len(state.action.Response.Headers) != 0 {
		t.Fatalf("ops appended after violation: %#v %#v", state.action.Patch.Headers, state.action.Response.Headers)
	}
	if state.headerOps != 0 {
		t.Fatalf("headerOps = %d", state.headerOps)
	}
	if state.violation != first {
		t.Fatalf("violation overwritten: %v", state.violation)
	}
}
