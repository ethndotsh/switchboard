package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethndotsh/switchboard/internal/bundle"
	"github.com/ethndotsh/switchboard/registry"
	"gopkg.in/yaml.v3"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "init":
		return initProject(args[1:])
	case "build":
		return build(ctx, args[1:])
	case "dist":
		return build(ctx, args[1:])
	case "deploy":
		return deploy(ctx, args[1:])
	case "inspect":
		return inspect(ctx, args[1:])
	default:
		return usage()
	}
}

type projectConfig struct {
	Name      string `yaml:"name"`
	Rule      string `yaml:"rule"`
	Dist      string `yaml:"dist"`
	Namespace string `yaml:"namespace,omitempty"`
	Channel   string `yaml:"channel"`
	Registry  string `yaml:"registry,omitempty"`
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
	registryURL := fs.String("registry", "", "registry URL, e.g. s3://bucket/prefix")
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
	fmt.Println("created switchboard.yaml")
	fmt.Println("created go.mod")
	fmt.Printf("rule: %s\n", cfg.Rule)
	fmt.Println("next: switchboard build")
	return nil
}

func build(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "out", "name", "tinygo-opt", "tinygo-panic", "wasm-opt-level")
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	out := fs.String("out", cfg.Dist, "output directory")
	name := fs.String("name", cfg.Name, "bundle name")
	skipTidy := fs.Bool("skip-tidy", false, "skip go mod tidy before TinyGo build")
	tinyGoOpt := fs.String("tinygo-opt", "2", "TinyGo optimization level: 0, 1, 2, s, or z")
	tinyGoPanic := fs.String("tinygo-panic", "trap", "TinyGo panic strategy: trap or print")
	tinyGoDebug := fs.Bool("tinygo-debug", false, "keep TinyGo debug information")
	runWasmOpt := fs.Bool("wasm-opt", false, "run wasm-opt after TinyGo build")
	wasmOptLevel := fs.String("wasm-opt-level", "Oz", "wasm-opt level: O, O1, O2, O3, O4, Os, or Oz")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: switchboard build [--out dist] [PATH]")
	}
	source := cfg.Rule
	if fs.NArg() == 1 {
		source = fs.Arg(0)
	}
	if _, err := exec.LookPath("tinygo"); err != nil {
		return errors.New("tinygo is required to build Switchboard rules; install it from https://tinygo.org/getting-started/install/")
	}
	if !*skipTidy {
		if err := tidyRuleModule(ctx); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	modulePath := filepath.Join(*out, "module.wasm")
	buildSource, buildDir, cleanup, err := prepareBuildSource(source)
	if err != nil {
		return err
	}
	defer cleanup()
	tinyGoArgs, err := tinyGoBuildArgs(modulePath, buildSource, *tinyGoOpt, *tinyGoPanic, *tinyGoDebug)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "tinygo", tinyGoArgs...)
	if buildDir != "" {
		cmd.Dir = buildDir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if *runWasmOpt {
		if err := optimizeWasm(ctx, modulePath, *wasmOptLevel); err != nil {
			return err
		}
	}
	module, err := os.ReadFile(modulePath)
	if err != nil {
		return err
	}
	bundleID := newBundleID()
	bundleName := *name
	if bundleName == "" {
		bundleName = filepath.Base(filepath.Clean(source))
	}
	manifest := bundle.Manifest{
		Name:       bundleName,
		Version:    bundleID,
		ABI:        bundle.ABIVersion,
		Entrypoint: "handle",
		Language:   "go-tinygo",
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	checksum := bundle.ModuleChecksum(module)
	if err := os.WriteFile(filepath.Join(*out, "manifest.json"), manifestData, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "checksum.txt"), []byte(checksum+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("built %s (%s)\n", bundleID, checksum)
	return nil
}

func optimizeWasm(ctx context.Context, modulePath, level string) error {
	tmpPath := modulePath + ".wasm-opt"
	args, err := wasmOptArgs(modulePath, tmpPath, level)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("wasm-opt"); err != nil {
		return errors.New("wasm-opt is required for --wasm-opt; install Binaryen or omit --wasm-opt")
	}
	defer os.Remove(tmpPath)
	cmd := exec.CommandContext(ctx, "wasm-opt", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return os.Rename(tmpPath, modulePath)
}

func wasmOptArgs(modulePath, outputPath, level string) ([]string, error) {
	switch level {
	case "O", "O1", "O2", "O3", "O4", "Os", "Oz":
	default:
		return nil, fmt.Errorf("invalid wasm-opt-level %q", level)
	}
	levelFlag := "-" + level
	return []string{modulePath, "--converge", "--flatten", "--rereloop", levelFlag, "--gufa", levelFlag, "-o", outputPath}, nil
}

func tinyGoBuildArgs(modulePath, buildSource, optLevel, panicStrategy string, debug bool) ([]string, error) {
	switch optLevel {
	case "0", "1", "2", "s", "z":
	default:
		return nil, fmt.Errorf("invalid tinygo-opt %q", optLevel)
	}
	switch panicStrategy {
	case "trap", "print":
	default:
		return nil, fmt.Errorf("invalid tinygo-panic %q", panicStrategy)
	}
	args := []string{"build", "-target=wasi", "-opt=" + optLevel, "-panic=" + panicStrategy}
	if !debug {
		args = append(args, "-no-debug")
	}
	args = append(args, "-o", modulePath, buildSource)
	return args, nil
}

func deploy(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "namespace", "channel", "registry")
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	namespace := fs.String("namespace", cfg.Namespace, "namespace")
	channel := fs.String("channel", cfg.Channel, "channel name")
	registryURL := fs.String("registry", cfg.Registry, "registry URL, e.g. s3://bucket/prefix")
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
	b, err := readBundle(dist)
	if err != nil {
		return err
	}
	if err := reg.PutBundle(ctx, scope, b); err != nil {
		return err
	}
	pointer := bundle.ChannelPointer{
		Namespace: scope.Namespace,
		Channel:   *channel,
		BundleID:  b.ID,
		Checksum:  b.Checksum,
		CreatedAt: time.Now().UTC(),
	}
	if err := reg.PutChannel(ctx, scope, pointer); err != nil {
		return err
	}
	if scope.Namespace != "" {
		fmt.Printf("deployed bundle %s to namespace %s channel %s\n", b.ID, scope.Namespace, *channel)
		return nil
	}
	fmt.Printf("deployed bundle %s to channel %s\n", b.ID, *channel)
	return nil
}

func inspect(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "namespace", "channel", "registry")
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	namespace := fs.String("namespace", cfg.Namespace, "namespace")
	channel := fs.String("channel", cfg.Channel, "channel name")
	registryURL := fs.String("registry", cfg.Registry, "registry URL, e.g. s3://bucket/prefix")
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
	data, err := json.MarshalIndent(pointer, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func openRegistry(ctx context.Context, rawURL string) (registry.Registry, error) {
	cfg := registry.S3ConfigFromEnv()
	if rawURL != "" {
		parsed, err := registry.ParseS3URL(rawURL)
		if err != nil {
			return nil, err
		}
		cfg.Bucket = parsed.Bucket
		cfg.Prefix = parsed.Prefix
	}
	return registry.NewS3(ctx, cfg)
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

func tidyRuleModule(ctx context.Context) error {
	if _, err := os.Stat("go.mod"); err != nil {
		return nil
	}
	if _, err := exec.LookPath("go"); err != nil {
		return errors.New("go is required to resolve Switchboard rule dependencies")
	}
	cmd := exec.CommandContext(ctx, "go", "mod", "tidy")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go mod tidy failed; rerun with --skip-tidy if dependencies are already resolved: %w", err)
	}
	return nil
}

type sourcePackage struct {
	Name            string
	HasRuleHandle   bool
	HasExportHandle bool
	ImportPath      string
	ModuleDir       string
}

func prepareBuildSource(source string) (string, string, func(), error) {
	info, err := inspectRulePackage(source)
	if err != nil {
		return "", "", func() {}, err
	}
	if info.Name == "main" {
		if !info.HasExportHandle {
			return "", "", func() {}, fmt.Errorf("rule package %s is package main but does not define exported handle; use the new plain-package style with func Handle(req sdk.Request) sdk.Action", source)
		}
		return source, "", func() {}, nil
	}
	if !info.HasRuleHandle {
		return "", "", func() {}, fmt.Errorf("rule package %s must define func Handle(req sdk.Request) sdk.Action", source)
	}
	tmp, err := os.MkdirTemp(info.ModuleDir, ".switchboard-build-*")
	if err != nil {
		return "", "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(wrapperSource(info.ImportPath)), 0o644); err != nil {
		cleanup()
		return "", "", func() {}, err
	}
	return tmp, info.ModuleDir, cleanup, nil
}

func inspectRulePackage(source string) (sourcePackage, error) {
	dir := source
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return sourcePackage{}, err
		}
		dir = abs
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(info fs.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		return sourcePackage{}, err
	}
	if len(pkgs) != 1 {
		return sourcePackage{}, fmt.Errorf("rule source %s must contain exactly one package", source)
	}
	var pkg *ast.Package
	for _, parsed := range pkgs {
		pkg = parsed
	}
	info := sourcePackage{Name: pkg.Name}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == "Handle" {
				info.HasRuleHandle = true
			}
			if ok && fn.Recv == nil && fn.Name.Name == "handle" {
				info.HasExportHandle = true
			}
		}
	}
	importPath, moduleDir, err := packageImportPath(dir)
	if err != nil && info.Name != "main" {
		return sourcePackage{}, err
	}
	info.ImportPath = importPath
	info.ModuleDir = moduleDir
	return info, nil
}

func packageImportPath(absDir string) (string, string, error) {
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}\n{{.Module.Dir}}", ".")
	cmd.Dir = absDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("go list failed for %s: %w: %s", absDir, err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || lines[0] == "" || lines[1] == "" {
		return "", "", fmt.Errorf("go list did not return module information for %s", absDir)
	}
	return lines[0], lines[1], nil
}

func wrapperSource(importPath string) string {
	return fmt.Sprintf(`package main

import (
	"github.com/ethndotsh/switchboard/abi/guest"
	rule %q
)

//export handle
func handle() int32 {
	return guest.Return(rule.Handle(guest.CurrentRequest()))
}

func main() {}
`, importPath)
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

func readBundle(dir string) (bundle.Bundle, error) {
	module, err := os.ReadFile(filepath.Join(dir, "module.wasm"))
	if err != nil {
		return bundle.Bundle{}, err
	}
	manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return bundle.Bundle{}, err
	}
	checksumData, err := os.ReadFile(filepath.Join(dir, "checksum.txt"))
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
	if manifest.Version == "" {
		return bundle.Bundle{}, fs.ErrInvalid
	}
	return bundle.Bundle{ID: manifest.Version, Module: module, Manifest: manifest, Checksum: checksum}, nil
}

func usage() error {
	return errors.New("usage: switchboard <init|build|dist|deploy|inspect>")
}

func normalizeFlagArgs(args []string, valueFlags ...string) []string {
	valueFlagSet := map[string]bool{}
	for _, flag := range valueFlags {
		valueFlagSet[flag] = true
	}
	var flags []string
	var positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if valueFlagSet[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positionals...)
}

func newBundleID() string {
	var random [4]byte
	_, _ = rand.Read(random[:])
	stamp := time.Now().UTC().Format("2006-01-02T15-04-05Z")
	return stamp + "-" + hex.EncodeToString(random[:])
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "switchboard-rules"
	}
	return wd
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
		return sdk.Deny(403)
	}

	return sdk.Next().SetHeader("x-powered-by", "switchboard")
}
`, packageName)
}
