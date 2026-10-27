package hub

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/askolesov/claude-mux/internal/migrate"
)

// hubSteps upgrade hub.json, one version each; dir is where it is. Never
// remove or change a step once released: add the next one, and a test with
// a file of the version before it.
func hubSteps(dir string) []migrate.Step {
	return []migrate.Step{
		// 0 → 1: everything before versions (up to v0.6).
		// - v0.3 kept the update check's fields at the top level.
		// - Before v0.4 the environments were in environments.json.
		func(doc map[string]any) error {
			if _, ok := doc["settings"]; !ok {
				settings, update := map[string]any{}, map[string]any{}
				for _, k := range []string{"checkUpdates", "everyDays"} {
					if v, ok := doc[k]; ok {
						settings[k] = v
						delete(doc, k)
					}
				}
				for _, k := range []string{"lastCheck", "skip"} {
					if v, ok := doc[k]; ok {
						update[k] = v
						delete(doc, k)
					}
				}
				if len(settings) > 0 {
					doc["settings"] = settings
				}
				doc["update"] = update
			}
			data, err := os.ReadFile(filepath.Join(dir, "environments.json"))
			if err != nil {
				return nil
			}
			var envs []map[string]any
			if err := json.Unmarshal(data, &envs); err != nil {
				return err
			}
			migrate.SetList(doc, "environments", append(migrate.List(doc, "environments"), envs...))
			return nil
		},
		// 1 → 2: v0.7 gives each tab its own startup message. The resume
		// default leaves the settings, kept only to upgrade backends.
		func(doc map[string]any) error {
			settings, _ := doc["settings"].(map[string]any)
			if r, ok := settings["resume"]; ok {
				doc["resumeDefault"] = r
				delete(settings, "resume")
			}
			return nil
		},
	}
}
