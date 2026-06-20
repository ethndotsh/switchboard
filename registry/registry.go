package registry

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/ethndotsh/switchboard/internal/bundle"
)

var safePathSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Scope struct {
	Namespace string
}

type Registry interface {
	GetChannel(ctx context.Context, scope Scope, channel string) (bundle.ChannelPointer, error)
	GetBundle(ctx context.Context, scope Scope, id string) (bundle.Bundle, error)
	PutBundle(ctx context.Context, scope Scope, b bundle.Bundle) error
	PutChannel(ctx context.Context, scope Scope, pointer bundle.ChannelPointer) error
}

func ValidateNamespace(namespace string) error {
	if namespace == "" {
		return nil
	}
	if strings.HasPrefix(namespace, "/") || strings.HasSuffix(namespace, "/") {
		return fmt.Errorf("namespace %q must not start or end with /", namespace)
	}
	for _, segment := range strings.Split(namespace, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("namespace %q contains invalid segment %q", namespace, segment)
		}
		if !safePathSegment.MatchString(segment) {
			return fmt.Errorf("namespace %q contains unsafe segment %q", namespace, segment)
		}
	}
	return nil
}
