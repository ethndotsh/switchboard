// Package replay runs captured Caddy access logs through two rule bundles
// and reports how their decisions differ.
package replay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/ethndotsh/switchboard"
)

type Invoker interface {
	Invoke(ctx context.Context, req switchboard.Request) (switchboard.Action, error)
}

// caddyAccessLine is the subset of Caddy's JSON access log the replayer
// needs; lines without request.method are skipped.
type caddyAccessLine struct {
	Request struct {
		RemoteIP string              `json:"remote_ip"`
		ClientIP string              `json:"client_ip"`
		Proto    string              `json:"proto"`
		Method   string              `json:"method"`
		Host     string              `json:"host"`
		URI      string              `json:"uri"`
		Headers  map[string][]string `json:"headers"`
		TLS      *struct{}           `json:"tls"`
	} `json:"request"`
}

type Difference struct {
	Line     int    `json:"line"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Current  string `json:"current"`
	Candidat string `json:"candidate"`
}

type Report struct {
	Processed        int64 `json:"requests_processed"`
	SkippedLines     int64 `json:"skipped_lines"`
	Same             int64 `json:"same_decisions"`
	ChangedDecisions int64 `json:"changed_decisions"`
	NewDenials       int64 `json:"new_denials"`
	LiftedDenials    int64 `json:"lifted_denials"`
	ChangedRedirects int64 `json:"changed_redirects"`
	ChangedRewrites  int64 `json:"changed_rewrites"`
	ChangedHeaders   int64 `json:"changed_header_ops"`
	ChangedMetadata  int64 `json:"changed_metadata"`
	CurrentErrors    int64 `json:"current_errors"`
	CandidateErrors  int64 `json:"candidate_errors"`

	CandidateP50Micros float64 `json:"candidate_p50_us"`
	CandidateP99Micros float64 `json:"candidate_p99_us"`

	SampledDifferences []Difference `json:"sampled_differences,omitempty"`
}

const (
	maxReservoir          = 1_000_000
	maxSampledDifferences = 50
	maxLineBytes          = 1 << 20
)

type Options struct {
	Verbose bool
	Writer  io.Writer
}

// Run streams the log through both invokers with bounded memory.
func Run(ctx context.Context, logs io.Reader, current, candidate Invoker, opts Options) (Report, error) {
	report := Report{}
	timings := make([]float64, 0, 4096)
	seen := int64(0)
	rng := rand.New(rand.NewSource(1))

	scanner := bufio.NewScanner(logs)
	scanner.Buffer(make([]byte, 64<<10), maxLineBytes)
	lineNo := 0
	for scanner.Scan() {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var parsed caddyAccessLine
		if err := json.Unmarshal(line, &parsed); err != nil || parsed.Request.Method == "" {
			report.SkippedLines++
			continue
		}
		req := requestFromLog(parsed)
		report.Processed++

		currentAction, currentErr := current.Invoke(ctx, req)
		start := time.Now()
		candidateAction, candidateErr := candidate.Invoke(ctx, req)
		elapsed := float64(time.Since(start).Nanoseconds()) / 1e3

		seen++
		if len(timings) < maxReservoir {
			timings = append(timings, elapsed)
		} else if slot := rng.Int63n(seen); slot < maxReservoir {
			timings[slot] = elapsed
		}

		if currentErr != nil {
			report.CurrentErrors++
		}
		if candidateErr != nil {
			report.CandidateErrors++
		}
		if currentErr != nil || candidateErr != nil {
			continue
		}
		diff := classify(currentAction, candidateAction)
		if diff == "" {
			report.Same++
			continue
		}
		recordDifference(&report, diff, currentAction, candidateAction)
		if opts.Verbose && opts.Writer != nil {
			fmt.Fprintf(opts.Writer, "line %d %s %s: %s (%s -> %s)\n",
				lineNo, req.Method, req.Path, diff, summarizeAction(currentAction), summarizeAction(candidateAction))
		}
		if len(report.SampledDifferences) < maxSampledDifferences {
			report.SampledDifferences = append(report.SampledDifferences, Difference{
				Line:     lineNo,
				Method:   req.Method,
				Path:     req.Path,
				Kind:     diff,
				Current:  summarizeAction(currentAction),
				Candidat: summarizeAction(candidateAction),
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return report, err
	}
	report.CandidateP50Micros = quantile(timings, 0.50)
	report.CandidateP99Micros = quantile(timings, 0.99)
	return report, nil
}

func requestFromLog(line caddyAccessLine) switchboard.Request {
	path, query, _ := strings.Cut(line.Request.URI, "?")
	scheme := "http"
	tls := line.Request.TLS != nil
	if tls {
		scheme = "https"
	}
	clientIP := line.Request.ClientIP
	if clientIP == "" {
		clientIP = line.Request.RemoteIP
	}
	headers := line.Request.Headers
	if headers == nil {
		headers = map[string][]string{}
	}
	return switchboard.Request{
		Method:     line.Request.Method,
		Scheme:     scheme,
		Host:       line.Request.Host,
		Path:       path,
		RawQuery:   query,
		Protocol:   line.Request.Proto,
		Headers:    headers,
		RemoteAddr: line.Request.RemoteIP,
		ClientIP:   clientIP,
		TLS:        tls,
	}
}

// classify names the first meaningful difference, or "" when equivalent.
func classify(current, candidate switchboard.Action) string {
	currentDenies := isDenial(current)
	candidateDenies := isDenial(candidate)
	if !currentDenies && candidateDenies {
		return "new-denial"
	}
	if currentDenies && !candidateDenies {
		return "lifted-denial"
	}
	if current.Decision != candidate.Decision {
		return "changed-decision"
	}
	switch current.Decision {
	case switchboard.DecisionRedirect:
		if current.Response.Location != candidate.Response.Location || current.Response.Status != candidate.Response.Status {
			return "changed-redirect"
		}
	case switchboard.DecisionDeny, switchboard.DecisionRespond:
		if current.Response.Status != candidate.Response.Status {
			return "changed-decision"
		}
	}
	if !patchEqual(current.Patch, candidate.Patch) {
		if pointerChanged(current.Patch.Path, candidate.Patch.Path) ||
			pointerChanged(current.Patch.Host, candidate.Patch.Host) ||
			pointerChanged(current.Patch.Query, candidate.Patch.Query) {
			return "changed-rewrite"
		}
		return "changed-header-ops"
	}
	if !headerOpsEqual(current.Response.Headers, candidate.Response.Headers) {
		return "changed-header-ops"
	}
	if !metadataEqual(current.Metadata, candidate.Metadata) {
		return "changed-metadata"
	}
	return ""
}

func recordDifference(report *Report, kind string, current, candidate switchboard.Action) {
	switch kind {
	case "new-denial":
		report.NewDenials++
		report.ChangedDecisions++
	case "lifted-denial":
		report.LiftedDenials++
		report.ChangedDecisions++
	case "changed-decision":
		report.ChangedDecisions++
	case "changed-redirect":
		report.ChangedRedirects++
	case "changed-rewrite":
		report.ChangedRewrites++
	case "changed-header-ops":
		report.ChangedHeaders++
	case "changed-metadata":
		report.ChangedMetadata++
	}
}

func isDenial(action switchboard.Action) bool {
	if action.Decision == switchboard.DecisionDeny {
		return true
	}
	return action.Decision == switchboard.DecisionRespond && action.Response.Status >= 400
}

func pointerChanged(a, b *string) bool {
	if a == nil && b == nil {
		return false
	}
	if a == nil || b == nil {
		return true
	}
	return *a != *b
}

func patchEqual(a, b switchboard.RequestPatch) bool {
	return !pointerChanged(a.Host, b.Host) &&
		!pointerChanged(a.Path, b.Path) &&
		!pointerChanged(a.Query, b.Query) &&
		headerOpsEqual(a.Headers, b.Headers)
}

func headerOpsEqual(a, b []switchboard.HeaderOp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func metadataEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func summarizeAction(action switchboard.Action) string {
	var parts []string
	parts = append(parts, string(action.Decision))
	if action.Response.Status != 0 {
		parts = append(parts, fmt.Sprintf("%d", action.Response.Status))
	}
	if action.Response.Location != "" {
		parts = append(parts, "-> "+action.Response.Location)
	}
	if action.Patch.Path != nil {
		parts = append(parts, "path="+*action.Patch.Path)
	}
	if action.Reason != "" {
		parts = append(parts, "("+action.Reason+")")
	}
	return strings.Join(parts, " ")
}

func quantile(values []float64, q float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int(q * float64(len(sorted)-1))
	return sorted[index]
}

func (r Report) Format(w io.Writer) {
	fmt.Fprintf(w, "Requests processed:     %d\n", r.Processed)
	if r.SkippedLines > 0 {
		fmt.Fprintf(w, "Skipped log lines:      %d\n", r.SkippedLines)
	}
	fmt.Fprintf(w, "Same decisions:         %d\n", r.Same)
	fmt.Fprintf(w, "Changed decisions:      %d\n", r.ChangedDecisions)
	fmt.Fprintf(w, "New denials:            %d\n", r.NewDenials)
	fmt.Fprintf(w, "Lifted denials:         %d\n", r.LiftedDenials)
	fmt.Fprintf(w, "Changed redirects:      %d\n", r.ChangedRedirects)
	fmt.Fprintf(w, "Changed rewrites:       %d\n", r.ChangedRewrites)
	fmt.Fprintf(w, "Changed header ops:     %d\n", r.ChangedHeaders)
	fmt.Fprintf(w, "Changed metadata:       %d\n", r.ChangedMetadata)
	fmt.Fprintf(w, "Current errors:         %d\n", r.CurrentErrors)
	fmt.Fprintf(w, "Candidate errors:       %d\n", r.CandidateErrors)
	fmt.Fprintf(w, "Candidate p50 exec:     %.1f us\n", r.CandidateP50Micros)
	fmt.Fprintf(w, "Candidate p99 exec:     %.1f us\n", r.CandidateP99Micros)
}
