package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethndotsh/switchboard"
)

var benchmarkRequest switchboard.Request

func stringPointer(s string) *string { return &s }

func TestRequestFromHTTPUsesRequestHeaderMap(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/path?q=1", nil)
	req.Header.Set("x-test", "1")

	got := RequestFromHTTP(req)
	if got.Method != http.MethodGet || got.Path != "/path" {
		t.Fatalf("request = %#v", got)
	}
	if got.Host != "example.com" || got.RawQuery != "q=1" || got.Scheme != "http" || got.Protocol != "HTTP/1.1" {
		t.Fatalf("request = %#v", got)
	}
	if got.ClientIP != "192.0.2.1" {
		t.Fatalf("client ip = %q", got.ClientIP)
	}
	got.Headers["X-Test"] = []string{"2"}
	if req.Header.Get("x-test") != "2" {
		t.Fatalf("expected request headers to be reused")
	}
}

func BenchmarkRequestFromHTTP(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/path", nil)
	req.Header = http.Header{
		"Accept":            {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		"Accept-Encoding":   {"gzip, deflate, br"},
		"Accept-Language":   {"en-US,en;q=0.9"},
		"Cache-Control":     {"no-cache"},
		"User-Agent":        {"switchboard-benchmark/1.0"},
		"X-Forwarded-For":   {"203.0.113.10"},
		"X-Forwarded-Host":  {"example.com"},
		"X-Forwarded-Proto": {"https"},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkRequest = RequestFromHTTP(req)
	}
}

func TestApplyActionNextAndRequestHeaderOps(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-delete", "gone")
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{
		Decision: switchboard.DecisionNext,
		Patch: switchboard.RequestPatch{
			Headers: []switchboard.HeaderOp{
				{Op: switchboard.HeaderOpSet, Name: "x-switchboard-rule", Value: "test"},
				{Op: switchboard.HeaderOpAdd, Name: "x-list", Value: "a"},
				{Op: switchboard.HeaderOpAdd, Name: "x-list", Value: "b"},
				{Op: switchboard.HeaderOpDelete, Name: "x-delete"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !next {
		t.Fatal("expected next")
	}
	if req.Header.Get("x-switchboard-rule") != "test" {
		t.Fatalf("header = %q", req.Header.Get("x-switchboard-rule"))
	}
	if got := req.Header.Values("x-list"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("x-list = %#v", got)
	}
	if req.Header.Get("x-delete") != "" {
		t.Fatalf("x-delete = %q", req.Header.Get("x-delete"))
	}
}

func TestApplyActionDenyWithBodyAndResponseHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{
		Decision: switchboard.DecisionDeny,
		Response: switchboard.Response{
			Status: http.StatusTeapot,
			Body:   []byte("nope"),
			Headers: []switchboard.HeaderOp{
				{Op: switchboard.HeaderOpSet, Name: "x-denied-by", Value: "switchboard"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if next {
		t.Fatal("did not expect next")
	}
	if res.Code != http.StatusTeapot {
		t.Fatalf("status = %d", res.Code)
	}
	if res.Body.String() != "nope" {
		t.Fatalf("body = %q", res.Body.String())
	}
	if res.Header().Get("x-denied-by") != "switchboard" {
		t.Fatalf("x-denied-by = %q", res.Header().Get("x-denied-by"))
	}
	if res.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("content-type = %q", res.Header().Get("Content-Type"))
	}
}

func TestApplyActionDenyDefaultsTo403(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{Decision: switchboard.DecisionDeny})
	if err != nil || next {
		t.Fatalf("next = %v err = %v", next, err)
	}
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestApplyActionRespond(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{
		Decision: switchboard.DecisionRespond,
		Response: switchboard.Response{
			Body: []byte(`{"ok":true}`),
			Headers: []switchboard.HeaderOp{
				{Op: switchboard.HeaderOpSet, Name: "Content-Type", Value: "application/json"},
			},
		},
	})
	if err != nil || next {
		t.Fatalf("next = %v err = %v", next, err)
	}
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d (respond defaults to 200)", res.Code)
	}
	if res.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("explicit content-type was overridden: %q", res.Header().Get("Content-Type"))
	}
	if res.Body.String() != `{"ok":true}` {
		t.Fatalf("body = %q", res.Body.String())
	}
}

func TestApplyActionRedirect(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{
		Decision: switchboard.DecisionRedirect,
		Response: switchboard.Response{
			Location: "/new",
			Headers: []switchboard.HeaderOp{
				{Op: switchboard.HeaderOpSet, Name: "x-powered-by", Value: "switchboard"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if next {
		t.Fatal("did not expect next")
	}
	if res.Code != http.StatusFound {
		t.Fatalf("status = %d", res.Code)
	}
	if res.Header().Get("Location") != "/new" {
		t.Fatalf("location = %q", res.Header().Get("Location"))
	}
	if res.Header().Get("x-powered-by") != "switchboard" {
		t.Fatalf("x-powered-by = %q", res.Header().Get("x-powered-by"))
	}
}

func TestApplyActionRewritePatch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/old?x=1", nil)
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{
		Decision: switchboard.DecisionRewrite,
		Patch: switchboard.RequestPatch{
			Path:  stringPointer("/new"),
			Query: stringPointer("y=2"),
			Host:  stringPointer("backend.internal"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !next {
		t.Fatal("expected next")
	}
	if req.URL.Path != "/new" || req.RequestURI != "/new?y=2" {
		t.Fatalf("url = %s request_uri = %s", req.URL.String(), req.RequestURI)
	}
	if req.Host != "backend.internal" {
		t.Fatalf("host = %q", req.Host)
	}
}

func TestApplyActionUnknownDecision(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	if _, err := ApplyAction(res, req, switchboard.Action{Decision: "bogus"}); err == nil {
		t.Fatal("expected error for unknown decision")
	}
}
