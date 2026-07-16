package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Prefix    string
	Insecure  bool
}

type S3Registry struct {
	client *minio.Client
	bucket string
	prefix string
}

func S3ConfigFromEnv() S3Config {
	return S3Config{
		Endpoint:  os.Getenv("SWITCHBOARD_S3_ENDPOINT"),
		AccessKey: os.Getenv("SWITCHBOARD_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("SWITCHBOARD_S3_SECRET_KEY"),
		Bucket:    os.Getenv("SWITCHBOARD_S3_BUCKET"),
		Prefix:    os.Getenv("SWITCHBOARD_S3_PREFIX"),
		Insecure:  strings.EqualFold(os.Getenv("SWITCHBOARD_S3_INSECURE"), "true"),
	}
}

func ParseS3URL(raw string) (S3Config, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return S3Config{}, err
	}
	if u.Scheme != "s3" {
		return S3Config{}, fmt.Errorf("registry URL must use s3://")
	}
	cfg := S3ConfigFromEnv()
	cfg.Bucket = u.Host
	cfg.Prefix = strings.TrimPrefix(u.Path, "/")
	return cfg, nil
}

// NewS3 constructs the client without touching the network, so a proxy can
// come up while the object store is unreachable. Use Ping for CLI preflight.
func NewS3(ctx context.Context, cfg S3Config) (*S3Registry, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("SWITCHBOARD_S3_ENDPOINT is required")
	}
	if cfg.AccessKey == "" {
		return nil, fmt.Errorf("SWITCHBOARD_S3_ACCESS_KEY is required")
	}
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("SWITCHBOARD_S3_SECRET_KEY is required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("SWITCHBOARD_S3_BUCKET is required")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: !cfg.Insecure,
	})
	if err != nil {
		return nil, err
	}
	return &S3Registry{client: client, bucket: cfg.Bucket, prefix: strings.Trim(cfg.Prefix, "/")}, nil
}

func (r *S3Registry) Ping(ctx context.Context) error {
	exists, err := r.client.BucketExists(ctx, r.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist", r.bucket)
	}
	return nil
}

func (r *S3Registry) GetChannel(ctx context.Context, scope Scope, channel string) (bundle.ChannelPointer, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.ChannelPointer{}, err
	}
	data, ok, err := r.get(ctx, r.scopedKey(scope, "channels", channel+".json"))
	if err != nil {
		return bundle.ChannelPointer{}, err
	}
	if !ok {
		return bundle.ChannelPointer{}, fmt.Errorf("channel %s: %w", channel, ErrNotFound)
	}
	return bundle.ParseChannelPointer(data)
}

func (r *S3Registry) GetBundle(ctx context.Context, scope Scope, id string) (bundle.Bundle, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.Bundle{}, err
	}
	return assembleBundle(id, func(name string) ([]byte, bool, error) {
		return r.get(ctx, r.scopedKey(scope, "bundles", id, name))
	})
}

func (r *S3Registry) HasBundle(ctx context.Context, scope Scope, id string) (bool, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return false, err
	}
	for _, marker := range []string{"descriptor.json", "checksum.txt"} {
		_, err := r.client.StatObject(ctx, r.bucket, r.scopedKey(scope, "bundles", id, marker), minio.StatObjectOptions{})
		if err == nil {
			return true, nil
		}
		if !isNoSuchKey(err) {
			return false, err
		}
	}
	return false, nil
}

func (r *S3Registry) PutBundle(ctx context.Context, scope Scope, b bundle.Bundle) error {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return err
	}
	files, err := bundleFiles(b)
	if err != nil {
		return err
	}
	for _, name := range BundleFileNames {
		data, ok := files[name]
		if !ok {
			continue
		}
		if err := r.put(ctx, r.scopedKey(scope, "bundles", b.ID, name), data, contentTypeFor(name), minio.PutObjectOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func (r *S3Registry) PutChannel(ctx context.Context, scope Scope, pointer bundle.ChannelPointer) error {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return err
	}
	pointer.Namespace = scope.Namespace
	data, err := json.MarshalIndent(pointer, "", "  ")
	if err != nil {
		return err
	}
	return r.put(ctx, r.scopedKey(scope, "channels", pointer.Channel+".json"), data, "application/json", minio.PutObjectOptions{})
}

func (r *S3Registry) GetRevision(ctx context.Context, scope Scope, channel string, generation uint64) (bundle.Revision, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.Revision{}, err
	}
	data, ok, err := r.get(ctx, r.scopedKey(scope, "revisions", channel, revisionFileName(generation)))
	if err != nil {
		return bundle.Revision{}, err
	}
	if !ok {
		return bundle.Revision{}, fmt.Errorf("revision %d: %w", generation, ErrNotFound)
	}
	return bundle.ParseRevision(data)
}

// PutRevision creates the generation object with If-None-Match: * so two
// concurrent deployers cannot claim the same generation; stores without
// conditional writes fall back to stat-then-put with a small race window.
func (r *S3Registry) PutRevision(ctx context.Context, scope Scope, rev bundle.Revision) error {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rev, "", "  ")
	if err != nil {
		return err
	}
	key := r.scopedKey(scope, "revisions", rev.Channel, revisionFileName(rev.Generation))
	opts := minio.PutObjectOptions{ContentType: "application/json"}
	opts.SetMatchETagExcept("*")
	err = r.put(ctx, key, data, "application/json", opts)
	if err == nil {
		return nil
	}
	response := minio.ToErrorResponse(err)
	if response.StatusCode == 412 || response.Code == "PreconditionFailed" {
		return fmt.Errorf("generation %d: %w", rev.Generation, ErrRevisionExists)
	}
	if response.Code == "NotImplemented" {
		if _, statErr := r.client.StatObject(ctx, r.bucket, key, minio.StatObjectOptions{}); statErr == nil {
			return fmt.Errorf("generation %d: %w", rev.Generation, ErrRevisionExists)
		}
		return r.put(ctx, key, data, "application/json", minio.PutObjectOptions{ContentType: "application/json"})
	}
	return err
}

func (r *S3Registry) ListRevisions(ctx context.Context, scope Scope, channel string, limit int) ([]bundle.Revision, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return nil, err
	}
	prefix := r.scopedKey(scope, "revisions", channel) + "/"
	var keys []string
	for object := range r.client.ListObjects(ctx, r.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			return nil, object.Err
		}
		if strings.HasSuffix(object.Key, ".json") {
			keys = append(keys, object.Key)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	var revisions []bundle.Revision
	for _, key := range keys {
		if limit > 0 && len(revisions) >= limit {
			break
		}
		data, ok, err := r.get(ctx, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		revision, err := bundle.ParseRevision(data)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, nil
}

func (r *S3Registry) get(ctx context.Context, key string) ([]byte, bool, error) {
	obj, err := r.client.GetObject(ctx, r.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if isNoSuchKey(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		if isNoSuchKey(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

func (r *S3Registry) put(ctx context.Context, key string, data []byte, contentType string, opts minio.PutObjectOptions) error {
	if opts.ContentType == "" {
		opts.ContentType = contentType
	}
	_, err := r.client.PutObject(ctx, r.bucket, key, bytes.NewReader(data), int64(len(data)), opts)
	return err
}

func (r *S3Registry) key(parts ...string) string {
	all := parts
	if r.prefix != "" {
		all = append([]string{r.prefix}, parts...)
	}
	return path.Join(all...)
}

func (r *S3Registry) scopedKey(scope Scope, parts ...string) string {
	if scope.Namespace == "" {
		return r.key(parts...)
	}
	all := append([]string{"namespaces"}, strings.Split(scope.Namespace, "/")...)
	all = append(all, parts...)
	return r.key(all...)
}

func isNoSuchKey(err error) bool {
	var response minio.ErrorResponse
	if errors.As(err, &response) {
		return response.Code == "NoSuchKey" || response.StatusCode == 404
	}
	return false
}

func contentTypeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".wasm"):
		return "application/wasm"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	case strings.HasSuffix(name, ".yaml"), strings.HasSuffix(name, ".yml"):
		return "application/yaml"
	default:
		return "text/plain"
	}
}
