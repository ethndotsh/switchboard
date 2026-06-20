package sdk

import (
	"testing"

	"github.com/ethndotsh/switchboard"
)

func TestChainStopsOnTerminalAction(t *testing.T) {
	req := NewRequest("GET", "/blocked", map[string][]string{})
	action := Chain(req,
		func(Request) Action { return Deny(403) },
		func(Request) Action { return Redirect(302, "/nope") },
	)
	if action.Type != ActionDeny || action.StatusCode != 403 {
		t.Fatalf("action = %#v", action)
	}
}

func TestChainCarriesHeaderPatches(t *testing.T) {
	req := NewRequest("GET", "/", map[string][]string{})
	action := Chain(req,
		func(req Request) Action {
			return Next().SetHeader("x-a", "1")
		},
		func(req Request) Action {
			return Next().SetHeader("x-b", req.Header("x-a")+"2")
		},
	)
	if action.Type != ActionNext {
		t.Fatalf("action type = %q", action.Type)
	}
	if len(action.HeaderOps) != 2 {
		t.Fatalf("header ops = %#v", action.HeaderOps)
	}
	if action.HeaderOps[0] != (switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x-a", Value: "1"}) {
		t.Fatalf("first header op = %#v", action.HeaderOps[0])
	}
	if action.HeaderOps[1] != (switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x-b", Value: "12"}) {
		t.Fatalf("second header op = %#v", action.HeaderOps[1])
	}
}

func TestChainCarriesHeaderPatchesIntoTerminalAction(t *testing.T) {
	req := NewRequest("GET", "/", map[string][]string{})
	action := Chain(req,
		func(req Request) Action {
			return Next().SetHeader("x-a", "1")
		},
		func(req Request) Action {
			return Redirect(302, "/new").SetHeader("x-b", req.Header("x-a"))
		},
	)
	if action.Type != ActionRedirect {
		t.Fatalf("action type = %q", action.Type)
	}
	if len(action.HeaderOps) != 2 {
		t.Fatalf("header ops = %#v", action.HeaderOps)
	}
	if action.HeaderOps[0].Name != "x-a" || action.HeaderOps[1].Name != "x-b" || action.HeaderOps[1].Value != "1" {
		t.Fatalf("header ops = %#v", action.HeaderOps)
	}
}

func TestRequestWithHeaderOps(t *testing.T) {
	req := NewRequest("GET", "/", map[string][]string{"x-a": {"1"}})
	req = req.WithHeaderOps([]switchboard.HeaderOp{
		{Op: switchboard.HeaderOpAdd, Name: "x-a", Value: "2"},
		{Op: switchboard.HeaderOpSet, Name: "x-b", Value: "3"},
		{Op: switchboard.HeaderOpDelete, Name: "x-c"},
	})
	if got := req.HeaderValues("x-a"); len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("x-a = %#v", got)
	}
	if got := req.Header("x-b"); got != "3" {
		t.Fatalf("x-b = %q", got)
	}
}
