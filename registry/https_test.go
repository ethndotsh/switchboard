package registry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethndotsh/switchboard/internal/bundle"
)

// populateFileRegistry writes a bundle, channel pointer, and revisions 1..3
// into a fresh registry directory and returns the directory, the bundle, and
// the channel pointer.
func populateFileRegistry(t *testing.T) (string, bundle.Bundle, bundle.ChannelPointer) {
	t.Helper()
	ctx := context.Background()
	reg, root := newFileRegistry(t)

	b := makeTestBundle(t, "http-rules", []byte("http module bytes"), []byte("cases: []\n"))
	if err := reg.PutBundle(ctx, Scope{}, b); err != nil {
		t.Fatalf("PutBundle: %v", err)
	}
	for _, generation := range []uint64{1, 2, 3} {
		if err := reg.PutRevision(ctx, Scope{}, testRevision("prod", generation)); err != nil {
			t.Fatalf("PutRevision(%d): %v", generation, err)
		}
	}
	pointer := bundle.ChannelPointer{
		Channel:    "prod",
		BundleID:   b.ID,
		Checksum:   b.Checksum,
		Generation: 3,
		CreatedAt:  time.Now().UTC().Truncate(time.Second),
	}
	if err := reg.PutChannel(ctx, Scope{}, pointer); err != nil {
		t.Fatalf("PutChannel: %v", err)
	}
	return root, b, pointer
}

func newHTTPRegistry(t *testing.T, handler http.Handler) *HTTPRegistry {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	reg, err := NewHTTP(server.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	return reg
}

func TestHTTPReadOnlyGets(t *testing.T) {
	ctx := context.Background()
	root, b, pointer := populateFileRegistry(t)
	reg := newHTTPRegistry(t, http.FileServer(http.Dir(root)))

	gotPointer, err := reg.GetChannel(ctx, Scope{}, "prod")
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if gotPointer.BundleID != pointer.BundleID || gotPointer.Generation != pointer.Generation {
		t.Errorf("pointer = %+v, want %+v", gotPointer, pointer)
	}

	gotBundle, err := reg.GetBundle(ctx, Scope{}, b.ID)
	if err != nil {
		t.Fatalf("GetBundle: %v", err)
	}
	if !bytes.Equal(gotBundle.Module, b.Module) {
		t.Errorf("Module mismatch")
	}
	if !bytes.Equal(gotBundle.Tests, b.Tests) {
		t.Errorf("Tests = %q, want %q", gotBundle.Tests, b.Tests)
	}
	if gotBundle.Manifest != b.Manifest {
		t.Errorf("Manifest = %+v, want %+v", gotBundle.Manifest, b.Manifest)
	}

	has, err := reg.HasBundle(ctx, Scope{}, b.ID)
	if err != nil {
		t.Fatalf("HasBundle: %v", err)
	}
	if !has {
		t.Errorf("HasBundle = false, want true")
	}
	has, err = reg.HasBundle(ctx, Scope{}, "sha256-missing")
	if err != nil {
		t.Fatalf("HasBundle missing: %v", err)
	}
	if has {
		t.Errorf("HasBundle(missing) = true, want false")
	}

	revision, err := reg.GetRevision(ctx, Scope{}, "prod", 2)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if revision.Generation != 2 || revision.Channel != "prod" {
		t.Errorf("revision = %+v", revision)
	}
	if _, err := reg.GetRevision(ctx, Scope{}, "prod", 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRevision(9) = %v, want ErrNotFound", err)
	}
}

func TestHTTPPutsAreReadOnly(t *testing.T) {
	ctx := context.Background()
	reg := newHTTPRegistry(t, http.NotFoundHandler())

	if err := reg.PutBundle(ctx, Scope{}, bundle.Bundle{}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("PutBundle = %v, want ErrReadOnly", err)
	}
	if err := reg.PutChannel(ctx, Scope{}, bundle.ChannelPointer{}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("PutChannel = %v, want ErrReadOnly", err)
	}
	if err := reg.PutRevision(ctx, Scope{}, bundle.Revision{}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("PutRevision = %v, want ErrReadOnly", err)
	}
}

func TestHTTPETagCaching(t *testing.T) {
	ctx := context.Background()
	root, _, pointer := populateFileRegistry(t)

	var fullResponses atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rel := strings.TrimPrefix(req.URL.Path, "/")
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			http.NotFound(w, req)
			return
		}
		etag := fmt.Sprintf("%q", bundle.ArtifactDigest(data))
		w.Header().Set("ETag", etag)
		if req.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		fullResponses.Add(1)
		_, _ = w.Write(data)
	})
	reg := newHTTPRegistry(t, handler)

	first, err := reg.GetChannel(ctx, Scope{}, "prod")
	if err != nil {
		t.Fatalf("first GetChannel: %v", err)
	}
	if got := fullResponses.Load(); got != 1 {
		t.Fatalf("full responses after first get = %d, want 1", got)
	}

	second, err := reg.GetChannel(ctx, Scope{}, "prod")
	if err != nil {
		t.Fatalf("second GetChannel: %v", err)
	}
	if got := fullResponses.Load(); got != 1 {
		t.Fatalf("full responses after second get = %d, want 1 (expected a 304)", got)
	}
	if second.BundleID != pointer.BundleID || second.Generation != pointer.Generation ||
		second.Checksum != pointer.Checksum || second.Channel != pointer.Channel {
		t.Errorf("cached pointer = %+v, want %+v", second, pointer)
	}
	if first.BundleID != second.BundleID {
		t.Errorf("first and second reads disagree: %+v vs %+v", first, second)
	}
}

func TestHTTPGetChannelNotFound(t *testing.T) {
	ctx := context.Background()
	reg := newHTTPRegistry(t, http.FileServer(http.Dir(t.TempDir())))
	if _, err := reg.GetChannel(ctx, Scope{}, "prod"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetChannel = %v, want ErrNotFound", err)
	}
}

func TestHTTPListRevisions(t *testing.T) {
	ctx := context.Background()
	root, _, _ := populateFileRegistry(t)
	reg := newHTTPRegistry(t, http.FileServer(http.Dir(root)))

	revisions, err := reg.ListRevisions(ctx, Scope{}, "prod", 0)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revisions) != 3 {
		t.Fatalf("ListRevisions len = %d, want 3", len(revisions))
	}
	for i, want := range []uint64{3, 2, 1} {
		if revisions[i].Generation != want {
			t.Errorf("revisions[%d].Generation = %d, want %d", i, revisions[i].Generation, want)
		}
	}

	limited, err := reg.ListRevisions(ctx, Scope{}, "prod", 2)
	if err != nil {
		t.Fatalf("ListRevisions limit: %v", err)
	}
	if len(limited) != 2 || limited[0].Generation != 3 || limited[1].Generation != 2 {
		t.Errorf("limited = %+v, want generations [3 2]", limited)
	}

	// The walk starts at the channel pointer; a missing channel means no
	// revisions can be listed.
	if _, err := reg.ListRevisions(ctx, Scope{}, "unknown", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListRevisions unknown channel = %v, want ErrNotFound", err)
	}
}
