package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethndotsh/switchboard"
)

func TestApplyActionNextAndHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	next, err := ApplyAction(res, req, switchboard.Action{
		Type:    switchboard.ActionNext,
		Headers: map[string]string{"x-switchboard-rule": "test"},
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

	next, err := ApplyAction(res, req, switchboard.Action{Type: switchboard.ActionRedirect, Location: "/new"})
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
