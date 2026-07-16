package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/ethndotsh/switchboard/internal/replay"
	"github.com/ethndotsh/switchboard/registry"
)

func replayCommand(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "current", "candidate", "namespace", "registry", "invoke-timeout")
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	currentRef := fs.String("current", "", "current bundle ref (dist dir, channel, or bundle id)")
	candidateRef := fs.String("candidate", cfg.Dist, "candidate bundle ref")
	namespace := fs.String("namespace", cfg.Namespace, "namespace for registry refs")
	registryURL := fs.String("registry", cfg.Registry, "registry URL for channel or bundle refs")
	invokeTimeout := fs.String("invoke-timeout", "", "invocation timeout (default 50ms)")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	verbose := fs.Bool("verbose", false, "stream every difference as it is found")
	failOnChange := fs.Bool("fail-on-change", false, "exit non-zero when any decision changed")
	failOnNewDenials := fs.Bool("fail-on-new-denials", false, "exit non-zero when the candidate denies requests the current allows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: switchboard replay ./access.jsonl --current prod --candidate ./dist")
	}
	if *currentRef == "" {
		return errors.New("--current is required (a dist dir, channel name, or bundle id)")
	}

	scope := registry.Scope{Namespace: *namespace}
	currentBundle, err := resolveBundleRef(ctx, *currentRef, scope, *registryURL)
	if err != nil {
		return fmt.Errorf("resolve --current: %w", err)
	}
	candidateBundle, err := resolveBundleRef(ctx, *candidateRef, scope, *registryURL)
	if err != nil {
		return fmt.Errorf("resolve --candidate: %w", err)
	}

	current, cleanupCurrent, err := loadRuntimeForBundle(ctx, currentBundle, *invokeTimeout)
	if err != nil {
		return err
	}
	defer cleanupCurrent()
	candidate, cleanupCandidate, err := loadRuntimeForBundle(ctx, candidateBundle, *invokeTimeout)
	if err != nil {
		return err
	}
	defer cleanupCandidate()

	logFile, err := os.Open(fs.Arg(0))
	if err != nil {
		return err
	}
	defer logFile.Close()

	report, err := replay.Run(ctx, logFile, current, candidate, replay.Options{Verbose: *verbose, Writer: os.Stderr})
	if err != nil {
		return err
	}
	if *asJSON {
		if err := printJSON(report); err != nil {
			return err
		}
	} else {
		report.Format(os.Stdout)
	}
	if *failOnNewDenials && report.NewDenials > 0 {
		return fmt.Errorf("candidate introduces %d new denials", report.NewDenials)
	}
	totalChanges := report.ChangedDecisions + report.ChangedRedirects + report.ChangedRewrites + report.ChangedHeaders + report.ChangedMetadata
	if *failOnChange && totalChanges > 0 {
		return fmt.Errorf("candidate changes %d decisions", totalChanges)
	}
	if report.CandidateErrors > 0 {
		return fmt.Errorf("candidate errored on %d requests", report.CandidateErrors)
	}
	return nil
}
