package main

import "fmt"

// references-resolve: Go reaches into CUE by literal paths (cue.Str, cue.Def,
// cue.Hid). Fire when a reference no longer resolves in the CUE — the scoped
// replacement for file-level co-change. Evaluated whenever either side changed,
// against the CURRENT content of all files on both sides (a CUE rename breaks
// references in Go files this changeset never touched). Fires on wrongness.
//
// Precision: a reference is reported only if it is newly broken — it resolved at
// base, or it sits on a line this changeset added. Pre-existing unresolved
// references are not this change's doing.
type referencesResolve struct{}

func (referencesResolve) assess(name string, s seam, cs *changeset) []signal {
	if !cs.touchedAny(s.Go) && !cs.touchedAny(s.Cue) {
		return nil
	}
	now := cueLabels(cs.current(s.Cue))
	base := cueLabels(cs.atBase(s.Cue))
	var out []signal
	sources := cs.current(s.Go)
	for _, path := range sortedKeys(sources) {
		src := sources[path]
		var changed map[int]bool
		if cs.touchedAny([]string{path}) {
			changed = cs.file(path).Changed
		}
		for _, r := range goStringRefs(src) {
			if now[r.Name] {
				continue
			}
			if !base[r.Name] && !changed[r.Line] {
				continue // was already unresolved and this change didn't introduce it
			}
			out = append(out, signal{Kind: s.Kind, Seam: name, File: path, Level: "step-back",
				Message: fmt.Sprintf("Go references CUE path %q (cue.%s, line %d), which does not resolve in the CUE files. The binding is broken.",
					r.Name, r.Via, r.Line)})
		}
	}
	return out
}
