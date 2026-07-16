package replay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethndotsh/switchboard"
)

// fakeInvoker returns actions from a scripted function and records every
// request it sees.
type fakeInvoker struct {
	fn    func(req switchboard.Request) (switchboard.Action, error)
	calls []switchboard.Request
}

func (f *fakeInvoker) Invoke(_ context.Context, req switchboard.Request) (switchboard.Action, error) {
	f.calls = append(f.calls, req)
	if f.fn == nil {
		return switchboard.Action{Decision: switchboard.DecisionNext}, nil
	}
	return f.fn(req)
}

func nextAction() (switchboard.Action, error) {
	return switchboard.Action{Decision: switchboard.DecisionNext}, nil
}

func openFixture(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "access.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestRunAgainstGoldenLog(t *testing.T) {
	current := &fakeInvoker{}
	candidate := &fakeInvoker{fn: func(req switchboard.Request) (switchboard.Action, error) {
		if req.Path == "/admin" {
			return switchboard.Action{
				Decision: switchboard.DecisionDeny,
				Response: switchboard.Response{Status: 403},
				Reason:   "admin locked down",
			}, nil
		}
		return nextAction()
	}}

	var verbose strings.Builder
	report, err := Run(context.Background(), openFixture(t), current, candidate, Options{Verbose: true, Writer: &verbose})
	if err != nil {
		t.Fatal(err)
	}

	if report.Processed != 6 {
		t.Fatalf("processed = %d", report.Processed)
	}
	if report.SkippedLines != 2 {
		t.Fatalf("skipped lines = %d", report.SkippedLines)
	}
	if report.Same != 5 {
		t.Fatalf("same decisions = %d", report.Same)
	}
	if report.NewDenials != 1 || report.ChangedDecisions != 1 {
		t.Fatalf("denial counts = %#v", report)
	}
	if report.CurrentErrors != 0 || report.CandidateErrors != 0 {
		t.Fatalf("error counts = %#v", report)
	}
	if len(current.calls) != 6 || len(candidate.calls) != 6 {
		t.Fatalf("invoker calls = %d / %d", len(current.calls), len(candidate.calls))
	}

	if len(report.SampledDifferences) != 1 {
		t.Fatalf("sampled differences = %#v", report.SampledDifferences)
	}
	diff := report.SampledDifferences[0]
	if diff.Line != 7 || diff.Method != "GET" || diff.Path != "/admin" || diff.Kind != "new-denial" {
		t.Fatalf("difference = %#v", diff)
	}
	if diff.Current != "next" || !strings.Contains(diff.Candidat, "deny 403") {
		t.Fatalf("difference summaries = %#v", diff)
	}
	if !strings.Contains(verbose.String(), "line 7 GET /admin: new-denial") {
		t.Fatalf("verbose output = %q", verbose.String())
	}

	if report.CandidateP50Micros < 0 || report.CandidateP99Micros < report.CandidateP50Micros {
		t.Fatalf("quantiles = %v / %v", report.CandidateP50Micros, report.CandidateP99Micros)
	}
}

func TestRunRequestFieldMapping(t *testing.T) {
	current := &fakeInvoker{}
	if _, err := Run(context.Background(), openFixture(t), current, &fakeInvoker{}, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(current.calls) != 6 {
		t.Fatalf("calls = %d", len(current.calls))
	}

	// Line 1: TLS request with a query string and an explicit client_ip.
	first := current.calls[0]
	if first.Method != "GET" || first.Host != "example.com" || first.Protocol != "HTTP/2.0" {
		t.Fatalf("first request = %#v", first)
	}
	if first.Path != "/path" || first.RawQuery != "q=1" {
		t.Fatalf("uri split = path %q query %q", first.Path, first.RawQuery)
	}
	if !first.TLS || first.Scheme != "https" {
		t.Fatalf("tls mapping = tls %v scheme %q", first.TLS, first.Scheme)
	}
	if first.ClientIP != "203.0.113.5" || first.RemoteAddr != "10.0.0.1" {
		t.Fatalf("ip mapping = client %q remote %q", first.ClientIP, first.RemoteAddr)
	}
	if got := first.Headers["User-Agent"]; len(got) != 1 || got[0] != "curl/8.5.0" {
		t.Fatalf("headers = %v", first.Headers)
	}

	// Line 3: plaintext request without client_ip falls back to remote_ip.
	second := current.calls[1]
	if second.TLS || second.Scheme != "http" {
		t.Fatalf("plaintext mapping = tls %v scheme %q", second.TLS, second.Scheme)
	}
	if second.Path != "/" || second.RawQuery != "" {
		t.Fatalf("query-free uri split = path %q query %q", second.Path, second.RawQuery)
	}
	if second.ClientIP != "192.0.2.44" || second.RemoteAddr != "192.0.2.44" {
		t.Fatalf("client_ip fallback = client %q remote %q", second.ClientIP, second.RemoteAddr)
	}

	// Last line: request with empty headers object still gets a non-nil map.
	last := current.calls[5]
	if last.Method != "HEAD" || last.Path != "/health" {
		t.Fatalf("last request = %#v", last)
	}
	if last.Headers == nil {
		t.Fatal("headers not defaulted to an empty map")
	}
}

func TestRunCountsInvokerErrors(t *testing.T) {
	logs := strings.NewReader(`{"logger":"http.log.access","request":{"remote_ip":"10.0.0.1","method":"GET","host":"example.com","uri":"/a","headers":{}},"status":200}
{"logger":"http.log.access","request":{"remote_ip":"10.0.0.1","method":"GET","host":"example.com","uri":"/b","headers":{}},"status":200}
`)
	candidate := &fakeInvoker{fn: func(req switchboard.Request) (switchboard.Action, error) {
		if req.Path == "/b" {
			return switchboard.Action{}, errors.New("guest trapped")
		}
		return nextAction()
	}}
	report, err := Run(context.Background(), logs, &fakeInvoker{}, candidate, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Processed != 2 || report.CandidateErrors != 1 || report.CurrentErrors != 0 {
		t.Fatalf("report = %#v", report)
	}
	// Errored lines are not classified.
	if report.Same != 1 || report.ChangedDecisions != 0 {
		t.Fatalf("classification counts = %#v", report)
	}
}

func strPtr(s string) *string { return &s }

func TestClassify(t *testing.T) {
	next := switchboard.Action{Decision: switchboard.DecisionNext}
	deny403 := switchboard.Action{Decision: switchboard.DecisionDeny, Response: switchboard.Response{Status: 403}}
	deny404 := switchboard.Action{Decision: switchboard.DecisionDeny, Response: switchboard.Response{Status: 404}}
	redirectA := switchboard.Action{Decision: switchboard.DecisionRedirect, Response: switchboard.Response{Status: 302, Location: "https://a.example"}}
	redirectB := switchboard.Action{Decision: switchboard.DecisionRedirect, Response: switchboard.Response{Status: 302, Location: "https://b.example"}}
	rewriteA := switchboard.Action{Decision: switchboard.DecisionRewrite, Patch: switchboard.RequestPatch{Path: strPtr("/a")}}
	rewriteB := switchboard.Action{Decision: switchboard.DecisionRewrite, Patch: switchboard.RequestPatch{Path: strPtr("/b")}}
	headerOpsA := switchboard.Action{Decision: switchboard.DecisionNext, Patch: switchboard.RequestPatch{Headers: []switchboard.HeaderOp{
		{Op: switchboard.HeaderOpSet, Name: "X-A", Value: "1"},
	}}}
	headerOpsB := switchboard.Action{Decision: switchboard.DecisionNext, Patch: switchboard.RequestPatch{Headers: []switchboard.HeaderOp{
		{Op: switchboard.HeaderOpSet, Name: "X-A", Value: "2"},
	}}}
	respHeadersA := switchboard.Action{Decision: switchboard.DecisionNext, Response: switchboard.Response{Headers: []switchboard.HeaderOp{
		{Op: switchboard.HeaderOpSet, Name: "X-R", Value: "1"},
	}}}
	metaA := switchboard.Action{Decision: switchboard.DecisionNext, Metadata: map[string]string{"rule": "a"}}
	metaB := switchboard.Action{Decision: switchboard.DecisionNext, Metadata: map[string]string{"rule": "b"}}
	respond500 := switchboard.Action{Decision: switchboard.DecisionRespond, Response: switchboard.Response{Status: 500}}
	respond200 := switchboard.Action{Decision: switchboard.DecisionRespond, Response: switchboard.Response{Status: 200}}

	tests := []struct {
		name      string
		current   switchboard.Action
		candidate switchboard.Action
		want      string
	}{
		{"identical next", next, next, ""},
		{"identical deny", deny403, deny403, ""},
		{"identical rewrite by value", rewriteA, switchboard.Action{Decision: switchboard.DecisionRewrite, Patch: switchboard.RequestPatch{Path: strPtr("/a")}}, ""},
		{"next to deny", next, deny403, "new-denial"},
		{"deny to next", deny403, next, "lifted-denial"},
		{"deny status change", deny403, deny404, "changed-decision"},
		{"next to redirect", next, redirectA, "changed-decision"},
		{"redirect location change", redirectA, redirectB, "changed-redirect"},
		{"redirect status change", redirectA, switchboard.Action{Decision: switchboard.DecisionRedirect, Response: switchboard.Response{Status: 301, Location: "https://a.example"}}, "changed-redirect"},
		{"rewrite path change", rewriteA, rewriteB, "changed-rewrite"},
		{"rewrite added", next, switchboard.Action{Decision: switchboard.DecisionNext, Patch: switchboard.RequestPatch{Path: strPtr("/a")}}, "changed-rewrite"},
		{"request header ops change", headerOpsA, headerOpsB, "changed-header-ops"},
		{"request header ops added", next, headerOpsA, "changed-header-ops"},
		{"response header ops added", next, respHeadersA, "changed-header-ops"},
		{"metadata change", metaA, metaB, "changed-metadata"},
		{"metadata added", next, metaA, "changed-metadata"},
		{"respond 500 counts as denial", next, respond500, "new-denial"},
		{"respond 500 lifted", respond500, next, "lifted-denial"},
		{"respond 200 is not a denial", next, respond200, "changed-decision"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(tt.current, tt.candidate); got != tt.want {
				t.Fatalf("classify() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuantile(t *testing.T) {
	if got := quantile(nil, 0.5); got != 0 {
		t.Fatalf("quantile(nil) = %v", got)
	}
	if got := quantile([]float64{}, 0.99); got != 0 {
		t.Fatalf("quantile(empty) = %v", got)
	}
	// Unsorted input; quantile sorts a copy.
	values := []float64{9, 1, 7, 3, 5, 6, 4, 8, 2, 10}
	if got := quantile(values, 0.50); got != 5 {
		t.Fatalf("p50 = %v", got)
	}
	if got := quantile(values, 0.99); got != 9 {
		t.Fatalf("p99 = %v", got)
	}
	if got := quantile(values, 1.0); got != 10 {
		t.Fatalf("p100 = %v", got)
	}
	if values[0] != 9 {
		t.Fatal("quantile mutated its input")
	}
	if got := quantile([]float64{42}, 0.99); got != 42 {
		t.Fatalf("single value p99 = %v", got)
	}
}
