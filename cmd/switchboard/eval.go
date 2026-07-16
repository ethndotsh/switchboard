package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/ethndotsh/switchboard"
	"github.com/ethndotsh/switchboard/registry"
)

type headerFlags map[string][]string

func (h headerFlags) String() string { return "" }

func (h headerFlags) Set(value string) error {
	name, headerValue, found := strings.Cut(value, ":")
	if !found || strings.TrimSpace(name) == "" {
		return fmt.Errorf("header %q must be in \"Name: value\" form", value)
	}
	h[strings.TrimSpace(name)] = append(h[strings.TrimSpace(name)], strings.TrimSpace(headerValue))
	return nil
}

func evalCommand(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "method", "scheme", "host", "path", "query", "client-ip", "H", "namespace", "registry", "invoke-timeout")
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	method := fs.String("method", "GET", "request method")
	scheme := fs.String("scheme", "http", "request scheme")
	host := fs.String("host", "", "request host")
	path := fs.String("path", "/", "request path")
	query := fs.String("query", "", "raw query string")
	clientIP := fs.String("client-ip", "", "client IP as seen by the proxy")
	tls := fs.Bool("tls", false, "mark the request as TLS")
	headers := headerFlags{}
	fs.Var(headers, "H", "request header \"Name: value\" (repeatable)")
	namespace := fs.String("namespace", cfg.Namespace, "namespace for registry refs")
	registryURL := fs.String("registry", cfg.Registry, "registry URL for channel or bundle refs")
	invokeTimeout := fs.String("invoke-timeout", "", "invocation timeout (default 50ms)")
	asJSON := fs.Bool("json", false, "print the action as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: switchboard eval [REF] --method GET --path /admin -H \"x-user-role: viewer\"")
	}
	ref := cfg.Dist
	if fs.NArg() == 1 {
		ref = fs.Arg(0)
	}

	scope := registry.Scope{Namespace: *namespace}
	b, err := resolveBundleRef(ctx, ref, scope, *registryURL)
	if err != nil {
		return err
	}
	runtime, cleanup, err := loadRuntimeForBundle(ctx, b, *invokeTimeout)
	if err != nil {
		return err
	}
	defer cleanup()

	req := switchboard.Request{
		Method:   strings.ToUpper(*method),
		Scheme:   *scheme,
		Host:     *host,
		Path:     *path,
		RawQuery: *query,
		ClientIP: *clientIP,
		TLS:      *tls,
		Headers:  headers,
	}
	if *tls && *scheme == "http" {
		req.Scheme = "https"
	}
	action, err := runtime.Invoke(ctx, req)
	if err != nil {
		return fmt.Errorf("invocation failed: %w", err)
	}
	if *asJSON {
		return printJSON(action)
	}
	printAction(action)
	return nil
}

func printAction(action switchboard.Action) {
	fmt.Printf("decision:  %s\n", action.Decision)
	if action.Reason != "" {
		fmt.Printf("reason:    %s\n", action.Reason)
	}
	if action.Response.Status != 0 {
		fmt.Printf("status:    %d\n", action.Response.Status)
	}
	if action.Response.Location != "" {
		fmt.Printf("location:  %s\n", action.Response.Location)
	}
	if len(action.Response.Body) > 0 {
		fmt.Printf("body:      %d bytes\n", len(action.Response.Body))
	}
	if action.Patch.Host != nil {
		fmt.Printf("rewrite host:  %s\n", *action.Patch.Host)
	}
	if action.Patch.Path != nil {
		fmt.Printf("rewrite path:  %s\n", *action.Patch.Path)
	}
	if action.Patch.Query != nil {
		fmt.Printf("rewrite query: %s\n", *action.Patch.Query)
	}
	printHeaderOps("request header", action.Patch.Headers)
	printHeaderOps("response header", action.Response.Headers)
	if len(action.Metadata) > 0 {
		keys := make([]string, 0, len(action.Metadata))
		for key := range action.Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Printf("metadata:  %s=%s\n", key, action.Metadata[key])
		}
	}
}

func printHeaderOps(label string, ops []switchboard.HeaderOp) {
	for _, op := range ops {
		switch op.Op {
		case switchboard.HeaderOpDelete:
			fmt.Printf("%s: delete %s\n", label, op.Name)
		default:
			fmt.Printf("%s: %s %s: %s\n", label, op.Op, op.Name, op.Value)
		}
	}
}
