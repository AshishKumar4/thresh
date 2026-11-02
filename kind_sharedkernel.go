package main

import (
	"fmt"
	"strings"
)

// shared-kernel: code co-owned by contexts you can name. You owe them
// consultation. Flat by nature — there is no compat notion in co-ownership —
// so the tap says what changed and who to consult. Fires on consequence.
type sharedKernel struct{}

func (sharedKernel) assess(name string, s seam, cs *changeset) []signal {
	var out []signal
	for _, f := range cs.under(s.Paths) {
		if !touched(f) {
			continue
		}
		var what string
		if len(s.Watch) > 0 {
			lines := changedLines(cs, f.Path)
			if s.Added {
				lines = addedText(cs, f.Path)
			}
			hits := watched(lines, s.Watch)
			if len(hits) == 0 {
				continue // the file changed, but not a watched line
			}
			what = "co-owned dependency moved: " + strings.Join(hits, "; ") + "."
		} else if defs := defsTouched(f); len(defs) > 0 {
			what = "co-owned kernel changed: " + strings.Join(defs, ", ") + "."
		} else {
			what = "co-owned kernel changed."
		}
		msg := what
		if len(s.Consult) > 0 {
			msg += fmt.Sprintf(" Consult: %s.", strings.Join(s.Consult, "; "))
		}
		out = append(out, signal{Kind: s.Kind, Seam: name, File: f.Path, Level: "step-back", Message: msg})
	}
	return out
}
