package sdk

import "testing"

func TestChainStopsOnTerminalAction(t *testing.T) {
	req := Request{Path: "/blocked", Headers: map[string][]string{}}
	action := Chain(req,
		func(Request) Action { return Deny(403) },
		func(Request) Action { return Redirect(302, "/nope") },
	)
	if action.Type != ActionDeny || action.StatusCode != 403 {
		t.Fatalf("action = %#v", action)
	}
}

func TestChainCarriesHeaderMutations(t *testing.T) {
	req := Request{Path: "/", Headers: map[string][]string{}}
	action := Chain(req,
		func(req Request) Action {
			req.Headers["x-a"] = []string{"1"}
			return Next(req)
		},
		func(req Request) Action {
			req.Headers["x-b"] = []string{req.Headers["x-a"][0] + "2"}
			return Next(req)
		},
	)
	if action.Type != ActionNext {
		t.Fatalf("action type = %q", action.Type)
	}
	if action.Headers["x-a"] != "1" || action.Headers["x-b"] != "12" {
		t.Fatalf("headers = %#v", action.Headers)
	}
}
