package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/ethndotsh/switchboard/internal/ruletest"
	"github.com/ethndotsh/switchboard/registry"
)

func statusCommand(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "namespace", "channel", "registry")
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	namespace := fs.String("namespace", cfg.Namespace, "namespace")
	channel := fs.String("channel", cfg.Channel, "channel name")
	registryURL := fs.String("registry", cfg.Registry, "registry URL")
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	scope := registry.Scope{Namespace: *namespace}
	reg, err := openRegistry(ctx, *registryURL)
	if err != nil {
		return err
	}
	pointer, err := reg.GetChannel(ctx, scope, *channel)
	if err != nil {
		return err
	}
	var revision *bundle.Revision
	if pointer.Generation > 0 {
		if rev, err := reg.GetRevision(ctx, scope, *channel, pointer.Generation); err == nil {
			revision = &rev
		}
	}
	if *asJSON {
		return printJSON(map[string]any{"pointer": pointer, "revision": revision})
	}
	fmt.Printf("channel:    %s\n", pointer.Channel)
	if pointer.Namespace != "" {
		fmt.Printf("namespace:  %s\n", pointer.Namespace)
	}
	fmt.Printf("bundle:     %s\n", abbreviateBundleID(pointer.BundleID))
	if pointer.Generation > 0 {
		fmt.Printf("generation: %d\n", pointer.Generation)
	}
	fmt.Printf("updated:    %s\n", pointer.CreatedAt.Format(time.RFC3339))
	if revision != nil {
		if revision.DeployedBy != "" {
			fmt.Printf("deployed by: %s\n", revision.DeployedBy)
		}
		if revision.SourceCommit != "" {
			fmt.Printf("commit:      %s\n", revision.SourceCommit)
		}
		if revision.Message != "" {
			fmt.Printf("message:     %s\n", revision.Message)
		}
	}
	return nil
}

func historyCommand(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "namespace", "channel", "registry", "limit")
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	namespace := fs.String("namespace", cfg.Namespace, "namespace")
	channel := fs.String("channel", cfg.Channel, "channel name")
	registryURL := fs.String("registry", cfg.Registry, "registry URL")
	limit := fs.Int("limit", 20, "maximum revisions to list")
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	scope := registry.Scope{Namespace: *namespace}
	reg, err := openRegistry(ctx, *registryURL)
	if err != nil {
		return err
	}
	revisions, err := reg.ListRevisions(ctx, scope, *channel, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(revisions)
	}
	if len(revisions) == 0 {
		fmt.Printf("no revisions recorded for channel %s\n", *channel)
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "GEN\tBUNDLE\tDEPLOYED_AT\tBY\tCOMMIT\tNOTE")
	for _, revision := range revisions {
		commit := revision.SourceCommit
		if len(commit) > 8 {
			commit = commit[:8]
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
			revision.Generation,
			abbreviateBundleID(revision.BundleID),
			revision.DeployedAt.Format(time.RFC3339),
			revision.DeployedBy,
			commit,
			revision.Message,
		)
	}
	return w.Flush()
}

func diffCommand(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "namespace", "registry", "invoke-timeout")
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	namespace := fs.String("namespace", cfg.Namespace, "namespace for registry refs")
	registryURL := fs.String("registry", cfg.Registry, "registry URL")
	runTests := fs.Bool("tests", false, "cross-run each side's embedded tests against the other side")
	invokeTimeout := fs.String("invoke-timeout", "", "invocation timeout for --tests")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: switchboard diff <REF-A> <REF-B> [--tests]")
	}
	scope := registry.Scope{Namespace: *namespace}
	bundleA, err := resolveBundleRef(ctx, fs.Arg(0), scope, *registryURL)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", fs.Arg(0), err)
	}
	bundleB, err := resolveBundleRef(ctx, fs.Arg(1), scope, *registryURL)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", fs.Arg(1), err)
	}

	if bundleA.ID == bundleB.ID {
		fmt.Printf("identical: both refs resolve to bundle %s\n", abbreviateBundleID(bundleA.ID))
		return nil
	}
	fmt.Printf("A: %s (%s)\n", abbreviateBundleID(bundleA.ID), fs.Arg(0))
	fmt.Printf("B: %s (%s)\n", abbreviateBundleID(bundleB.ID), fs.Arg(1))
	diffArtifacts(bundleA, bundleB)

	if !*runTests {
		return nil
	}
	runtimeA, cleanupA, err := loadRuntimeForBundle(ctx, bundleA, *invokeTimeout)
	if err != nil {
		return err
	}
	defer cleanupA()
	runtimeB, cleanupB, err := loadRuntimeForBundle(ctx, bundleB, *invokeTimeout)
	if err != nil {
		return err
	}
	defer cleanupB()
	failed := false
	failed = crossRunTests(ctx, "A's tests against B", bundleA.Tests, runtimeB) || failed
	failed = crossRunTests(ctx, "B's tests against A", bundleB.Tests, runtimeA) || failed
	if failed {
		return errors.New("cross-run test failures")
	}
	return nil
}

func diffArtifacts(a, b bundle.Bundle) {
	names := map[string]bool{}
	for name := range a.Descriptor.Artifacts {
		names[name] = true
	}
	for name := range b.Descriptor.Artifacts {
		names[name] = true
	}
	if len(names) == 0 {
		fmt.Println("module.wasm differs (no descriptors to compare further)")
		return
	}
	for name := range names {
		refA, inA := a.Descriptor.Artifacts[name]
		refB, inB := b.Descriptor.Artifacts[name]
		switch {
		case !inA:
			fmt.Printf("  %s: only in B\n", name)
		case !inB:
			fmt.Printf("  %s: only in A\n", name)
		case refA.Digest != refB.Digest:
			fmt.Printf("  %s: differs (%d bytes -> %d bytes)\n", name, refA.Size, refB.Size)
		default:
			fmt.Printf("  %s: identical\n", name)
		}
	}
	if a.Descriptor.ABI != b.Descriptor.ABI && a.Descriptor.ABI != "" {
		fmt.Printf("  abi: %s -> %s\n", a.Descriptor.ABI, b.Descriptor.ABI)
	}
}

func crossRunTests(ctx context.Context, label string, tests []byte, runtime ruletest.Invoker) bool {
	if len(tests) == 0 {
		fmt.Printf("%s: no embedded tests\n", label)
		return false
	}
	suite, err := ruletest.ParseSuite(tests)
	if err != nil {
		fmt.Printf("%s: invalid suite: %v\n", label, err)
		return true
	}
	report := suite.Run(ctx, runtime)
	fmt.Printf("%s: %s\n", label, report.Summary())
	if !report.OK() {
		report.Format(os.Stdout, false)
		return true
	}
	return false
}

func promoteCommand(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "from", "to", "namespace", "registry", "message")
	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	from := fs.String("from", "", "source channel")
	to := fs.String("to", "", "target channel")
	namespace := fs.String("namespace", cfg.Namespace, "namespace")
	registryURL := fs.String("registry", cfg.Registry, "registry URL")
	message := fs.String("message", "", "revision message")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" || *to == "" {
		return errors.New("usage: switchboard promote --from staging --to prod")
	}
	scope := registry.Scope{Namespace: *namespace}
	reg, err := openRegistry(ctx, *registryURL)
	if err != nil {
		return err
	}
	pointer, err := reg.GetChannel(ctx, scope, *from)
	if err != nil {
		return err
	}
	b, err := reg.GetBundle(ctx, scope, pointer.BundleID)
	if err != nil {
		return err
	}
	note := *message
	if note == "" {
		note = fmt.Sprintf("promote from %s (generation %d)", *from, pointer.Generation)
	}
	revision, err := appendRevision(ctx, reg, scope, *to, b, revisionInfo{Message: note})
	if err != nil {
		return err
	}
	fmt.Printf("promoted bundle %s from %s to %s (generation %d)\n", abbreviateBundleID(b.ID), *from, *to, revision.Generation)
	return nil
}

func rollbackCommand(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "channel", "namespace", "registry", "to", "to-generation", "message")
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	channel := fs.String("channel", cfg.Channel, "channel name")
	namespace := fs.String("namespace", cfg.Namespace, "namespace")
	registryURL := fs.String("registry", cfg.Registry, "registry URL")
	toBundle := fs.String("to", "", "target bundle id")
	toGeneration := fs.Uint64("to-generation", 0, "target revision generation")
	message := fs.String("message", "", "revision message")
	if err := fs.Parse(args); err != nil {
		return err
	}
	scope := registry.Scope{Namespace: *namespace}
	reg, err := openRegistry(ctx, *registryURL)
	if err != nil {
		return err
	}
	pointer, err := reg.GetChannel(ctx, scope, *channel)
	if err != nil {
		return err
	}

	targetBundleID := ""
	note := *message
	switch {
	case *toBundle != "":
		targetBundleID, err = resolveBundleIDPrefix(ctx, reg, scope, *toBundle)
		if err != nil {
			return err
		}
		if note == "" {
			note = fmt.Sprintf("rollback to bundle %s", abbreviateBundleID(targetBundleID))
		}
	case *toGeneration > 0:
		revision, err := reg.GetRevision(ctx, scope, *channel, *toGeneration)
		if err != nil {
			return err
		}
		targetBundleID = revision.BundleID
		if note == "" {
			note = fmt.Sprintf("rollback to generation %d", *toGeneration)
		}
	default:
		// Default: the most recent revision whose bundle differs from what
		// is currently live.
		revisions, err := reg.ListRevisions(ctx, scope, *channel, 0)
		if err != nil {
			return err
		}
		for _, revision := range revisions {
			if revision.BundleID != pointer.BundleID {
				targetBundleID = revision.BundleID
				if note == "" {
					note = fmt.Sprintf("rollback to generation %d", revision.Generation)
				}
				break
			}
		}
		if targetBundleID == "" {
			return errors.New("no earlier revision with a different bundle to roll back to")
		}
	}
	if targetBundleID == pointer.BundleID {
		return fmt.Errorf("channel %s already points at bundle %s", *channel, abbreviateBundleID(targetBundleID))
	}
	b, err := reg.GetBundle(ctx, scope, targetBundleID)
	if err != nil {
		return fmt.Errorf("rollback target bundle is not fully present in the registry: %w", err)
	}
	revision, err := appendRevision(ctx, reg, scope, *channel, b, revisionInfo{Message: note})
	if err != nil {
		return err
	}
	fmt.Printf("rolled back channel %s to bundle %s (generation %d)\n", *channel, abbreviateBundleID(b.ID), revision.Generation)
	return nil
}
