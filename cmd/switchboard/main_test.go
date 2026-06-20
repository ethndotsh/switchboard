package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandEnv(t *testing.T) {
	t.Setenv("SWITCHBOARD_RULE", "./rules/prod")
	t.Setenv("SWITCHBOARD_EMPTY", "")

	input := strings.Join([]string{
		"rule: ${SWITCHBOARD_RULE}",
		"channel: ${SWITCHBOARD_CHANNEL:-prod}",
		"dist: ${SWITCHBOARD_DIST-dist}",
		"name: $SWITCHBOARD_NAME",
		"empty_default: ${SWITCHBOARD_EMPTY:-fallback}",
		"empty_no_default: ${SWITCHBOARD_EMPTY-fallback}",
	}, "\n")

	got := string(expandEnv([]byte(input)))
	want := strings.Join([]string{
		"rule: ./rules/prod",
		"channel: prod",
		"dist: dist",
		"name: ",
		"empty_default: fallback",
		"empty_no_default: ",
	}, "\n")

	if got != want {
		t.Fatalf("expanded config mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestNormalizeFlagArgs(t *testing.T) {
	got := normalizeFlagArgs([]string{"./dist", "--channel", "prod", "--registry=s3://bucket/prefix"}, "channel", "registry")
	want := []string{"--channel", "prod", "--registry=s3://bucket/prefix", "./dist"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized args = %#v", got)
	}

	got = normalizeFlagArgs([]string{"./rules/basic", "--skip-tidy", "--out", "./dist"}, "out", "name")
	want = []string{"--skip-tidy", "--out", "./dist", "./rules/basic"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized bool args = %#v", got)
	}

	got = normalizeFlagArgs([]string{"./rules/basic", "--tinygo-opt", "s", "--tinygo-debug"}, "tinygo-opt", "tinygo-panic")
	want = []string{"--tinygo-opt", "s", "--tinygo-debug", "./rules/basic"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized tinygo args = %#v", got)
	}

	got = normalizeFlagArgs([]string{"./rules/basic", "--wasm-opt", "--wasm-opt-level", "O4"}, "wasm-opt-level")
	want = []string{"--wasm-opt", "--wasm-opt-level", "O4", "./rules/basic"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("normalized wasm-opt args = %#v", got)
	}
}

func TestTinyGoBuildArgsDefaultForPerformance(t *testing.T) {
	args, err := tinyGoBuildArgs("dist/module.wasm", "./rules/basic", "2", "trap", false)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	for _, want := range []string{"build", "-target=wasi", "-opt=2", "-panic=trap", "-no-debug", "-o dist/module.wasm ./rules/basic"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tinygo args %q missing %q", got, want)
		}
	}
}

func TestTinyGoBuildArgsDebugKeepsDebugInfo(t *testing.T) {
	args, err := tinyGoBuildArgs("dist/module.wasm", "./rules/basic", "z", "print", true)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	if strings.Contains(got, "-no-debug") {
		t.Fatalf("debug tinygo args should not strip debug info: %q", got)
	}
	if !strings.Contains(got, "-opt=z") || !strings.Contains(got, "-panic=print") {
		t.Fatalf("tinygo args = %q", got)
	}
}

func TestTinyGoBuildArgsRejectsInvalidTuning(t *testing.T) {
	if _, err := tinyGoBuildArgs("out", "src", "fast", "trap", false); err == nil {
		t.Fatal("expected invalid opt error")
	}
	if _, err := tinyGoBuildArgs("out", "src", "2", "explode", false); err == nil {
		t.Fatal("expected invalid panic error")
	}
}

func TestWasmOptArgs(t *testing.T) {
	args, err := wasmOptArgs("dist/module.wasm", "dist/module.wasm.opt", "Oz")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	for _, want := range []string{"dist/module.wasm", "--converge", "--flatten", "--rereloop", "-Oz", "--gufa", "-o dist/module.wasm.opt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wasm-opt args %q missing %q", got, want)
		}
	}
}

func TestWasmOptArgsRejectsInvalidLevel(t *testing.T) {
	if _, err := wasmOptArgs("dist/module.wasm", "dist/module.wasm.opt", "fast"); err == nil {
		t.Fatal("expected invalid wasm-opt level error")
	}
}

func TestInitGeneratesPlainRulePackage(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)

	if err := initProject([]string{"--name", "demo", "--module", "example.com/demo"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "rules", "basic", "rule.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "package basic") {
		t.Fatalf("expected plain package, got:\n%s", text)
	}
	if strings.Contains(text, "//export handle") || strings.Contains(text, "func main()") || strings.Contains(text, "abi/guest") {
		t.Fatalf("init rule should not expose wasm wrapper details:\n%s", text)
	}
	if !strings.Contains(text, `return sdk.Next().SetHeader("x-powered-by", "switchboard")`) {
		t.Fatalf("init rule should set x-powered-by:\n%s", text)
	}
}

func TestPrepareBuildSourceGeneratesWrapperForPlainPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/rules\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ruleDir := filepath.Join(dir, "rules", "basic")
	if err := os.MkdirAll(ruleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ruleDir, "rule.go"), []byte(`package basic

func Handle(req any) any { return req }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	source, buildDir, cleanup, err := prepareBuildSource(ruleDir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if buildDir != dir {
		t.Fatalf("build dir = %q, want %q", buildDir, dir)
	}
	if !strings.HasPrefix(source, dir) {
		t.Fatalf("wrapper source %q should be under module dir %q", source, dir)
	}
	wrapper, err := os.ReadFile(filepath.Join(source, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(wrapper)
	if !strings.Contains(text, `rule "example.com/rules/rules/basic"`) {
		t.Fatalf("wrapper imports wrong package:\n%s", text)
	}
	if !strings.Contains(text, "//export handle") || !strings.Contains(text, "func main() {}") {
		t.Fatalf("wrapper missing wasm glue:\n%s", text)
	}
}

func TestPrepareBuildSourceRejectsMainWithoutExport(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/mainrule\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rule.go"), []byte(`package main

func Handle() int32 { return 0 }

func main() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := prepareBuildSource(dir)
	if err == nil || !strings.Contains(err.Error(), "does not define exported handle") {
		t.Fatalf("expected friendly missing export error, got %v", err)
	}
}
