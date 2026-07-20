package sdk

import "testing"

func TestDataSet(t *testing.T) {
	LoadTestData(map[string][]byte{
		"allowlist.txt": []byte("# comment\n203.0.113.7\n\n192.0.2.10\n"),
	})
	set := DataSet("allowlist.txt")
	if !set.Contains("203.0.113.7") || !set.Contains("192.0.2.10") {
		t.Fatal("expected allowlisted entries to be present")
	}
	if set.Contains("# comment") || set.Contains("") || set.Contains("10.0.0.5") {
		t.Fatal("comments, blanks, and absent entries must not be members")
	}
}

func TestDataLines(t *testing.T) {
	LoadTestData(map[string][]byte{
		"order.txt": []byte("# ordered\nfirst\nsecond\nfirst\n\nthird\n"),
	})
	lines := DataLines("order.txt")
	want := []string{"first", "second", "first", "third"}
	if len(lines) != len(want) {
		t.Fatalf("got %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
	// DataSet over the same file dedups and drops order.
	if n := len(DataSet("order.txt")); n != 3 {
		t.Fatalf("set size = %d, want 3", n)
	}
}

func TestDataMap(t *testing.T) {
	LoadTestData(map[string][]byte{
		"flags.json": []byte(`{"beta":"on","legacy":"off"}`),
	})
	flags := DataMap("flags.json")
	if flags["beta"] != "on" || flags["legacy"] != "off" {
		t.Fatalf("unexpected flags: %v", flags)
	}
	if DataMap("missing.json") != nil {
		t.Fatal("missing file should yield nil map")
	}
}

func TestDataJSON(t *testing.T) {
	LoadTestData(map[string][]byte{"config.json": []byte(`{"beta":true,"rollout":25,"paths":["/a","/b"]}`)})
	var cfg struct {
		Beta    bool     `json:"beta"`
		Rollout int      `json:"rollout"`
		Paths   []string `json:"paths"`
	}
	if err := DataJSON("config.json", &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Beta || cfg.Rollout != 25 || len(cfg.Paths) != 2 {
		t.Fatalf("unexpected struct: %+v", cfg)
	}
	// Missing file leaves the target untouched, no error.
	var empty struct{ X int }
	if err := DataJSON("absent.json", &empty); err != nil {
		t.Fatal(err)
	}
}

func TestDataJSONL(t *testing.T) {
	LoadTestData(map[string][]byte{"rows.jsonl": []byte("{\"id\":\"a\"}\n{\"id\":\"b\"}\n{\"id\":\"c\"}\n")})
	var rows []struct {
		ID string `json:"id"`
	}
	if err := DataJSONL("rows.jsonl", &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].ID != "a" || rows[2].ID != "c" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := DataJSONL("rows.jsonl", &struct{}{}); err == nil {
		t.Fatal("expected error when out is not a pointer to slice")
	}
}

func TestDataTOML(t *testing.T) {
	LoadTestData(map[string][]byte{"config.toml": []byte("beta = true\nrollout = 9\n")})
	var cfg struct {
		Beta    bool `toml:"beta"`
		Rollout int  `toml:"rollout"`
	}
	if err := DataTOML("config.toml", &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Beta || cfg.Rollout != 9 {
		t.Fatalf("unexpected struct: %+v", cfg)
	}
}

func TestDataCSV(t *testing.T) {
	LoadTestData(map[string][]byte{"t.csv": []byte("a,b\n1,2\n3,4\n")})
	rows, err := DataCSV("t.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0][1] != "b" || rows[2][0] != "3" {
		t.Fatalf("unexpected rows: %v", rows)
	}
	if got, _ := DataCSV("absent.csv"); got != nil {
		t.Fatal("absent CSV should be nil")
	}
}

func TestDataBytesAndString(t *testing.T) {
	LoadTestData(map[string][]byte{"greeting.txt": []byte("hello")})
	if DataString("greeting.txt") != "hello" {
		t.Fatal("unexpected string")
	}
	if DataBytes("absent.txt") != nil {
		t.Fatal("absent file should be nil")
	}
}

func TestLoadTestDataStripsPrefix(t *testing.T) {
	LoadTestData(map[string][]byte{"data/allowlist.txt": []byte("1.2.3.4\n")})
	if !DataSet("allowlist.txt").Contains("1.2.3.4") {
		t.Fatal("keys given with the data/ prefix should resolve by relative name")
	}
}

func TestLoadTestDataResetsCache(t *testing.T) {
	LoadTestData(map[string][]byte{"allowlist.txt": []byte("1.1.1.1\n")})
	if !DataSet("allowlist.txt").Contains("1.1.1.1") {
		t.Fatal("first load missing entry")
	}
	LoadTestData(map[string][]byte{"allowlist.txt": []byte("2.2.2.2\n")})
	set := DataSet("allowlist.txt")
	if set.Contains("1.1.1.1") || !set.Contains("2.2.2.2") {
		t.Fatal("reload must reset the memoized set")
	}
}
