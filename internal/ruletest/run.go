package ruletest

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethndotsh/switchboard"
)

type Invoker interface {
	Invoke(ctx context.Context, req switchboard.Request) (switchboard.Action, error)
}

type CaseResult struct {
	Name     string   `json:"name"`
	Passed   bool     `json:"passed"`
	Errored  bool     `json:"errored,omitempty"`
	Failures []string `json:"failures,omitempty"`
}

type Report struct {
	Total   int          `json:"total"`
	Passed  int          `json:"passed"`
	Failed  int          `json:"failed"`
	Errored int          `json:"errored"`
	Results []CaseResult `json:"results"`
}

func (r Report) OK() bool {
	return r.Failed == 0 && r.Errored == 0
}

func (r Report) Summary() string {
	if r.OK() {
		return fmt.Sprintf("%d/%d cases passed", r.Passed, r.Total)
	}
	detail := ""
	for _, result := range r.Results {
		if !result.Passed {
			suffix := ""
			if len(result.Failures) > 0 {
				suffix = ": " + result.Failures[0]
			}
			detail = fmt.Sprintf(" (first failure %q%s)", result.Name, suffix)
			break
		}
	}
	return fmt.Sprintf("%d/%d cases passed, %d failed, %d errored%s", r.Passed, r.Total, r.Failed, r.Errored, detail)
}

func (s Suite) Run(ctx context.Context, invoker Invoker) Report {
	report := Report{Total: len(s.Cases)}
	for _, testCase := range s.Cases {
		result := CaseResult{Name: testCase.Name}
		req, err := testCase.Request.BuildRequest()
		if err != nil {
			result.Errored = true
			result.Failures = []string{err.Error()}
			report.Errored++
			report.Results = append(report.Results, result)
			continue
		}
		action, err := invoker.Invoke(ctx, req)
		if err != nil {
			result.Errored = true
			result.Failures = []string{fmt.Sprintf("invocation error: %v", err)}
			report.Errored++
			report.Results = append(report.Results, result)
			continue
		}
		result.Failures = matchAction(testCase.Expect, action)
		result.Passed = len(result.Failures) == 0
		if result.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Results = append(report.Results, result)
	}
	return report
}

// matchAction holds all expect-to-action field knowledge in one place.
func matchAction(expect Expect, action switchboard.Action) []string {
	var failures []string
	fail := func(format string, args ...any) {
		failures = append(failures, fmt.Sprintf(format, args...))
	}
	if expect.Action != nil && string(action.Decision) != *expect.Action {
		fail("expect.action: want %q, got %q", *expect.Action, action.Decision)
	}
	if expect.Status != nil && action.Response.Status != *expect.Status {
		fail("expect.status: want %d, got %d", *expect.Status, action.Response.Status)
	}
	if expect.Reason != nil && action.Reason != *expect.Reason {
		fail("expect.reason: want %q, got %q", *expect.Reason, action.Reason)
	}
	if expect.Location != nil && action.Response.Location != *expect.Location {
		fail("expect.location: want %q, got %q", *expect.Location, action.Response.Location)
	}
	if expect.RewritePath != nil {
		if action.Patch.Path == nil {
			fail("expect.rewrite_path: want %q, got no path rewrite", *expect.RewritePath)
		} else if *action.Patch.Path != *expect.RewritePath {
			fail("expect.rewrite_path: want %q, got %q", *expect.RewritePath, *action.Patch.Path)
		}
	}
	if expect.RewriteHost != nil {
		if action.Patch.Host == nil {
			fail("expect.rewrite_host: want %q, got no host rewrite", *expect.RewriteHost)
		} else if *action.Patch.Host != *expect.RewriteHost {
			fail("expect.rewrite_host: want %q, got %q", *expect.RewriteHost, *action.Patch.Host)
		}
	}
	if expect.RewriteQuery != nil {
		if action.Patch.Query == nil {
			fail("expect.rewrite_query: want %q, got no query rewrite", *expect.RewriteQuery)
		} else if *action.Patch.Query != *expect.RewriteQuery {
			fail("expect.rewrite_query: want %q, got %q", *expect.RewriteQuery, *action.Patch.Query)
		}
	}
	if expect.BodyContains != nil && !strings.Contains(string(action.Response.Body), *expect.BodyContains) {
		fail("expect.body_contains: %q not found in response body", *expect.BodyContains)
	}
	for key, want := range expect.Metadata {
		got, ok := action.Metadata[key]
		if !ok {
			fail("expect.metadata[%s]: want %q, key not set", key, want)
		} else if got != want {
			fail("expect.metadata[%s]: want %q, got %q", key, want, got)
		}
	}
	assertHeaderOps(expect.RequestHeaders, action.Patch.Headers, "request_headers", fail)
	assertHeaderOps(expect.ResponseHeaders, action.Response.Headers, "response_headers", fail)
	return failures
}

// assertHeaderOps checks the effective value of each expected header after
// applying the action's ops in order.
func assertHeaderOps(expected map[string]string, ops []switchboard.HeaderOp, label string, fail func(string, ...any)) {
	if len(expected) == 0 {
		return
	}
	effective := map[string][]string{}
	for _, op := range ops {
		key := strings.ToLower(op.Name)
		switch op.Op {
		case switchboard.HeaderOpSet:
			effective[key] = []string{op.Value}
		case switchboard.HeaderOpAdd:
			effective[key] = append(effective[key], op.Value)
		case switchboard.HeaderOpDelete:
			delete(effective, key)
		}
	}
	for name, want := range expected {
		values := effective[strings.ToLower(name)]
		if len(values) == 0 {
			fail("expect.%s[%s]: want %q, header not set", label, name, want)
			continue
		}
		found := false
		for _, value := range values {
			if value == want {
				found = true
				break
			}
		}
		if !found {
			fail("expect.%s[%s]: want %q, got %q", label, name, want, strings.Join(values, ", "))
		}
	}
}
