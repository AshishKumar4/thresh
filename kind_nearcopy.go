package main

import (
	"fmt"
	"os"
	"strings"
)

// near-copy: one sequence of steps has one owner. Over the Python files under
// paths, a function the diff writes anew whose winnowed fingerprints are
// mostly another's there (extract_copies.go's nearCopies over extract_py.go's
// reading) is noted, with the share and whose fingerprints it is of. Fires on
// wrongness. Whether the two should be one is the reader's verdict, never the
// tool's; identical copies are a duplicate checker's.
type nearCopy struct{}

const (
	// nearCopyContainment is the share of fingerprints a seam taps at by default.
	nearCopyContainment = 0.9
	// nearCopyCommon: a fingerprint more functions hold than this is an idiom
	// of the codebase (a shared setup, a common assertion), not read.
	nearCopyCommon = 40
	// nearCopyFewest: a function with fewer fingerprints left is too short to call a copy.
	nearCopyFewest = 4
)

func (nearCopy) assess(name string, s seam, cs *changeset) []signal {
	touched := map[string]bool{}
	for _, p := range cs.changed {
		if isPython(p) && matchAny(s.Paths, p) {
			touched[p] = true
		}
	}
	if len(touched) == 0 {
		return nil
	}
	head, ok := pyCopyTree(cs.current(s.Paths))
	if !ok {
		return nil
	}
	base, ok := pyCopyTree(cs.atBase(s.Paths))
	if !ok {
		return nil
	}
	for _, p := range sortedKeys(touched) {
		if head.Unread[p] || base.Unread[p] {
			fmt.Fprintf(os.Stderr, "thresh: %s does not parse on one side of the diff; seam %s does not read it\n", p, name)
		}
	}
	threshold := s.Containment
	if threshold == 0 {
		threshold = nearCopyContainment
	}
	var out []signal
	for _, m := range nearCopies(head, base, touched, threshold, nearCopyCommon, nearCopyFewest) {
		u, first := m.Fresh, m.Partners[0]
		also := ""
		if rest := m.Partners[1:]; len(rest) > 0 {
			var names []string
			for _, p := range rest[:min(len(rest), 3)] {
				names = append(names, fmt.Sprintf("%s (%s:%d)", p.Func.Name, p.Func.File, p.Func.Line))
			}
			if len(rest) > 3 {
				names = append(names, fmt.Sprintf("%d more", len(rest)-3))
			}
			also = fmt.Sprintf("; at least %.0f%% too with %s", 100*threshold, strings.Join(names, ", "))
		}
		out = append(out, signal{Kind: s.Kind, Seam: name, File: u.File, Level: "note",
			Message: fmt.Sprintf("%s (line %d) and %s (%s:%d) share %.0f%% of %s's winnowed fingerprints (those at most %d functions hold)%s: one sequence of steps written more than once, not identically. Give it one owner, or say why they differ.",
				u.Name, u.Line, first.Func.Name, first.Func.File, first.Func.Line, 100*first.Share, first.Smaller, nearCopyCommon, also)})
	}
	return out
}
