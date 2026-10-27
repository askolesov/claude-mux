package backend

import (
	"github.com/askolesov/claude-mux/internal/migrate"
)

// stateSteps upgrade backend.json, one version each. Never remove or change
// a step once released: add the next one, and a test with a file of the
// version before it.
var stateSteps = []migrate.Step{
	// 0 → 1: everything before versions (up to v0.6).
	// - A tab's startupMessage (before v0.5) is its own resume message.
	// - A tab's group was a name, and a group's tabs sat together where its
	//   first tab was. Each name becomes a group node at the root, in that
	//   place, and every node gets its order key.
	func(doc map[string]any) error {
		sessions := migrate.List(doc, "sessions")
		groups := []map[string]any{}
		ids := map[string]string{} // group name → its id
		last := map[string]string{"": ""}
		next := func(parent string) string {
			last[parent] = keyBetween(last[parent], "")
			return last[parent]
		}
		for _, s := range sessions {
			if msg, _ := s["startupMessage"].(string); msg != "" {
				s["resume"], s["resumeMessage"] = string(ResumeCustom), msg
			}
			delete(s, "startupMessage")
			name, _ := s["group"].(string)
			delete(s, "group")
			parent := ""
			if name != "" {
				if ids[name] == "" {
					ids[name] = newID()
					groups = append(groups, map[string]any{"id": ids[name], "name": name, "order": next("")})
				}
				parent = ids[name]
				s["parent"] = parent
			}
			s["order"] = next(parent)
		}
		migrate.SetList(doc, "sessions", sessions)
		migrate.SetList(doc, "groups", groups)
		return nil
	},
}
