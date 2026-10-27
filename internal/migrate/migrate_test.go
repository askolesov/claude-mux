package migrate

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestUpgrade(t *testing.T) {
	var ran []int
	steps := []Step{
		func(d map[string]any) error { ran = append(ran, 0); d["a"] = 1; return nil },
		func(d map[string]any) error { ran = append(ran, 1); d["b"] = d["a"]; return nil },
	}
	out, changed, err := Upgrade([]byte(`{"x":true}`), steps)
	if err != nil || !changed {
		t.Fatal(err, changed)
	}
	var doc map[string]any
	_ = json.Unmarshal(out, &doc)
	if Version(doc) != 2 || doc["b"] != float64(1) || len(ran) != 2 {
		t.Errorf("doc %v, ran %v", doc, ran)
	}

	ran = nil
	if _, changed, _ := Upgrade([]byte(`{"version":1,"a":5}`), steps); !changed || len(ran) != 1 || ran[0] != 1 {
		t.Errorf("from 1 ran %v", ran)
	}
	if _, changed, _ := Upgrade([]byte(`{"version":2}`), steps); changed {
		t.Error("a current file changed")
	}
	if _, _, err := Upgrade([]byte(`{"version":3}`), steps); !errors.Is(err, ErrNewer) {
		t.Errorf("newer: %v", err)
	}
}
