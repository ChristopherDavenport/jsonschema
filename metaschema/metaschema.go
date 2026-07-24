// Package metaschema bundles the official JSON Schema meta-schemas so that
// documents which reference them (for self-validation) resolve offline.
package metaschema

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed all:2020-12 all:2019-09 all:draft-07
var files embed.FS

// Documents returns every bundled meta-schema keyed by its canonical $id
// (with any trailing '#' fragment removed).
func Documents() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		data, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		var head struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(data, &head); err != nil {
			return fmt.Errorf("metaschema %s: %w", path, err)
		}
		uri := strings.TrimSuffix(head.ID, "#")
		if uri == "" {
			return fmt.Errorf("metaschema %s: missing $id", path)
		}
		out[uri] = data
		return nil
	})
	return out, err
}
