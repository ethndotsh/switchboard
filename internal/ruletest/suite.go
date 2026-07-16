// Package ruletest parses and runs declarative behavioral test suites
// against a compiled rule; the CLI and the engine's activation gate share it.
package ruletest

import (
	"fmt"

	"github.com/ethndotsh/switchboard"
	"gopkg.in/yaml.v3"
)

const SuiteSchema = "switchboard.tests/v1"

type Suite struct {
	Schema string `yaml:"schema"`
	Cases  []Case `yaml:"cases"`
}

type Case struct {
	Name    string      `yaml:"name"`
	Request CaseRequest `yaml:"request"`
	Expect  Expect      `yaml:"expect"`
}

type CaseRequest struct {
	Method   string            `yaml:"method"`
	Scheme   string            `yaml:"scheme"`
	Host     string            `yaml:"host"`
	Path     string            `yaml:"path"`
	Query    string            `yaml:"query"`
	ClientIP string            `yaml:"client_ip"`
	TLS      bool              `yaml:"tls"`
	Headers  map[string]any    `yaml:"headers"`
	Cookies  map[string]string `yaml:"cookies"`
}

// Expect asserts only the fields present in the YAML; header and metadata
// maps assert a subset.
type Expect struct {
	Action          *string           `yaml:"action"`
	Status          *int              `yaml:"status"`
	Reason          *string           `yaml:"reason"`
	Location        *string           `yaml:"location"`
	RewritePath     *string           `yaml:"rewrite_path"`
	RewriteHost     *string           `yaml:"rewrite_host"`
	RewriteQuery    *string           `yaml:"rewrite_query"`
	BodyContains    *string           `yaml:"body_contains"`
	Metadata        map[string]string `yaml:"metadata"`
	RequestHeaders  map[string]string `yaml:"request_headers"`
	ResponseHeaders map[string]string `yaml:"response_headers"`
}

var knownExpectKeys = map[string]bool{
	"action": true, "status": true, "reason": true, "location": true,
	"rewrite_path": true, "rewrite_host": true, "rewrite_query": true,
	"body_contains": true, "metadata": true,
	"request_headers": true, "response_headers": true,
}

func ParseSuite(data []byte) (Suite, error) {
	// A typoed expect key would otherwise silently assert nothing.
	var raw struct {
		Schema string `yaml:"schema"`
		Cases  []struct {
			Name    string         `yaml:"name"`
			Request CaseRequest    `yaml:"request"`
			Expect  map[string]any `yaml:"expect"`
		} `yaml:"cases"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Suite{}, fmt.Errorf("parse tests: %w", err)
	}
	if raw.Schema != "" && raw.Schema != SuiteSchema {
		return Suite{}, fmt.Errorf("unsupported tests schema %q (expected %s)", raw.Schema, SuiteSchema)
	}
	if len(raw.Cases) == 0 {
		return Suite{}, fmt.Errorf("tests file declares no cases")
	}
	for i, c := range raw.Cases {
		if c.Name == "" {
			return Suite{}, fmt.Errorf("case %d is missing a name", i+1)
		}
		if len(c.Expect) == 0 {
			return Suite{}, fmt.Errorf("case %q has no expectations", c.Name)
		}
		for key := range c.Expect {
			if !knownExpectKeys[key] {
				return Suite{}, fmt.Errorf("case %q has unknown expect key %q", c.Name, key)
			}
		}
	}

	var suite Suite
	if err := yaml.Unmarshal(data, &suite); err != nil {
		return Suite{}, fmt.Errorf("parse tests: %w", err)
	}
	return suite, nil
}

func (c CaseRequest) BuildRequest() (switchboard.Request, error) {
	req := switchboard.Request{
		Method:   c.Method,
		Scheme:   c.Scheme,
		Host:     c.Host,
		Path:     c.Path,
		RawQuery: c.Query,
		ClientIP: c.ClientIP,
		TLS:      c.TLS,
		Headers:  map[string][]string{},
	}
	if req.Method == "" {
		req.Method = "GET"
	}
	if req.Path == "" {
		req.Path = "/"
	}
	if req.Scheme == "" {
		if c.TLS {
			req.Scheme = "https"
		} else {
			req.Scheme = "http"
		}
	}
	for name, value := range c.Headers {
		switch v := value.(type) {
		case string:
			req.Headers[name] = append(req.Headers[name], v)
		case []any:
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					return switchboard.Request{}, fmt.Errorf("header %q has a non-string value", name)
				}
				req.Headers[name] = append(req.Headers[name], s)
			}
		default:
			return switchboard.Request{}, fmt.Errorf("header %q must be a string or list of strings", name)
		}
	}
	for name, value := range c.Cookies {
		req.Headers["Cookie"] = append(req.Headers["Cookie"], name+"="+value)
	}
	return req, nil
}
