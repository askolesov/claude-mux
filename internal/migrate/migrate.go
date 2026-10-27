// Package migrate keeps claude-mux's state files readable across versions.
// Each file carries a "version"; each change to its shape is a numbered step
// that upgrades the JSON from one version to the next. Steps are never
// removed, so a file from any release upgrades to the current one, and a
// file from a newer release is refused rather than overwritten.
package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// A Step upgrades a document by one version, in place.
type Step func(doc map[string]any) error

// ErrNewer is the error for a file a newer claude-mux wrote.
var ErrNewer = errors.New("written by a newer claude-mux; update this one")

// Upgrade brings data up to version len(steps) and returns it, and whether
// it changed. A document without a version is version 0.
func Upgrade(data []byte, steps []Step) ([]byte, bool, error) {
	doc := map[string]any{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, false, err
	}
	v := Version(doc)
	switch {
	case v > len(steps):
		return nil, false, fmt.Errorf("version %d: %w", v, ErrNewer)
	case v == len(steps):
		return data, false, nil
	}
	for ; v < len(steps); v++ {
		if err := steps[v](doc); err != nil {
			return nil, false, fmt.Errorf("upgrading from version %d: %w", v, err)
		}
	}
	doc["version"] = len(steps)
	out, err := json.Marshal(doc)
	return out, true, err
}

// Version is a document's version, 0 for none.
func Version(doc map[string]any) int {
	v, _ := doc["version"].(float64)
	return int(v)
}

// Load reads path and upgrades it, saying whether it did; the caller then
// writes the upgraded file back. The old one is kept as path.v<version>.bak
// first, so a step that went wrong loses nothing. A missing file is nil.
func Load(path string, steps []Step) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out, changed, err := Upgrade(data, steps)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	if changed {
		var doc map[string]any
		_ = json.Unmarshal(data, &doc)
		if err := os.WriteFile(fmt.Sprintf("%s.v%d.bak", path, Version(doc)), data, 0o600); err != nil {
			return nil, false, err
		}
	}
	return out, changed, nil
}

// List reads doc[key] as a list of objects; a missing key is an empty list.
func List(doc map[string]any, key string) []map[string]any {
	raw, _ := doc[key].([]any)
	var out []map[string]any
	for _, x := range raw {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// SetList stores a list of objects as doc[key].
func SetList(doc map[string]any, key string, list []map[string]any) {
	raw := make([]any, len(list))
	for i, m := range list {
		raw[i] = m
	}
	doc[key] = raw
}
