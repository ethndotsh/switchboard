package sdk

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
)

// Data accessors expose read-only files bundled under the project's data
// directory. Files are named relative to that directory (e.g. "allowlist.txt"
// for data/allowlist.txt) and are immutable for the life of the instance.
//
// The raw bytes of each file are memoized. The parsed helpers each link their
// parser into the guest only when called, so a rule pays for a format only if
// it uses it (a rule that calls just DataSet never links encoding/json or the
// TOML parser).
var (
	dataCache  map[string][]byte
	setCache   map[string]Set
	linesCache map[string][]string
	mapCache   map[string]map[string]string
	// testData backs the accessors in host-side rule tests; LoadTestData
	// fills it. In the wasm guest, data is read from the host instead.
	testData map[string][]byte
)

// LoadTestData injects bundled data files for host-side rule unit tests. Keys
// may be given with or without the "data/" prefix. It resets the accessor
// caches so successive tests see fresh data.
func LoadTestData(files map[string][]byte) {
	testData = make(map[string][]byte, len(files))
	for name, value := range files {
		testData[strings.TrimPrefix(name, "data/")] = value
	}
	dataCache = nil
	setCache = nil
	linesCache = nil
	mapCache = nil
}

// DataBytes returns the raw contents of a bundled data file, or nil if it is
// not present in the bundle.
func DataBytes(name string) []byte {
	if dataCache == nil {
		dataCache = map[string][]byte{}
	}
	if value, ok := dataCache[name]; ok {
		return value
	}
	value := dataRead(name)
	dataCache[name] = value
	return value
}

// DataString returns a bundled data file as a string.
func DataString(name string) string {
	return string(DataBytes(name))
}

// DataJSON unmarshals a bundled JSON file into v (a pointer). A missing file
// leaves v untouched and returns nil.
func DataJSON(name string, v any) error {
	raw := DataBytes(name)
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// DataJSONL decodes a bundled JSON Lines file (one JSON value per line) into
// out, which must be a pointer to a slice. A missing file leaves out empty.
func DataJSONL(name string, out any) error {
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Slice {
		return errors.New("DataJSONL: out must be a pointer to a slice")
	}
	raw := DataBytes(name)
	if len(raw) == 0 {
		return nil
	}
	slice := rv.Elem()
	elemType := slice.Type().Elem()
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		elem := reflect.New(elemType)
		err := dec.Decode(elem.Interface())
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		slice.Set(reflect.Append(slice, elem.Elem()))
	}
	return nil
}

// DataTOML unmarshals a bundled TOML file into v (a pointer). A missing file
// leaves v untouched and returns nil.
func DataTOML(name string, v any) error {
	raw := DataBytes(name)
	if len(raw) == 0 {
		return nil
	}
	return toml.Unmarshal(raw, v)
}

// DataCSV parses a bundled CSV file into rows of fields. A missing file yields
// no rows.
func DataCSV(name string) ([][]string, error) {
	raw := DataBytes(name)
	if len(raw) == 0 {
		return nil, nil
	}
	return csv.NewReader(bytes.NewReader(raw)).ReadAll()
}

// DataMap parses a bundled JSON object file into a string map. A missing file
// or invalid JSON yields nil.
func DataMap(name string) map[string]string {
	if mapCache == nil {
		mapCache = map[string]map[string]string{}
	}
	if value, ok := mapCache[name]; ok {
		return value
	}
	var out map[string]string
	if raw := DataBytes(name); len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			out = nil
		}
	}
	mapCache[name] = out
	return out
}

// DataLines parses a bundled newline-delimited file into ordered lines. Blank
// lines and lines beginning with '#' are ignored; order and duplicates are
// preserved (use DataSet for membership tests).
func DataLines(name string) []string {
	if linesCache == nil {
		linesCache = map[string][]string{}
	}
	if value, ok := linesCache[name]; ok {
		return value
	}
	var lines []string
	if raw := DataBytes(name); len(raw) > 0 {
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			lines = append(lines, line)
		}
	}
	linesCache[name] = lines
	return lines
}

// Set is a membership lookup over newline-delimited data files.
type Set map[string]struct{}

// Contains reports whether value is a member of the set.
func (s Set) Contains(value string) bool {
	_, ok := s[value]
	return ok
}

// DataSet parses a bundled newline-delimited file into a Set. Blank lines and
// lines beginning with '#' are ignored.
func DataSet(name string) Set {
	if setCache == nil {
		setCache = map[string]Set{}
	}
	if value, ok := setCache[name]; ok {
		return value
	}
	set := Set{}
	for _, line := range DataLines(name) {
		set[line] = struct{}{}
	}
	setCache[name] = set
	return set
}
