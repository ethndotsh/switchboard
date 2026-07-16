package sdk

import (
	"net/url"
	"testing"

	"github.com/ethndotsh/switchboard"
)

func TestChainStopsOnTerminalAction(t *testing.T) {
	req := NewRequest(RequestData{Method: "GET", Path: "/blocked"})
	action := Chain(req,
		func(Request) Action { return Deny(403) },
		func(Request) Action { return Redirect(302, "/nope") },
	)
	if action.Decision != DecisionDeny || action.Response.Status != 403 {
		t.Fatalf("action = %#v", action)
	}
}

func TestChainCarriesHeaderPatches(t *testing.T) {
	req := NewRequest(RequestData{Method: "GET", Path: "/"})
	action := Chain(req,
		func(req Request) Action {
			return Next().SetRequestHeader("x-a", "1")
		},
		func(req Request) Action {
			return Next().SetRequestHeader("x-b", req.Header("x-a")+"2")
		},
	)
	if action.Decision != DecisionNext {
		t.Fatalf("decision = %q", action.Decision)
	}
	if len(action.Patch.Headers) != 2 {
		t.Fatalf("header ops = %#v", action.Patch.Headers)
	}
	if action.Patch.Headers[0] != (switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x-a", Value: "1"}) {
		t.Fatalf("first header op = %#v", action.Patch.Headers[0])
	}
	if action.Patch.Headers[1] != (switchboard.HeaderOp{Op: switchboard.HeaderOpSet, Name: "x-b", Value: "12"}) {
		t.Fatalf("second header op = %#v", action.Patch.Headers[1])
	}
}

func TestChainCarriesStateIntoTerminalAction(t *testing.T) {
	req := NewRequest(RequestData{Method: "GET", Path: "/"})
	action := Chain(req,
		func(req Request) Action {
			return Next().
				SetRequestHeader("x-a", "1").
				SetResponseHeader("x-frame-options", "DENY").
				SetMetadata("tier", "free").
				WithReason("early")
		},
		func(req Request) Action {
			return Redirect(302, "/new").
				SetResponseHeader("x-b", req.Header("x-a")).
				SetMetadata("tier", "paid")
		},
	)
	if action.Decision != DecisionRedirect {
		t.Fatalf("decision = %q", action.Decision)
	}
	if len(action.Patch.Headers) != 1 || action.Patch.Headers[0].Name != "x-a" {
		t.Fatalf("patch headers = %#v", action.Patch.Headers)
	}
	if len(action.Response.Headers) != 2 || action.Response.Headers[0].Name != "x-frame-options" || action.Response.Headers[1].Value != "1" {
		t.Fatalf("response headers = %#v", action.Response.Headers)
	}
	if action.Metadata["tier"] != "paid" {
		t.Fatalf("metadata = %#v (terminal rule should win)", action.Metadata)
	}
	if action.Reason != "early" {
		t.Fatalf("reason = %q (terminal without reason keeps accumulated)", action.Reason)
	}
}

func TestChainLaterRulesSeeRewrites(t *testing.T) {
	req := NewRequest(RequestData{Method: "GET", Path: "/old", Host: "a.example"})
	var observedPath, observedHost string
	action := Chain(req,
		func(req Request) Action {
			return Rewrite("/new").RewriteHost("b.example")
		},
		func(req Request) Action {
			observedPath = req.Path()
			observedHost = req.Host()
			return Next()
		},
	)
	if observedPath != "/new" || observedHost != "b.example" {
		t.Fatalf("later rule saw path=%q host=%q", observedPath, observedHost)
	}
	if action.Decision != DecisionRewrite {
		t.Fatalf("decision = %q (accumulated rewrite should surface)", action.Decision)
	}
	if action.Patch.Path == nil || *action.Patch.Path != "/new" {
		t.Fatalf("patch = %#v", action.Patch)
	}
}

func TestChainReasonLastNonEmptyWins(t *testing.T) {
	req := NewRequest(RequestData{Method: "GET", Path: "/"})
	action := Chain(req,
		func(Request) Action { return Next().WithReason("first") },
		func(Request) Action { return Next() },
		func(Request) Action { return Next().WithReason("second") },
	)
	if action.Reason != "second" {
		t.Fatalf("reason = %q", action.Reason)
	}
}

func TestRequestWithPatchHeaders(t *testing.T) {
	req := NewRequest(RequestData{Method: "GET", Path: "/", Headers: map[string][]string{"x-a": {"1"}}})
	req = req.WithPatch(switchboard.RequestPatch{Headers: []switchboard.HeaderOp{
		{Op: switchboard.HeaderOpAdd, Name: "x-a", Value: "2"},
		{Op: switchboard.HeaderOpSet, Name: "x-b", Value: "3"},
		{Op: switchboard.HeaderOpDelete, Name: "x-c"},
	}})
	if got := req.HeaderValues("x-a"); len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("x-a = %#v", got)
	}
	if got := req.Header("x-b"); got != "3" {
		t.Fatalf("x-b = %q", got)
	}
	if req.Method() != "GET" || req.Path() != "/" {
		t.Fatalf("patched request lost base fields: method=%q path=%q", req.Method(), req.Path())
	}
}

func TestRequestAccessors(t *testing.T) {
	req := NewRequest(RequestData{
		Method:     "POST",
		Scheme:     "https",
		Host:       "example.com",
		Path:       "/admin",
		RawQuery:   "a=1&b=two+words&c=%2Fslash",
		Protocol:   "HTTP/2.0",
		RemoteAddr: "203.0.113.9:1234",
		ClientIP:   "203.0.113.9",
		TLS:        true,
		Headers: map[string][]string{
			"Cookie":       {"session=abc123; user_id=42", `quoted="hello world"`},
			"X-Multi":      {"a", "b"},
			"Content-Type": {"application/json"},
		},
	})
	if req.Method() != "POST" || req.Scheme() != "https" || req.Host() != "example.com" {
		t.Fatalf("basic accessors failed")
	}
	if !req.TLS() || req.Protocol() != "HTTP/2.0" || req.RemoteAddr() != "203.0.113.9:1234" || req.ClientIP() != "203.0.113.9" {
		t.Fatalf("connection accessors failed")
	}
	if req.Query("a") != "1" || req.Query("b") != "two words" || req.Query("c") != "/slash" || req.Query("missing") != "" {
		t.Fatalf("query accessors failed: a=%q b=%q c=%q", req.Query("a"), req.Query("b"), req.Query("c"))
	}
	if req.Cookie("session") != "abc123" || req.Cookie("user_id") != "42" || req.Cookie("quoted") != "hello world" || req.Cookie("nope") != "" {
		t.Fatalf("cookie accessors failed: session=%q quoted=%q", req.Cookie("session"), req.Cookie("quoted"))
	}
	if req.Header("content-type") != "application/json" {
		t.Fatalf("case-insensitive header lookup failed")
	}
	if got := req.HeaderValues("X-Multi"); len(got) != 2 {
		t.Fatalf("multi-value header = %#v", got)
	}
}

// TestQueryParserMatchesNetURL locks the hand-rolled TinyGo-friendly parser
// to url.ParseQuery semantics for the shapes rules actually see.
func TestQueryParserMatchesNetURL(t *testing.T) {
	cases := []string{
		"a=1&b=2",
		"a=one+two",
		"a=%2Fpath%2Fhere",
		"empty=&x=1",
		"flag",
		"dup=1&dup=2",
		"pct%20key=value",
		"a=b=c",
		"",
		"trailing=&",
	}
	names := []string{"a", "b", "empty", "flag", "dup", "pct key", "x", "trailing", "missing"}
	for _, rawQuery := range cases {
		expected, err := url.ParseQuery(rawQuery)
		if err != nil {
			continue
		}
		for _, name := range names {
			if got, want := queryValue(rawQuery, name), expected.Get(name); got != want {
				t.Errorf("queryValue(%q, %q) = %q, url.ParseQuery says %q", rawQuery, name, got, want)
			}
		}
	}
}
