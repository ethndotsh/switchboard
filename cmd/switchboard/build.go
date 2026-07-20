package main

import (
	"bytes"
	"context"
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

	"github.com/ethndotsh/switchboard/engine"
	"github.com/ethndotsh/switchboard/internal/bundle"
)

func build(ctx context.Context, args []string) error {
	cfg := loadConfigOrDefault()
	args = normalizeFlagArgs(args, "out", "name", "cases", "data", "max-data-bytes", "tinygo-opt", "tinygo-panic", "wasm-opt-level")
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	out := fs.String("out", cfg.Dist, "output directory")
	name := fs.String("name", cfg.Name, "bundle name")
	cases := fs.String("cases", cfg.Tests, "tests.yaml to embed in the bundle")
	dataDir := fs.String("data", cfg.Data, "directory of read-only data files to embed in the bundle")
	maxDataBytes := fs.String("max-data-bytes", cfg.MaxDataBytes, "maximum total size of embedded data files")
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

	bundleName := *name
	if bundleName == "" {
		bundleName = filepath.Base(filepath.Clean(source))
	}
	tests, err := resolveTestsFile(*cases, source)
	if err != nil {
		return err
	}
	data, err := resolveDataDir(*dataDir, source)
	if err != nil {
		return err
	}
	maxData := engine.DefaultMaxDataBytes
	if *maxDataBytes != "" {
		limit, err := engine.ParseByteSize(*maxDataBytes)
		if err != nil {
			return fmt.Errorf("invalid max-data-bytes: %w", err)
		}
		maxData = int(limit)
	}
	result, err := writeBundleArtifacts(*out, module, bundleArtifactOptions{
		Name:         bundleName,
		Tests:        tests,
		Data:         data,
		MaxDataBytes: maxData,
		Provenance:   buildProvenance(ctx),
	})
	if err != nil {
		return err
	}
	summary := fmt.Sprintf("built %s (%s", abbreviateBundleID(result.BundleID), result.Checksum)
	if result.TestCases > 0 {
		summary += fmt.Sprintf(", %d embedded test cases", result.TestCases)
	}
	if result.DataFiles > 0 {
		summary += fmt.Sprintf(", %d data files", result.DataFiles)
	}
	fmt.Println(summary + ")")
	return nil
}

// resolveDataDir reads a directory of read-only data files, keyed by their
// bundle artifact name (data/<path>). An explicit path must exist; the default
// data/ directory next to the rule is optional.
func resolveDataDir(explicit, rulePath string) (map[string][]byte, error) {
	dir := explicit
	optional := false
	if dir == "" {
		dir = filepath.Join(rulePath, "data")
		optional = true
	}
	info, err := os.Stat(dir)
	if err != nil {
		if optional && errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read data dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("data path %s is not a directory", dir)
	}
	data := map[string][]byte{}
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		data[bundle.DataPrefix+filepath.ToSlash(rel)] = contents
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(data) == 0 {
		return nil, nil
	}
	return data, nil
}

// resolveTestsFile prefers an explicit path, then tests.yaml next to the
// rule package.
func resolveTestsFile(explicit, rulePath string) ([]byte, error) {
	if explicit != "" {
		data, err := os.ReadFile(explicit)
		if err != nil {
			return nil, fmt.Errorf("read tests file: %w", err)
		}
		return data, nil
	}
	data, err := os.ReadFile(filepath.Join(rulePath, "tests.yaml"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
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
	// Parse stdout only: on a cold module cache, go writes "go: downloading …"
	// progress to stderr, which must not contaminate the two expected lines.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("go list failed for %s: %w: %s", absDir, err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
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
