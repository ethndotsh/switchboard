package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
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
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("bucket %q does not exist", cfg.Bucket)
	}
	return &S3Registry{client: client, bucket: cfg.Bucket, prefix: strings.Trim(cfg.Prefix, "/")}, nil
}

func (r *S3Registry) GetChannel(ctx context.Context, scope Scope, channel string) (bundle.ChannelPointer, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.ChannelPointer{}, err
	}
	data, err := r.get(ctx, r.scopedKey(scope, "channels", channel+".json"))
	if err != nil {
		return bundle.ChannelPointer{}, err
	}
	return bundle.ParseChannelPointer(data)
}

func (r *S3Registry) GetBundle(ctx context.Context, scope Scope, id string) (bundle.Bundle, error) {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return bundle.Bundle{}, err
	}
	module, err := r.get(ctx, r.scopedKey(scope, "bundles", id, "module.wasm"))
	if err != nil {
		return bundle.Bundle{}, err
	}
	manifestData, err := r.get(ctx, r.scopedKey(scope, "bundles", id, "manifest.json"))
	if err != nil {
		return bundle.Bundle{}, err
	}
	checksumData, err := r.get(ctx, r.scopedKey(scope, "bundles", id, "checksum.txt"))
	if err != nil {
		return bundle.Bundle{}, err
	}
	manifest, err := bundle.ParseManifest(manifestData)
	if err != nil {
		return bundle.Bundle{}, err
	}
	checksum := strings.TrimSpace(string(checksumData))
	if err := bundle.VerifyModuleChecksum(module, checksum); err != nil {
		return bundle.Bundle{}, err
	}
	return bundle.Bundle{ID: id, Module: module, Manifest: manifest, Checksum: checksum}, nil
}

func (r *S3Registry) PutBundle(ctx context.Context, scope Scope, b bundle.Bundle) error {
	if err := ValidateNamespace(scope.Namespace); err != nil {
		return err
	}
	manifestData, err := json.MarshalIndent(b.Manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := bundle.VerifyModuleChecksum(b.Module, b.Checksum); err != nil {
		return err
	}
	if err := r.put(ctx, r.scopedKey(scope, "bundles", b.ID, "module.wasm"), b.Module, "application/wasm"); err != nil {
		return err
	}
	if err := r.put(ctx, r.scopedKey(scope, "bundles", b.ID, "manifest.json"), manifestData, "application/json"); err != nil {
		return err
	}
	return r.put(ctx, r.scopedKey(scope, "bundles", b.ID, "checksum.txt"), []byte(b.Checksum+"\n"), "text/plain")
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
	return r.put(ctx, r.scopedKey(scope, "channels", pointer.Channel+".json"), data, "application/json")
}

func (r *S3Registry) get(ctx context.Context, key string) ([]byte, error) {
	obj, err := r.client.GetObject(ctx, r.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(obj)
}

func (r *S3Registry) put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := r.client.PutObject(ctx, r.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
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
