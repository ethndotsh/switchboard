package registry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

var s3EnvVars = []string{
	"SWITCHBOARD_REGISTRY",
	"SWITCHBOARD_S3_ENDPOINT",
	"SWITCHBOARD_S3_ACCESS_KEY",
	"SWITCHBOARD_S3_SECRET_KEY",
	"SWITCHBOARD_S3_BUCKET",
	"SWITCHBOARD_S3_PREFIX",
	"SWITCHBOARD_S3_INSECURE",
}

func TestOpen(t *testing.T) {
	// Isolate from any registry configuration in the ambient environment.
	for _, key := range s3EnvVars {
		t.Setenv(key, "")
	}

	// Run relative-path cases from a temp working directory so NewFile's
	// MkdirAll does not litter the package directory.
	tmp := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Errorf("restore wd: %v", err)
		}
	})

	s3Env := map[string]string{
		"SWITCHBOARD_S3_ENDPOINT":   "localhost:9",
		"SWITCHBOARD_S3_ACCESS_KEY": "test-access",
		"SWITCHBOARD_S3_SECRET_KEY": "test-secret",
	}

	cases := []struct {
		name    string
		url     string
		env     map[string]string
		want    string // "file", "http", "s3"
		wantErr bool
	}{
		{name: "file URL", url: "file://" + filepath.Join(tmp, "file-url"), want: "file"},
		{name: "bare relative path", url: "./bare-relative", want: "file"},
		{name: "bare absolute path", url: filepath.Join(tmp, "bare-abs"), want: "file"},
		{name: "https URL", url: "https://example.com/base", want: "http"},
		{name: "s3 URL with env credentials", url: "s3://bucket/prefix", env: s3Env, want: "s3"},
		{name: "s3 URL without env credentials", url: "s3://bucket/prefix", wantErr: true},
		{name: "unsupported scheme", url: "ftp://x", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			reg, err := Open(context.Background(), tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Open(%q) = %T, want error", tc.url, reg)
				}
				return
			}
			if err != nil {
				t.Fatalf("Open(%q): %v", tc.url, err)
			}
			var got string
			switch reg.(type) {
			case *FileRegistry:
				got = "file"
			case *HTTPRegistry:
				got = "http"
			case *S3Registry:
				got = "s3"
			default:
				got = "unknown"
			}
			if got != tc.want {
				t.Fatalf("Open(%q) = %T, want %s registry", tc.url, reg, tc.want)
			}
		})
	}
}
