package main

import (
	"fmt"
	"io"
	"strings"
)

// explain renders the repo's seams as guidance for an agent: why each exists (its
// purpose, in the author's words), what is watched, and what a change there obliges. Generated from the annotations so it cannot drift
// from them; meant for an AGENTS.md section so the agent consults thresh
// mid-task, not only at the Stop hook. It ends with the verdict protocol, so the
// repo carries the rubric for recording whether a tap helped.
func explain(w io.Writer, cfg config) {
	fmt.Fprintln(w, "## Thresh seams")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Before finalizing an approach that touches any of these, run `thresh check` and")
	fmt.Fprintln(w, "read the taps. They are facts about your diff, not opinions, and they never block.")
	fmt.Fprintln(w)
	for _, name := range sortedKeys(cfg.Seams) {
		s := cfg.Seams[name]
		fmt.Fprintf(w, "- **%s** (`%s`): %s\n  %s\n", name, s.Kind, s.Purpose, obligation(s))
	}
	fmt.Fprintln(w)
	fmt.Fprint(w, protocol)
}

func obligation(s seam) string {
	p := strings.Join(s.Paths, ", ")
	switch s.Kind {
	case "published-language":
		if s.Format == "golden" {
			return fmt.Sprintf("%s is recorded output that consumers you cannot enumerate depend on. A change that moves it owes them compatibility, and is loud even when correct.", p)
		}
		return fmt.Sprintf("%s is a versioned contract for consumers you cannot enumerate. A change owes them compatibility; a breaking change is loud even when correct.", p)
	case "shared-kernel":
		who := "its co-owners"
		if len(s.Consult) > 0 {
			who = strings.Join(s.Consult, "; ")
		}
		if len(s.Watch) > 0 {
			return fmt.Sprintf("in %s, the lines mentioning %s are co-owned substrate. A change there owes consultation with: %s.", p, strings.Join(s.Watch, ", "), who)
		}
		return fmt.Sprintf("%s is co-owned kernel. A change there owes consultation with: %s.", p, who)
	case "choke-point":
		return fmt.Sprintf("in %s, only %s may construct (Go) or use (Python) %s. Doing so elsewhere skips what the choke point enforces.", p, strings.Join(s.OnlyIn, "/"), s.Type)
	case "declared-set":
		what := s.Funcs + " functions"
		if s.Defs != "" {
			what = s.Defs + " definitions"
		}
		if s.Entries != "" {
			what = "entries of " + s.Entries
		}
		return fmt.Sprintf("the %s in %s are an intentional, enumerated set (%d declared). Adding one requires declaring why it belongs.", what, p, len(s.Declared))
	case "near-copy":
		share := s.Containment
		if share == 0 {
			share = nearCopyContainment
		}
		return fmt.Sprintf("in %s, one sequence of steps has one owner: a function you write anew (not renamed, moved, or with only a name, a constant, a comment or a docstring changed) that shares at least %.0f%% of the smaller one's winnowed fingerprints (those at most %d functions hold) with another function is noted. An exact copy of another is a duplicate checker's, not noted.", p, 100*share, nearCopyCommon)
	case "references-resolve":
		return fmt.Sprintf("Go files (%s) reach into CUE files (%s) by literal path; every such reference must resolve.", strings.Join(s.Go, ", "), strings.Join(s.Cue, ", "))
	}
	return "(unknown kind)"
}
