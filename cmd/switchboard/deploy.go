package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/ethndotsh/switchboard/registry"
)

func deploy(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "namespace", "channel", "registry", "message")
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	namespace := fs.String("namespace", cfg.Namespace, "namespace")
	channel := fs.String("channel", cfg.Channel, "channel name")
	registryURL := fs.String("registry", cfg.Registry, "registry URL, e.g. s3://bucket/prefix or file://./registry")
	message := fs.String("message", "", "revision message")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: switchboard deploy [DIST] [--namespace customer-a] --channel prod [--registry s3://bucket/prefix]")
	}
	scope := registry.Scope{Namespace: *namespace}
	if err := registry.ValidateNamespace(scope.Namespace); err != nil {
		return err
	}
	reg, err := openRegistry(ctx, *registryURL)
	if err != nil {
		return err
	}
	dist := cfg.Dist
	if fs.NArg() == 1 {
		dist = fs.Arg(0)
	}
	b, err := readBundleDir(dist)
	if err != nil {
		return err
	}

	exists, err := reg.HasBundle(ctx, scope, b.ID)
	if err != nil {
		return err
	}
	if exists {
		fmt.Printf("bundle %s already in registry, skipping upload\n", abbreviateBundleID(b.ID))
	} else {
		if err := reg.PutBundle(ctx, scope, b); err != nil {
			return err
		}
	}

	revision, err := appendRevision(ctx, reg, scope, *channel, b, revisionInfo{Message: *message})
	if err != nil {
		return err
	}
	if scope.Namespace != "" {
		fmt.Printf("deployed bundle %s to namespace %s channel %s (generation %d)\n", abbreviateBundleID(b.ID), scope.Namespace, *channel, revision.Generation)
		return nil
	}
	fmt.Printf("deployed bundle %s to channel %s (generation %d)\n", abbreviateBundleID(b.ID), *channel, revision.Generation)
	return nil
}

func inspect(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "namespace", "channel", "registry")
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	namespace := fs.String("namespace", cfg.Namespace, "namespace")
	channel := fs.String("channel", cfg.Channel, "channel name")
	registryURL := fs.String("registry", cfg.Registry, "registry URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	reg, err := openRegistry(ctx, *registryURL)
	if err != nil {
		return err
	}
	scope := registry.Scope{Namespace: *namespace}
	if err := registry.ValidateNamespace(scope.Namespace); err != nil {
		return err
	}
	pointer, err := reg.GetChannel(ctx, scope, *channel)
	if err != nil {
		return err
	}
	return printJSON(pointer)
}

// openRegistry pings S3 registries so misconfiguration surfaces immediately.
func openRegistry(ctx context.Context, rawURL string) (registry.Registry, error) {
	reg, err := registry.Open(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	if s3reg, ok := reg.(*registry.S3Registry); ok {
		if err := s3reg.Ping(ctx); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

type revisionInfo struct {
	Message string
}

// appendRevision claims the next generation, retrying on concurrent-deploy
// conflicts, then repoints the channel.
func appendRevision(ctx context.Context, reg registry.Registry, scope registry.Scope, channel string, b bundle.Bundle, info revisionInfo) (bundle.Revision, error) {
	descriptorDigest := ""
	if len(b.DescriptorRaw) > 0 {
		descriptorDigest = bundle.DescriptorDigest(b.DescriptorRaw)
	}
	for attempt := 0; attempt < 5; attempt++ {
		var previousGeneration uint64
		pointer, err := reg.GetChannel(ctx, scope, channel)
		switch {
		case err == nil:
			previousGeneration = pointer.Generation
		case errors.Is(err, registry.ErrNotFound):
		default:
			return bundle.Revision{}, err
		}
		revision := bundle.Revision{
			Schema:             bundle.RevisionSchema,
			Namespace:          scope.Namespace,
			Channel:            channel,
			Generation:         previousGeneration + 1,
			BundleID:           b.ID,
			DescriptorDigest:   descriptorDigest,
			PreviousGeneration: previousGeneration,
			DeployedAt:         time.Now().UTC(),
			DeployedBy:         deployerIdentity(),
			SourceCommit:       b.Descriptor.Provenance.SourceCommit,
			CIRun:              b.Descriptor.Provenance.CIRun,
			Message:            info.Message,
		}
		if err := reg.PutRevision(ctx, scope, revision); err != nil {
			if errors.Is(err, registry.ErrRevisionExists) {
				continue
			}
			return bundle.Revision{}, err
		}
		newPointer := bundle.ChannelPointer{
			Namespace:        scope.Namespace,
			Channel:          channel,
			BundleID:         b.ID,
			Checksum:         b.Checksum,
			Generation:       revision.Generation,
			DescriptorDigest: descriptorDigest,
			CreatedAt:        revision.DeployedAt,
		}
		if err := reg.PutChannel(ctx, scope, newPointer); err != nil {
			return bundle.Revision{}, err
		}
		// The pointer write is last-writer-wins; detect a lost race.
		if latest, err := reg.GetChannel(ctx, scope, channel); err == nil && latest.Generation > revision.Generation {
			fmt.Fprintf(os.Stderr, "warning: channel %s was updated concurrently (now at generation %d)\n", channel, latest.Generation)
		}
		return revision, nil
	}
	return bundle.Revision{}, fmt.Errorf("could not claim a revision generation for channel %s after 5 attempts", channel)
}

func deployerIdentity() string {
	if deployer := os.Getenv("SWITCHBOARD_DEPLOYER"); deployer != "" {
		return deployer
	}
	if actor := os.Getenv("GITHUB_ACTOR"); actor != "" {
		return actor
	}
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	return "unknown"
}
