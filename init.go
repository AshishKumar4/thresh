package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// initRepo scaffolds .thresh/thresh.cue with every kind as a commented
// example. Onboarding a repo is: init, keep the seams that apply, then
// `thresh explain` to generate agent guidance.
func initRepo(repo string) error {
	dir := filepath.Join(repo, ".thresh")
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists; not overwriting", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "thresh.cue")
	if err := os.WriteFile(path, []byte(initTemplate), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\nNext: uncomment and edit the seams that apply, then run 'thresh explain' to generate agent guidance for AGENTS.md.\n", path)
	return nil
}

const initTemplate = `package thresh

// Thresh annotations for this repo. Data only: the #Seam schema lives in the
// thresh binary and validates this file at load (no import needed).
//
// A seam is a purpose, paths and a kind. purpose says why the seam exists, in
// your words: it is required, and thresh explain renders it, so it belongs here
// rather than in AGENTS.md prose. paths are plain globs; the kind decides what
// to look for. Every seam below is a commented example -- keep the ones that apply.
// Then run 'thresh explain' to render these as guidance for agents.

// published-language: a versioned contract for consumers you CANNOT enumerate.
// You owe them compatibility; a breaking change is loud even when correct.
// Needs a format with a compatibility checker (thema today).
// seams: "my-contract": {
// 	kind:    "published-language"
// 	purpose: "clients we don't ship decode these; a breaking change strands them"
// 	paths:   ["schemas/*.cue"]
// 	format:  "thema"
// }

// published-language over a golden corpus: the contract is recorded output, such
// as a checker's verdicts. section names the txtar section holding it (omit for
// the whole file); headline says its first line is the verdict, so a moved
// headline is loud and a reworded rest is a note.
// seams: "verdict-corpus": {
// 	kind:     "published-language"
// 	purpose:  "tools downstream match on these verdicts; a moved one misleads them"
// 	paths:    ["testdata/*.txtar"]
// 	format:   "golden"
// 	section:  "out"
// 	headline: true
// }

// shared-kernel: code co-owned by contexts you CAN name. You owe them consultation.
// seams: "core-types": {
// 	kind:    "shared-kernel"
// 	purpose: "the API and storage layers both build on these types"
// 	paths:   ["pkg/core/types.go"]
// 	consult: ["the API layer", "the storage layer"]
// }

// shared-kernel with watch: only changed lines mentioning these count.
// seams: "db-driver": {
// 	kind:    "shared-kernel"
// 	purpose: "every query is written against this driver's behavior"
// 	paths:   ["go.mod"]
// 	watch:   ["github.com/jackc/pgx"]
// 	consult: ["every query in pkg/storage"]
// }

// choke-point: all construction of a type goes through the named functions.
// seams: "connections": {
// 	kind:    "choke-point"
// 	purpose: "Open sets the timeouts and TLS config every connection needs"
// 	paths:   ["*.go"]
// 	type:    "conn"
// 	onlyIn:  ["Open"]
// }

// declared-set: an intentional, enumerated set of functions. An undeclared
// addition is flagged; a declared one that disappears is noted.
// seams: "validators": {
// 	kind:    "declared-set"
// 	purpose: "validation belongs in the schema; each Go check is an exception"
// 	paths:   ["validate.go"]
// 	funcs:   "check*"
// 	declared: {
// 		checkShape: "why this rule is enforced here and nowhere else"
// 	}
// }

// choke-point in Python: every use of a dotted name goes through the named
// functions (methods as "Class.method", globs allowed).
// seams: "profiler": {
// 	kind:    "choke-point"
// 	purpose: "Profiler owns the trace's lifetime and where it is written"
// 	paths:   ["src/**/*.py", "src/*/*.py"]
// 	type:    "jax.profiler"
// 	onlyIn:  ["Profiler.*"]
// }

// declared-set over a Python registry: the keys of a module-level dict (or the
// elements of a set, list or tuple), each one an intentional entry.
// seams: "routes": {
// 	kind:    "declared-set"
// 	purpose: "each route is a contract a client was promised"
// 	paths:   ["app/routes.py"]
// 	entries: "ROUTES"
// 	declared: {
// 		"/health": "why this route exists"
// 	}
// }

// declared-set over CUE definitions (at any depth), instead of Go functions.
// seams: "kernel-defs": {
// 	kind:    "declared-set"
// 	purpose: "definitions close everything that flows through them"
// 	paths:   ["schema/*.cue"]
// 	defs:    "*#*" // hidden definitions are named _#X
// 	declared: {
// 		"#Root": "why this must be a definition, not a regular field"
// 	}
// }

// near-copy: one sequence of steps has one owner. A Python function a diff
// writes that shares at least containment of its winnowed fingerprints with
// another function under paths is noted.
// seams: "copies": {
// 	kind:        "near-copy"
// 	purpose:     "a sequence of steps written twice drifts apart"
// 	paths:       ["src/*.py", "src/*/*.py", "tests/*.py"]
// 	containment: 0.9
// }

// references-resolve: Go reaches into CUE by literal path (cue.Str/Def/Hid);
// every such reference must resolve in the CUE files.
// seams: "cue-go-binding": {
// 	kind:    "references-resolve"
// 	purpose: "the Go side reads the CUE by literal path"
// 	go:      ["*.go"]
// 	cue:     ["*.cue"]
// }
`
