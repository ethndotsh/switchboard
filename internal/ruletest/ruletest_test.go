package ruletest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ethndotsh/switchboard"
)

func TestParseSuiteValid(t *testing.T) {
	suite, err := ParseSuite([]byte(`
schema: switchboard.tests/v1
cases:
  - name: denies blocked path
    request:
      method: POST
      path: /blocked
      host: example.com
      tls: true
    expect:
      action: deny
      status: 403
  - name: passes root
    request:
      path: /
    expect:
      action: next
`))
	if err != nil {
		t.Fatal(err)
	}
	if suite.Schema != SuiteSchema {
		t.Fatalf("schema = %q", suite.Schema)
	}
	if len(suite.Cases) != 2 {
		t.Fatalf("cases = %d", len(suite.Cases))
	}
	first := suite.Cases[0]
	if first.Name != "denies blocked path" || first.Request.Method != "POST" || !first.Request.TLS {
		t.Fatalf("first case = %#v", first)
	}
	if first.Expect.Action == nil || *first.Expect.Action != "deny" {
		t.Fatalf("first expect action = %v", first.Expect.Action)
	}
	if first.Expect.Status == nil || *first.Expect.Status != 403 {
		t.Fatalf("first expect status = %v", first.Expect.Status)
	}
	if first.Expect.Reason != nil {
		t.Fatalf("absent expect field decoded as %v", first.Expect.Reason)
	}
}

func TestParseSuiteOmittedSchemaAccepted(t *testing.T) {
	if _, err := ParseSuite([]byte("cases:\n  - name: a\n    expect:\n      action: next\n")); err != nil {
		t.Fatalf("suite without schema rejected: %v", err)
	}
}

func TestParseSuiteErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{
			name: "unknown expect key",
			yaml: `
cases:
  - name: typo case
    expect:
      statuss: 403
`,
			wantSub: `"statuss"`,
		},
		{
			name: "missing case name",
			yaml: `
cases:
  - expect:
      action: next
`,
			wantSub: "case 1 is missing a name",
		},
		{
			name:    "empty cases",
			yaml:    "schema: switchboard.tests/v1\ncases: []\n",
			wantSub: "declares no cases",
		},
		{
			name:    "no cases key",
			yaml:    "schema: switchboard.tests/v1\n",
			wantSub: "declares no cases",
		},
		{
			name: "wrong schema",
			yaml: `
schema: switchboard.tests/v2
cases:
  - name: a
    expect:
      action: next
`,
			wantSub: "unsupported tests schema",
		},
		{
			name: "case without expectations",
			yaml: `
cases:
  - name: empty expectations
    expect: {}
`,
			wantSub: "has no expectations",
		},
		{
			name:    "not yaml",
			yaml:    "cases: [}{",
			wantSub: "parse tests",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSuite([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("error %q does not mention %q", err, tt.wantSub)
			}
		})
	}
}

func TestParseSuiteHeaderForms(t *testing.T) {
	suite, err := ParseSuite([]byte(`
cases:
  - name: header forms
    request:
      path: /
      headers:
        X-Scalar: one
        X-List:
          - first
          - second
    expect:
      action: next
`))
	if err != nil {
		t.Fatal(err)
	}
	req, err := suite.Cases[0].Request.BuildRequest()
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Headers["X-Scalar"]; len(got) != 1 || got[0] != "one" {
		t.Fatalf("scalar header = %v", got)
	}
	if got := req.Headers["X-List"]; len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("list header = %v", got)
	}
}

func TestBuildRequestCookies(t *testing.T) {
	req, err := CaseRequest{
		Path:    "/login",
		Cookies: map[string]string{"session": "abc123"},
	}.BuildRequest()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, value := range req.Headers["Cookie"] {
		if value == "session=abc123" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Cookie header = %v", req.Headers["Cookie"])
	}
}

func TestBuildRequestDefaults(t *testing.T) {
	req, err := CaseRequest{}.BuildRequest()
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "GET" || req.Path != "/" || req.Scheme != "http" {
		t.Fatalf("defaults = %#v", req)
	}
	tlsReq, err := CaseRequest{TLS: true}.BuildRequest()
	if err != nil {
		t.Fatal(err)
	}
	if tlsReq.Scheme != "https" {
		t.Fatalf("tls scheme = %q", tlsReq.Scheme)
	}
	explicit, err := CaseRequest{Scheme: "ws"}.BuildRequest()
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Scheme != "ws" {
		t.Fatalf("explicit scheme overridden to %q", explicit.Scheme)
	}
}

func TestBuildRequestRejectsBadHeaderValues(t *testing.T) {
	if _, err := (CaseRequest{Headers: map[string]any{"X-Num": 5}}).BuildRequest(); err == nil {
		t.Fatal("non-string header accepted")
	}
	if _, err := (CaseRequest{Headers: map[string]any{"X-List": []any{1}}}).BuildRequest(); err == nil {
		t.Fatal("non-string header list item accepted")
	}
}

// scriptedInvoker returns canned actions keyed by request path.
type scriptedInvoker struct {
	actions map[string]switchboard.Action
	err     error
	calls   []switchboard.Request
}

func (s *scriptedInvoker) Invoke(_ context.Context, req switchboard.Request) (switchboard.Action, error) {
	s.calls = append(s.calls, req)
	if s.err != nil {
		return switchboard.Action{}, s.err
	}
	return s.actions[req.Path], nil
}

func strPtr(s string) *string { return &s }

func passingSuiteActions() map[string]switchboard.Action {
	return map[string]switchboard.Action{
		"/blocked": {
			Decision: switchboard.DecisionDeny,
			Response: switchboard.Response{Status: 403, Body: []byte("request denied by policy")},
			Metadata: map[string]string{"rule": "security", "unchecked": "extra"},
			Reason:   "blocked by policy",
		},
		"/old": {
			Decision: switchboard.DecisionRedirect,
			Response: switchboard.Response{Status: 301, Location: "https://example.com/new"},
		},
		"/api": {
			Decision: switchboard.DecisionRewrite,
			Patch: switchboard.RequestPatch{
				Path:  strPtr("/internal/api"),
				Host:  strPtr("backend.internal"),
				Query: strPtr("v=2"),
				Headers: []switchboard.HeaderOp{
					{Op: switchboard.HeaderOpSet, Name: "X-Forwarded-Rule", Value: "api"},
					{Op: switchboard.HeaderOpSet, Name: "X-Multi", Value: "first"},
					{Op: switchboard.HeaderOpSet, Name: "X-Multi", Value: "second"},
					{Op: switchboard.HeaderOpAdd, Name: "X-Extra", Value: "a"},
					{Op: switchboard.HeaderOpAdd, Name: "X-Extra", Value: "b"},
					{Op: switchboard.HeaderOpSet, Name: "X-Temp", Value: "gone"},
					{Op: switchboard.HeaderOpDelete, Name: "x-temp"},
				},
			},
		},
		"/": {
			Decision: switchboard.DecisionNext,
			Response: switchboard.Response{Headers: []switchboard.HeaderOp{
				{Op: switchboard.HeaderOpSet, Name: "X-Frame-Options", Value: "DENY"},
			}},
		},
		"/partial": {
			Decision: switchboard.DecisionDeny,
			Response: switchboard.Response{Status: 500, Location: "https://not-asserted"},
			Reason:   "unchecked reason",
		},
	}
}

const passingSuiteYAML = `
schema: switchboard.tests/v1
cases:
  - name: denies blocked path
    request:
      path: /blocked
    expect:
      action: deny
      status: 403
      reason: blocked by policy
      body_contains: denied
      metadata:
        rule: security
  - name: redirects old path
    request:
      path: /old
    expect:
      action: redirect
      status: 301
      location: https://example.com/new
  - name: rewrites api path
    request:
      path: /api
    expect:
      action: rewrite
      rewrite_path: /internal/api
      rewrite_host: backend.internal
      rewrite_query: v=2
      request_headers:
        X-Forwarded-Rule: api
        x-multi: second
        X-Extra: b
  - name: sets response headers
    request:
      path: /
    expect:
      action: next
      response_headers:
        X-Frame-Options: DENY
  - name: partial matching only asserts present fields
    request:
      path: /partial
    expect:
      action: deny
`

func TestSuiteRunPassing(t *testing.T) {
	suite, err := ParseSuite([]byte(passingSuiteYAML))
	if err != nil {
		t.Fatal(err)
	}
	invoker := &scriptedInvoker{actions: passingSuiteActions()}
	report := suite.Run(context.Background(), invoker)

	if !report.OK() {
		t.Fatalf("report not OK: %#v", report)
	}
	if report.Total != 5 || report.Passed != 5 || report.Failed != 0 || report.Errored != 0 {
		t.Fatalf("report counts = %#v", report)
	}
	if len(invoker.calls) != 5 {
		t.Fatalf("invoker called %d times", len(invoker.calls))
	}
	for _, result := range report.Results {
		if !result.Passed || len(result.Failures) != 0 {
			t.Fatalf("result %q = %#v", result.Name, result)
		}
	}
	if got := report.Summary(); got != "5/5 cases passed" {
		t.Fatalf("summary = %q", got)
	}
}

const failingSuiteYAML = `
schema: switchboard.tests/v1
cases:
  - name: wrong action
    request:
      path: /blocked
    expect:
      action: next
  - name: wrong status
    request:
      path: /blocked
    expect:
      status: 403
  - name: wrong reason
    request:
      path: /blocked
    expect:
      reason: expected reason
  - name: missing location
    request:
      path: /blocked
    expect:
      location: https://example.com/new
  - name: missing rewrites
    request:
      path: /blocked
    expect:
      rewrite_path: /elsewhere
      rewrite_host: elsewhere.internal
      rewrite_query: v=3
  - name: missing body substring
    request:
      path: /blocked
    expect:
      body_contains: teapot
  - name: metadata mismatch
    request:
      path: /blocked
    expect:
      metadata:
        rule: routing
        absent: value
  - name: deleted request header
    request:
      path: /api
    expect:
      request_headers:
        X-Temp: gone
  - name: wrong response header value
    request:
      path: /
    expect:
      response_headers:
        X-Frame-Options: SAMEORIGIN
`

func TestSuiteRunFailureDiffs(t *testing.T) {
	suite, err := ParseSuite([]byte(failingSuiteYAML))
	if err != nil {
		t.Fatal(err)
	}
	actions := passingSuiteActions()
	// Give /blocked an unexpected status for the diff message.
	blocked := actions["/blocked"]
	blocked.Response.Status = 200
	actions["/blocked"] = blocked

	report := suite.Run(context.Background(), &scriptedInvoker{actions: actions})
	if report.OK() {
		t.Fatal("report unexpectedly OK")
	}
	if report.Failed != report.Total || report.Passed != 0 || report.Errored != 0 {
		t.Fatalf("report counts = %#v", report)
	}

	wantFailures := map[string][]string{
		"wrong action":                {`expect.action: want "next", got "deny"`},
		"wrong status":                {"expect.status: want 403, got 200"},
		"wrong reason":                {`expect.reason: want "expected reason", got "blocked by policy"`},
		"missing location":            {`expect.location: want "https://example.com/new", got ""`},
		"missing rewrites":            {`expect.rewrite_path: want "/elsewhere", got no path rewrite`, `expect.rewrite_host: want "elsewhere.internal", got no host rewrite`, `expect.rewrite_query: want "v=3", got no query rewrite`},
		"missing body substring":      {`expect.body_contains: "teapot" not found in response body`},
		"metadata mismatch":           {`expect.metadata[rule]: want "routing", got "security"`, `expect.metadata[absent]: want "value", key not set`},
		"deleted request header":      {`expect.request_headers[X-Temp]: want "gone", header not set`},
		"wrong response header value": {`expect.response_headers[X-Frame-Options]: want "SAMEORIGIN", got "DENY"`},
	}
	for _, result := range report.Results {
		want, ok := wantFailures[result.Name]
		if !ok {
			t.Fatalf("unexpected result %q", result.Name)
		}
		for _, wantFailure := range want {
			found := false
			for _, failure := range result.Failures {
				if failure == wantFailure {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("case %q: failure %q not in %v", result.Name, wantFailure, result.Failures)
			}
		}
	}
}

func TestSuiteRunInvokerError(t *testing.T) {
	suite, err := ParseSuite([]byte(`
cases:
  - name: exploding rule
    request:
      path: /
    expect:
      action: next
`))
	if err != nil {
		t.Fatal(err)
	}
	report := suite.Run(context.Background(), &scriptedInvoker{err: errors.New("guest trapped")})
	if report.Errored != 1 || report.Passed != 0 || report.Failed != 0 {
		t.Fatalf("report counts = %#v", report)
	}
	if report.OK() {
		t.Fatal("report with errors reported OK")
	}
	result := report.Results[0]
	if !result.Errored || len(result.Failures) != 1 || !strings.Contains(result.Failures[0], "invocation error: guest trapped") {
		t.Fatalf("result = %#v", result)
	}
}

func TestSuiteRunBuildRequestErrorCountsAsErrored(t *testing.T) {
	suite := Suite{Cases: []Case{{
		Name:    "bad header",
		Request: CaseRequest{Headers: map[string]any{"X-Bad": 5}},
		Expect:  Expect{Action: strPtr("next")},
	}}}
	invoker := &scriptedInvoker{actions: map[string]switchboard.Action{}}
	report := suite.Run(context.Background(), invoker)
	if report.Errored != 1 {
		t.Fatalf("report = %#v", report)
	}
	if len(invoker.calls) != 0 {
		t.Fatal("invoker called despite request build failure")
	}
}

func TestReportSummaryWithFailures(t *testing.T) {
	report := Report{
		Total:  3,
		Passed: 1,
		Failed: 1, Errored: 1,
		Results: []CaseResult{
			{Name: "good", Passed: true},
			{Name: "bad", Failures: []string{"expect.status: want 403, got 200"}},
			{Name: "broken", Errored: true, Failures: []string{"invocation error: boom"}},
		},
	}
	summary := report.Summary()
	for _, sub := range []string{"1/3 cases passed", "1 failed", "1 errored", `first failure "bad"`, "expect.status: want 403, got 200"} {
		if !strings.Contains(summary, sub) {
			t.Fatalf("summary %q missing %q", summary, sub)
		}
	}
}

func TestReportFormat(t *testing.T) {
	report := Report{
		Total:  3,
		Passed: 1, Failed: 1, Errored: 1,
		Results: []CaseResult{
			{Name: "good", Passed: true},
			{Name: "bad", Failures: []string{"expect.status: want 403, got 200"}},
			{Name: "broken", Errored: true, Failures: []string{"invocation error: boom"}},
		},
	}

	var quiet strings.Builder
	report.Format(&quiet, false)
	out := quiet.String()
	for _, sub := range []string{
		"FAIL  bad",
		"      expect.status: want 403, got 200",
		"ERROR broken",
		"      invocation error: boom",
		"1 passed, 1 failed, 1 errored (3 total)",
	} {
		if !strings.Contains(out, sub) {
			t.Fatalf("format output %q missing %q", out, sub)
		}
	}
	if strings.Contains(out, "PASS") {
		t.Fatalf("non-verbose output lists passing cases: %q", out)
	}

	var verbose strings.Builder
	report.Format(&verbose, true)
	if !strings.Contains(verbose.String(), "PASS  good") {
		t.Fatalf("verbose output missing pass line: %q", verbose.String())
	}
}

// Guard against knownExpectKeys drifting from the Expect struct: every key
// accepted by ParseSuite must round-trip into a populated assertion.
func TestEveryExpectKeyIsAsserted(t *testing.T) {
	for key := range knownExpectKeys {
		value := "1"
		if key == "metadata" || key == "request_headers" || key == "response_headers" {
			value = "{k: v}"
		}
		doc := fmt.Sprintf("cases:\n  - name: probe\n    expect:\n      %s: %s\n", key, value)
		if _, err := ParseSuite([]byte(doc)); err != nil {
			t.Errorf("known key %q rejected: %v", key, err)
		}
	}
}
