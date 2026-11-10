package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// declared-set: a set of code elements that is intentional and enumerated. The
// human judges membership once, at declaration time (with a reason); the tool
// only enforces that the set never grows silently and never goes stale.
// Fires on wrongness. The *bodies* of declared elements are not its concern —
// whether they are correct belongs to the target's tests.
type declaredSet struct{}

func (declaredSet) assess(name string, s seam, cs *changeset) []signal {
	var out []signal
	for _, f := range cs.under(s.Paths) {
		glob, elems := s.Funcs, goFuncElements
		switch {
		case s.Defs != "":
			glob, elems = s.Defs, cueDefElements
		case s.Entries != "":
			glob, elems = "", pyRegistryElements(s.Entries) // a registry's keys are identities, not names to glob
		case isPython(f.Path):
			elems = pyFuncElements
		}
		in := func(e element) bool {
			ok, _ := filepath.Match(glob, e.name)
			return glob == "" || ok
		}
		was, wasRead := elems(f.Before)
		now, nowRead := elems(f.After)
		if !wasRead || !nowRead {
			fmt.Fprintf(os.Stderr, "thresh: %s cannot be read on one side of the diff (it does not parse, or python3 cannot run); seam %s does not compare its set there\n", f.Path, name)
			continue
		}
		before := map[string]bool{}
		unnamedBefore := 0
		for _, e := range was {
			if e.name == "" {
				unnamedBefore++
			} else if in(e) {
				before[e.name] = true
			}
		}
		// A registry entry whose key the program computes cannot be named, so the set cannot be compared at
		// it: more of them than before is said, never passed over.
		var unnamed []string
		for _, e := range now {
			if e.name == "" {
				unnamed = append(unnamed, fmt.Sprint(e.line))
			}
		}
		if len(unnamed) > unnamedBefore {
			out = append(out, signal{Kind: s.Kind, Seam: name, File: f.Path, Level: "step-back",
				Message: fmt.Sprintf("registry entries whose keys cannot be named (lines %s): a declared set reads a key written as a constant, a dotted name, a tuple of them, or a call of a dotted name on them; this set cannot see what such an entry adds.",
					strings.Join(unnamed, ", "))})
		}
		after := map[string]bool{}
		for _, e := range now {
			if e.name == "" || !in(e) {
				continue
			}
			after[e.name] = true
			if _, declared := s.Declared[e.name]; !declared && !before[e.name] {
				out = append(out, signal{Kind: s.Kind, Seam: name, File: f.Path, Level: "step-back",
					Message: fmt.Sprintf("undeclared element added: %s (line %d). This set is intentional — declare why it belongs here, or it may not.",
						e.name, e.line)})
			}
		}
		for _, d := range sortedKeys(s.Declared) {
			if before[d] && !after[d] {
				out = append(out, signal{Kind: s.Kind, Seam: name, File: f.Path, Level: "note",
					Message: fmt.Sprintf("declared element %s no longer exists. Remove it from the declared set (did its rule move elsewhere?).", d)})
			}
		}
	}
	return out
}

type element struct {
	name string
	line int
}

// The element extractors return a source's elements and whether it was read: a
// set is compared only where both sides were. Go and CUE sources read as empty
// where they do not parse.
func goFuncElements(src string) ([]element, bool) {
	var out []element
	for _, fn := range goFuncs(src) {
		out = append(out, element{fn.Name, fn.Line})
	}
	return out, true
}

func cueDefElements(src string) ([]element, bool) {
	var out []element
	for _, d := range cueDefLabels(src) {
		out = append(out, element{d.Name, d.Line})
	}
	return out, true
}
