package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// --- The uniform contract every kind satisfies ---------------------------------
//
// Kinds receive a changeset (files with before/after content and changed line
// numbers) and return signals. They never touch git or output. This is the whole
// shared interface; all variability lives in the extractors (extract_*.go).

type changedFile struct {
	Path    string
	Before  string       // content at base; "" if the file is new
	After   string       // content now;    "" if the file was deleted
	Changed map[int]bool // line numbers added/modified in After
}

type signal struct {
	ID      string `json:"id"`      // the tap's identity in the tap log (taplog.go); never set by a kind
	Kind    string `json:"kind"`    // the catalog kind
	Seam    string `json:"seam"`    // the seam's stable name (map key)
	File    string `json:"file"`    // the file that tripped it ("" if seam-wide)
	Level   string `json:"level"`   // step-back | note
	Message string `json:"message"` // the shoulder-tap: a fact about the diff
}

// seam is the union of every per-kind field. The embedded CUE schema (a per-kind
// disjunction) enforces which fields apply to which kind, so Go just reads them.
type seam struct {
	Kind     string            `json:"kind"`
	Purpose  string            `json:"purpose"` // every kind: why the seam exists
	Paths    []string          `json:"paths"`
	Format   string            `json:"format"`   // published-language: compat extractor
	Section  string            `json:"section"`  // published-language golden: txtar section holding the output
	Headline bool              `json:"headline"` // published-language golden: first line of the output is its headline
	Isolated bool              `json:"isolated"` // published-language golden: the section is a contract alone; an edit elsewhere in its file does not touch it
	Consult  []string          `json:"consult"`  // shared-kernel: co-owners to consult
	Watch    []string          `json:"watch"`    // shared-kernel: only changed lines containing these
	Added    bool              `json:"added"`    // shared-kernel: only added lines count; a removed use leaves the dependency
	Type     string            `json:"type"`     // choke-point: the guarded type
	OnlyIn   []string          `json:"onlyIn"`   // choke-point: functions allowed to construct it
	Funcs    string            `json:"funcs"`    // declared-set: glob on Go or Python function name
	Defs     string            `json:"defs"`     // declared-set: glob on CUE definition name
	Entries  string            `json:"entries"`  // declared-set: a Python registry's name; its keys or elements
	Declared map[string]string `json:"declared"` // declared-set: name -> reason it is Go-side
	Go       []string          `json:"go"`       // references-resolve: Go files
	Cue      []string          `json:"cue"`      // references-resolve: CUE files
}

type config struct {
	Seams map[string]seam `json:"seams"`
}

// guardedSite is a choke point's site, as each language's extractor reads it:
// what is done there ("constructs guarded type", "uses guarded name"), to which
// name, in which function ("" at module or package level), on which line.
type guardedSite struct {
	What string
	Name string
	Func string
	Line int
}

type kind interface {
	assess(name string, s seam, cs *changeset) []signal
}

var kinds = map[string]kind{
	"published-language": publishedLanguage{},
	"shared-kernel":      sharedKernel{},
	"choke-point":        chokePoint{},
	"declared-set":       declaredSet{},
	"references-resolve": referencesResolve{},
}

// --- Changeset: the tool's view of the diff, produced from git ------------------

type changeset struct {
	repo     string
	base     string // ref for "before": HEAD in working-tree mode, A in range mode
	head     string // ref for "after":  "" in working-tree mode (read disk), B in range mode
	diffspec string // what to pass to `git diff`
	baseSHA  string // base resolved to a commit
	headSHA  string // head resolved to a commit; "" in working-tree mode
	worktree bool
	changed  []string
	cache    map[string]*changedFile
}

func newChangeset(repo, rng string) (*changeset, error) {
	cs := &changeset{repo: repo, cache: map[string]*changedFile{}}
	if rng == "" {
		cs.worktree, cs.base, cs.diffspec = true, "HEAD", "HEAD"
	} else {
		parts := strings.SplitN(rng, "..", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("range must be A..B, got %q", rng)
		}
		cs.base, cs.head, cs.diffspec = parts[0], parts[1], rng
	}
	out, err := cs.git("diff", "--name-only", cs.diffspec)
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %v", err)
	}
	seen := map[string]bool{}
	add := func(s string) {
		for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
			if l != "" && !seen[l] {
				seen[l] = true
				cs.changed = append(cs.changed, l)
			}
		}
	}
	add(out)
	if cs.worktree { // `git diff` omits untracked new files
		u, _ := cs.git("ls-files", "--others", "--exclude-standard")
		add(u)
	}
	if cs.baseSHA, err = cs.resolve(cs.base); err != nil {
		return nil, err
	}
	if !cs.worktree {
		if cs.headSHA, err = cs.resolve(cs.head); err != nil {
			return nil, err
		}
	}
	return cs, nil
}

func (cs *changeset) resolve(ref string) (string, error) {
	out, err := cs.git("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("cannot resolve %q to a commit", ref)
	}
	return strings.TrimSpace(out), nil
}

func (cs *changeset) git(args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", cs.repo}, args...)...).Output()
	return string(out), err
}

func matchAny(globs []string, path string) bool {
	for _, g := range globs {
		if ok, _ := filepath.Match(g, path); ok {
			return true
		}
	}
	return false
}

// under returns the changed files matching any of the globs, fully loaded.
func (cs *changeset) under(globs []string) []changedFile {
	var out []changedFile
	for _, p := range cs.changed {
		if matchAny(globs, p) {
			out = append(out, cs.file(p))
		}
	}
	return out
}

// file loads one changed file's before/after/changed-lines (memoized).
func (cs *changeset) file(path string) changedFile {
	if f, ok := cs.cache[path]; ok {
		return *f
	}
	f := &changedFile{Path: path}
	f.Before, _ = cs.git("show", cs.base+":"+path)
	if cs.worktree {
		b, _ := os.ReadFile(filepath.Join(cs.repo, path))
		f.After = string(b)
	} else {
		f.After, _ = cs.git("show", cs.head+":"+path)
	}
	if f.Before == "" {
		f.Changed = allLines(f.After) // new file: every line is new
	} else {
		f.Changed = addedLines(cs, path)
	}
	cs.cache[path] = f
	return *f
}

// current returns the CURRENT content of every repo file matching the globs,
// changed or not. Needed by kinds that resolve references across files.
func (cs *changeset) current(globs []string) map[string]string {
	return cs.contentAt(globs, "")
}

// atBase returns the BASE content of every file matching the globs.
func (cs *changeset) atBase(globs []string) map[string]string {
	return cs.contentAt(globs, cs.base)
}

func (cs *changeset) contentAt(globs []string, ref string) map[string]string {
	out := map[string]string{}
	var list string
	if ref == "" && cs.worktree {
		t, _ := cs.git("ls-files")
		u, _ := cs.git("ls-files", "--others", "--exclude-standard")
		list = t + "\n" + u
	} else {
		if ref == "" {
			ref = cs.head
		}
		list, _ = cs.git("ls-tree", "-r", "--name-only", ref)
	}
	for _, p := range strings.Split(strings.TrimSpace(list), "\n") {
		if p == "" || !matchAny(globs, p) {
			continue
		}
		if ref == "" && cs.worktree {
			b, _ := os.ReadFile(filepath.Join(cs.repo, p))
			out[p] = string(b)
		} else {
			out[p], _ = cs.git("show", ref+":"+p)
		}
	}
	return out
}

// touchedAny reports whether any changed file matches the globs.
func (cs *changeset) touchedAny(globs []string) bool {
	for _, p := range cs.changed {
		if matchAny(globs, p) {
			return true
		}
	}
	return false
}

func allLines(s string) map[int]bool {
	m := map[int]bool{}
	for i, n := 1, strings.Count(s, "\n")+1; i <= n; i++ {
		m[i] = true
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
