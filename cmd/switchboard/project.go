package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethndotsh/switchboard/registry"
	"gopkg.in/yaml.v3"
)

type projectConfig struct {
	Name         string `yaml:"name"`
	Rule         string `yaml:"rule"`
	Dist         string `yaml:"dist"`
	Tests        string `yaml:"tests,omitempty"`
	Data         string `yaml:"data,omitempty"`
	MaxDataBytes string `yaml:"max_data_bytes,omitempty"`
	Namespace    string `yaml:"namespace,omitempty"`
	Channel      string `yaml:"channel"`
	Registry     string `yaml:"registry,omitempty"`
}

func defaultConfig() projectConfig {
	return projectConfig{
		Name:    filepath.Base(mustGetwd()),
		Rule:    "./rules/basic",
		Dist:    "./dist",
		Channel: "prod",
	}
}

func initProject(args []string) error {
	args = normalizeFlagArgs(args, "name", "module", "rule", "dist", "namespace", "channel", "registry")
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	name := fs.String("name", filepath.Base(mustGetwd()), "project/rule name")
	module := fs.String("module", "", "Go module path for the rule project")
	rule := fs.String("rule", "./rules/basic", "rule package path")
	dist := fs.String("dist", "./dist", "bundle output directory")
	namespace := fs.String("namespace", "", "default namespace")
	channel := fs.String("channel", "prod", "default deploy channel")
	registryURL := fs.String("registry", "", "registry URL, e.g. s3://bucket/prefix or file://./registry")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := projectConfig{
		Name:      *name,
		Rule:      *rule,
		Dist:      *dist,
		Namespace: *namespace,
		Channel:   *channel,
		Registry:  *registryURL,
	}
	if err := registry.ValidateNamespace(cfg.Namespace); err != nil {
		return err
	}
	if err := writeConfig(cfg); err != nil {
		return err
	}
	if *module == "" {
		*module = sanitizeModuleName(*name)
	}
	if err := writeRuleGoMod(*module); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Rule, 0o755); err != nil {
		return err
	}
	rulePath := filepath.Join(cfg.Rule, "rule.go")
	if _, err := os.Stat(rulePath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(rulePath, []byte(exampleRuleSource(packageNameFromDir(cfg.Rule))), 0o644); err != nil {
			return err
		}
	}
	testsPath := filepath.Join(cfg.Rule, "tests.yaml")
	if _, err := os.Stat(testsPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(testsPath, []byte(exampleTestsSource()), 0o644); err != nil {
			return err
		}
	}
	fmt.Println("created switchboard.yaml")
	fmt.Println("created go.mod")
	fmt.Printf("rule: %s\n", cfg.Rule)
	fmt.Printf("tests: %s\n", testsPath)
	fmt.Println("next: switchboard build && switchboard test")
	return nil
}

func loadConfigOrDefault() projectConfig {
	cfg := defaultConfig()
	data, err := os.ReadFile("switchboard.yaml")
	if err != nil {
		return cfg
	}
	data = expandEnv(data)
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return defaultConfig()
	}
	if cfg.Name == "" {
		cfg.Name = defaultConfig().Name
	}
	if cfg.Rule == "" {
		cfg.Rule = defaultConfig().Rule
	}
	if cfg.Dist == "" {
		cfg.Dist = defaultConfig().Dist
	}
	if cfg.Namespace != "" {
		if err := registry.ValidateNamespace(cfg.Namespace); err != nil {
			return defaultConfig()
		}
	}
	if cfg.Channel == "" {
		cfg.Channel = defaultConfig().Channel
	}
	return cfg
}

func expandEnv(data []byte) []byte {
	expanded := os.Expand(string(data), func(key string) string {
		if name, fallback, ok := strings.Cut(key, ":-"); ok {
			if value, found := os.LookupEnv(name); found && value != "" {
				return value
			}
			return fallback
		}
		if name, fallback, ok := strings.Cut(key, "-"); ok {
			if value, found := os.LookupEnv(name); found {
				return value
			}
			return fallback
		}
		return os.Getenv(key)
	})
	return []byte(expanded)
}

func writeConfig(cfg projectConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if _, err := os.Stat("switchboard.yaml"); err == nil {
		return errors.New("switchboard.yaml already exists")
	}
	return os.WriteFile("switchboard.yaml", data, 0o644)
}

func writeRuleGoMod(modulePath string) error {
	if _, err := os.Stat("go.mod"); err == nil {
		return nil
	}
	data := fmt.Sprintf(`module %s

go 1.23
`, modulePath)
	return os.WriteFile("go.mod", []byte(data), 0o644)
}

func packageNameFromDir(dir string) string {
	name := filepath.Base(filepath.Clean(dir))
	name = strings.TrimSpace(strings.ToLower(name))
	var b strings.Builder
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		if i == 0 && r >= '0' && r <= '9' {
			b.WriteByte('_')
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	cleaned := strings.Trim(b.String(), "_")
	if cleaned == "" || cleaned == "main" {
		return "rules"
	}
	return cleaned
}

func sanitizeModuleName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '/' || r == '_' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	cleaned := strings.Trim(b.String(), "-")
	if cleaned == "" {
		return "switchboard-rules"
	}
	return cleaned
}

func exampleRuleSource(packageName string) string {
	return fmt.Sprintf(`package %s

import "github.com/ethndotsh/switchboard/sdk"

func Handle(req sdk.Request) sdk.Action {
	if req.Path() == "/blocked" {
		return sdk.Deny(403).WithReason("blocked-path")
	}

	return sdk.Next().SetRequestHeader("x-powered-by", "switchboard")
}
`, packageName)
}

func exampleTestsSource() string {
	return `schema: switchboard.tests/v1
cases:
  - name: blocks the blocked path
    request:
      method: GET
      path: /blocked
    expect:
      action: deny
      status: 403
      reason: blocked-path

  - name: passes other requests through
    request:
      method: GET
      path: /
    expect:
      action: next
      request_headers:
        x-powered-by: switchboard
`
}
