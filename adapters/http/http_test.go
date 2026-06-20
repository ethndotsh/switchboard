package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethndotsh/switchboard"
)

var benchmarkRequest switchboard.Request

func TestRequestFromHTTPUsesRequestHeaderMap(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/path", nil)
	req.Header.Set("x-test", "1")

	got := RequestFromHTTP(req)
	if got.Method != http.MethodGet || got.Path != "/path" {
		t.Fatalf("request = %#v", got)
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

func TestApplyActionNextAndHeaderOps(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("x-delete", "gone")
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{
		Type: switchboard.ActionNext,
		HeaderOps: []switchboard.HeaderOp{
			{Op: switchboard.HeaderOpSet, Name: "x-switchboard-rule", Value: "test"},
			{Op: switchboard.HeaderOpAdd, Name: "x-list", Value: "a"},
			{Op: switchboard.HeaderOpAdd, Name: "x-list", Value: "b"},
			{Op: switchboard.HeaderOpDelete, Name: "x-delete"},
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

func TestApplyActionDeny(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{Type: switchboard.ActionDeny, StatusCode: http.StatusTeapot})
	if err != nil {
		t.Fatal(err)
	}
	if next {
		t.Fatal("did not expect next")
	}
	if res.Code != http.StatusTeapot {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestApplyActionRedirect(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{
		Type:     switchboard.ActionRedirect,
		Location: "/new",
		HeaderOps: []switchboard.HeaderOp{
			{Op: switchboard.HeaderOpSet, Name: "x-powered-by", Value: "switchboard"},
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

func TestApplyActionRewrite(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/old?x=1", nil)
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{Type: switchboard.ActionRewrite, RewritePath: "/new"})
	if err != nil {
		t.Fatal(err)
	}
	if !next {
		t.Fatal("expected next")
	}
	if req.URL.Path != "/new" || req.RequestURI != "/new?x=1" {
		t.Fatalf("url = %s request_uri = %s", req.URL.String(), req.RequestURI)
	}
}
