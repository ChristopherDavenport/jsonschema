package jsonschema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/jsonschema/dialect"
)

// suiteRoot is the vendored official JSON Schema Test Suite.
const suiteRoot = "third_party/JSON-Schema-Test-Suite"

// testGroup mirrors one element of a suite file: a schema plus its cases.
type testGroup struct {
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Tests       []struct {
		Description string          `json:"description"`
		Data        json.RawMessage `json:"data"`
		Valid       bool            `json:"valid"`
	} `json:"tests"`
}

type remoteDoc struct {
	uri  string
	data []byte
}

// loadRemotes reads the suite's remotes/ tree, mapping each file to the URL the
// suite expects it to be served from.
func loadRemotes(t *testing.T) []remoteDoc {
	t.Helper()
	base := filepath.Join(suiteRoot, "remotes")
	var out []remoteDoc
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		rel, _ := filepath.Rel(base, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, remoteDoc{
			uri:  "http://localhost:1234/" + filepath.ToSlash(rel),
			data: data,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("loading remotes: %v", err)
	}
	return out
}

// conformanceDrafts lists the suite directories exercised and the draft each
// document defaults to when it carries no $schema.
var conformanceDrafts = []struct {
	dir   string
	draft dialect.Draft
}{
	{"draft2020-12", dialect.Draft2020},
	{"draft2019-09", dialect.Draft2019},
	{"draft7", dialect.Draft7},
}

func TestConformance(t *testing.T) {
	if _, err := os.Stat(suiteRoot); err != nil {
		t.Skipf("test suite not present (run: git submodule update --init): %v", err)
	}
	remotes := loadRemotes(t)
	strict := os.Getenv("JSONSCHEMA_STRICT") != ""

	for _, dc := range conformanceDrafts {
		dc := dc
		t.Run(dc.dir, func(t *testing.T) {
			dir := filepath.Join(suiteRoot, "tests", dc.dir)
			files, err := filepath.Glob(filepath.Join(dir, "*.json"))
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(files)

			var pass, fail int
			failByFile := map[string]int{}

			for _, file := range files {
				groups, err := readGroups(file)
				if err != nil {
					t.Errorf("read %s: %v", file, err)
					continue
				}
				name := filepath.Base(file)
				for _, g := range groups {
					for _, tc := range g.Tests {
						ok := runCase(dc.draft, remotes, g.Schema, tc.Data, tc.Valid)
						if ok {
							pass++
						} else {
							fail++
							failByFile[name]++
							if strict {
								t.Errorf("%s: %s / %s (want valid=%v)", name, g.Description, tc.Description, tc.Valid)
							}
						}
					}
				}
			}

			total := pass + fail
			rate := 100.0
			if total > 0 {
				rate = 100 * float64(pass) / float64(total)
			}
			t.Logf("%s: %d/%d passed (%.1f%%)", dc.dir, pass, total, rate)
			for _, f := range sortedKeys(failByFile) {
				t.Logf("    %-40s %d failing", f, failByFile[f])
			}
			// The required set is fully green; any failure is a regression.
			if fail > 0 {
				t.Errorf("%s: %d conformance cases failing (run with JSONSCHEMA_STRICT=1 for per-case detail)", dc.dir, fail)
			}
		})
	}
}

func readGroups(file string) ([]testGroup, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var groups []testGroup
	if err := json.Unmarshal(data, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

// runCase compiles schema (with remotes registered) and reports whether the
// engine's verdict on data matches the expected validity.
func runCase(draft dialect.Draft, remotes []remoteDoc, schema, rawData json.RawMessage, want bool) (ok bool) {
	defer func() {
		// A panic on an exotic schema counts as a failure, never a crash.
		if r := recover(); r != nil {
			ok = false
		}
	}()

	data, err := decodeInstance(rawData)
	if err != nil {
		return false
	}

	c := NewCompiler().DefaultDraft(draft)
	if _, err := c.RegisterMetaSchemas(); err != nil {
		return false
	}
	for _, r := range remotes {
		if err := c.AddResource(r.uri, r.data); err != nil {
			return false
		}
	}
	s, err := c.AddAndCompile("", schema)
	if err != nil {
		return false
	}
	got := s.Validate(data) == nil
	return got == want
}

// decodeInstance decodes a test instance with UseNumber, so numbers arrive as
// json.Number and compare exactly against schema numbers.
func decodeInstance(b []byte) (any, error) {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
