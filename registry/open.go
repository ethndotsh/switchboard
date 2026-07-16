package registry

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Open builds a registry from an s3://, file://, or https:// URL (or a bare
// local path). An empty URL falls back to SWITCHBOARD_REGISTRY, then the
// legacy SWITCHBOARD_S3_* environment configuration.
func Open(ctx context.Context, rawURL string) (Registry, error) {
	if rawURL == "" {
		rawURL = os.Getenv("SWITCHBOARD_REGISTRY")
	}
	if rawURL == "" {
		return NewS3(ctx, S3ConfigFromEnv())
	}
	switch {
	case strings.HasPrefix(rawURL, "s3://"):
		cfg, err := ParseS3URL(rawURL)
		if err != nil {
			return nil, err
		}
		return NewS3(ctx, cfg)
	case strings.HasPrefix(rawURL, "file://"):
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, err
		}
		root := u.Path
		if u.Host != "" {
			// Treat the host as a path segment so file://./registry works.
			root = u.Host + u.Path
		}
		if u.Opaque != "" {
			root = u.Opaque
		}
		return NewFile(root)
	case strings.HasPrefix(rawURL, "http://"), strings.HasPrefix(rawURL, "https://"):
		return NewHTTP(rawURL)
	case strings.HasPrefix(rawURL, "/"), strings.HasPrefix(rawURL, "./"), strings.HasPrefix(rawURL, "../"):
		return NewFile(rawURL)
	default:
		return nil, fmt.Errorf("unsupported registry URL %q (expected s3://, file://, https://, or a local path)", rawURL)
	}
}
