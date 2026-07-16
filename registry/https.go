package registry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/ethndotsh/switchboard/internal/bundle"
)

// HTTPRegistry reads the standard registry layout from any static HTTP(S)
// origin; it is read-only.
type HTTPRegistry struct {
	base   string
	client *http.Client

	mu    sync.Mutex
	cache map[string]httpCacheEntry
}

type httpCacheEntry struct {
	etag string
	body []byte
}

func NewHTTP(base string) (*HTTPRegistry, error) {
	trimmed := strings.TrimRight(base, "/")
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return nil, fmt.Errorf("http registry URL must start with http:// or https://")
	}
	return &HTTPRegistry{
		base:   trimmed,
		client: http.DefaultClient,
		cache:  map[string]httpCacheEntry{},
	}, nil
}

func (r *HTTPRegistry) key(scope Scope, parts ...string) string {
	all := make([]string, 0, len(parts)+2)
	if scope.Namespace != "" {
		all = append(all, "namespaces", scope.Namespace)
	}
	all = append(all, parts...)
	return strings.Join(all, "/")
}

// get revalidates with If-None-Match; 304s are served from the cache.
func (r *HTTPRegistry) get(ctx context.Context, key string) ([]byte, bool, error) {
	r.mu.Lock()
	cached, hasCached := r.cache[key]
	r.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base+"/"+key, nil)
	if err != nil {
		return nil, false, err
	}
	if hasCached && cached.etag != "" {
		req.Header.Set("If-None-Match", cached.etag)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return cached.body, true, nil
	case http.StatusNotFound:
		return nil, false, nil
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, false, err
		}
		if etag := resp.Header.Get("ETag"); etag != "" {
			r.mu.Lock()
			r.cache[key] = httpCacheEntry{etag: etag, body: body}
			r.mu.Unlock()
		}
		return body, true, nil
	default:
		return nil, false, fmt.Errorf("GET %s/%s: unexpected status %s", r.base, key, resp.Status)
	}
}

func (r *HTTPRegistry) GetChannel(ctx context.Context, scope Scope, channel string) (bundle.ChannelPointer, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.ChannelPointer{}, err
	}
	data, ok, err := r.get(ctx, r.key(scope, "channels", channel+".json"))
	if err != nil {
		return bundle.ChannelPointer{}, err
	}
	if !ok {
		return bundle.ChannelPointer{}, fmt.Errorf("channel %s: %w", channel, ErrNotFound)
	}
	return bundle.ParseChannelPointer(data)
}

func (r *HTTPRegistry) GetBundle(ctx context.Context, scope Scope, id string) (bundle.Bundle, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.Bundle{}, err
	}
	return assembleBundle(id, func(name string) ([]byte, bool, error) {
		return r.get(ctx, r.key(scope, "bundles", id, name))
	})
}

func (r *HTTPRegistry) HasBundle(ctx context.Context, scope Scope, id string) (bool, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return false, err
	}
	if _, ok, err := r.get(ctx, r.key(scope, "bundles", id, "descriptor.json")); err != nil || ok {
		return ok, err
	}
	_, ok, err := r.get(ctx, r.key(scope, "bundles", id, "checksum.txt"))
	return ok, err
}

func (r *HTTPRegistry) PutBundle(ctx context.Context, scope Scope, b bundle.Bundle) error {
	return fmt.Errorf("put bundle: %w", ErrReadOnly)
}

func (r *HTTPRegistry) PutChannel(ctx context.Context, scope Scope, pointer bundle.ChannelPointer) error {
	return fmt.Errorf("put channel: %w", ErrReadOnly)
}

func (r *HTTPRegistry) GetRevision(ctx context.Context, scope Scope, channel string, generation uint64) (bundle.Revision, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.Revision{}, err
	}
	data, ok, err := r.get(ctx, r.key(scope, "revisions", channel, revisionFileName(generation)))
	if err != nil {
		return bundle.Revision{}, err
	}
	if !ok {
		return bundle.Revision{}, fmt.Errorf("revision %d: %w", generation, ErrNotFound)
	}
	return bundle.ParseRevision(data)
}

func (r *HTTPRegistry) PutRevision(ctx context.Context, scope Scope, rev bundle.Revision) error {
	return fmt.Errorf("put revision: %w", ErrReadOnly)
}

func (r *HTTPRegistry) ListRevisions(ctx context.Context, scope Scope, channel string, limit int) ([]bundle.Revision, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return nil, err
	}
	return listRevisionsByWalk(ctx, r, scope, channel, limit)
}
