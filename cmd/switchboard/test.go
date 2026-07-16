package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/ethndotsh/switchboard/internal/ruletest"
	"github.com/ethndotsh/switchboard/registry"
)

func testCommand(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "cases", "namespace", "registry", "invoke-timeout")
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cases := fs.String("cases", "", "tests.yaml to run (default: the suite embedded in the bundle)")
	namespace := fs.String("namespace", cfg.Namespace, "namespace for registry refs")
	registryURL := fs.String("registry", cfg.Registry, "registry URL for channel or bundle refs")
	invokeTimeout := fs.String("invoke-timeout", "", "invocation timeout (default 50ms)")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	verbose := fs.Bool("verbose", false, "list passing cases too")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: switchboard test [REF] [--cases tests.yaml]")
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

	var suiteData []byte
	switch {
	case *cases != "":
		suiteData, err = os.ReadFile(*cases)
		if err != nil {
			return err
		}
	case len(b.Tests) > 0:
		suiteData = b.Tests
	default:
		implicit, err := os.ReadFile("tests.yaml")
		if err != nil {
			return errors.New("no test cases: pass --cases tests.yaml, embed a suite with switchboard build, or add tests.yaml to the working directory")
		}
		suiteData = implicit
	}
	suite, err := ruletest.ParseSuite(suiteData)
	if err != nil {
		return err
	}

	runtime, cleanup, err := loadRuntimeForBundle(ctx, b, *invokeTimeout)
	if err != nil {
		return err
	}
	defer cleanup()

	report := suite.Run(ctx, runtime)
	if *asJSON {
		if err := printJSON(report); err != nil {
			return err
		}
	} else {
		report.Format(os.Stdout, *verbose)
	}
	if !report.OK() {
		return fmt.Errorf("bundle %s failed its test suite", abbreviateBundleID(b.ID))
	}
	return nil
}
