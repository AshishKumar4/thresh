package main

// Python extractors, via the stdlib `ast` of the target's own python3 (extract_py.py, embedded). The
// target need not import or run. One run a distinct source; its facts are memoized.

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed extract_py.py
var pyScript string

type pyFunc struct {
	Name string `json:"name"` // qualified within the file: "Class.method", "outer.inner"
	Line int    `json:"line"`
}

type pyUse struct {
	Name string `json:"name"` // the dotted name referenced, its root resolved through the file's imports
	Line int    `json:"line"`
	Func string `json:"func"` // the qualified function it sits in; "" at module level
}

type pyEntry struct {
	Key         string `json:"key"`
	Named       bool   `json:"named"`       // false: a key the program computes, which has no name
	Constructed bool   `json:"constructed"` // its key constructs an object, never the string that reads as it
	Line        int    `json:"line"`
}

type pyFile struct {
	OK         bool                 `json:"ok"`
	Funcs      []pyFunc             `json:"funcs"`
	Uses       []pyUse              `json:"uses"`
	Registries map[string][]pyEntry `json:"registries"`
	Normalized string               `json:"normalized"` // the AST without docstrings
}

var (
	pyMemo     = map[[32]byte]*pyFile{}
	pyMemoLock sync.Mutex
	pyWarned   sync.Once
)

func isPython(path string) bool { return strings.HasSuffix(path, ".py") }

// pyFacts parses one Python source. It returns nil where python3 cannot be run (warned once on stderr,
// as every kind then stays silent on Python files) and a fact set with OK false where the source does
// not parse. $THRESH_PYTHON names the interpreter; python3 on PATH by default.
func pyFacts(src string) *pyFile {
	key := sha256.Sum256([]byte(src))
	pyMemoLock.Lock()
	defer pyMemoLock.Unlock()
	if f, ok := pyMemo[key]; ok {
		return f
	}
	python := os.Getenv("THRESH_PYTHON")
	if python == "" {
		python = "python3"
	}
	cmd := exec.Command(python, "-c", pyScript)
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	var f pyFile
	if err == nil {
		err = json.Unmarshal(out, &f)
	}
	if err != nil {
		pyWarned.Do(func() {
			fmt.Fprintf(os.Stderr, "thresh: cannot run %s for Python files (%v); their seams stay silent\n", python, err)
		})
		return nil
	}
	pyMemo[key] = &f
	return &f
}

// pyCopy is one function as a copy is read: its body's digest and winnowed fingerprints (extract_py.py).
type pyCopy struct {
	Name   string  `json:"name"`
	Line   int     `json:"line"`
	Digest string  `json:"digest"`
	Prints []int64 `json:"prints"`
}

// pyCopyFile is one source's functions as copies are read, and whether it parsed.
type pyCopyFile struct {
	OK    bool     `json:"ok"`
	Funcs []pyCopy `json:"funcs"`
}

var (
	copiesMemo     = map[[32]byte]pyCopyFile{}
	copiesMemoLock sync.Mutex
)

// copiesStore is the directory a source's reading as copies is kept in across runs (`pyCopies`):
// $THRESH_CACHE's copies/, else the user's cache directory's thresh/copies/; "" where there is none,
// and nothing is kept. Each check is a process of its own, and a tree's thousands of Python files
// read again every run held a pre-push hook's check of nine files past its 20 s (24 s of 26 on Dew,
// 2026-11-09). An entry no run has read for a while is its keeper's to remove (by its mtime, which a
// read moves).
func copiesStore() string {
	if dir := os.Getenv("THRESH_CACHE"); dir != "" {
		return filepath.Join(dir, "copies")
	}
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "thresh", "copies")
	}
	return ""
}

// copiesKey is a source's key as copies are read: its content's and the extractor's, so an extractor
// that reads otherwise reads every source again.
func copiesKey(src string) [32]byte {
	h := sha256.New()
	h.Write([]byte(pyScript))
	h.Write([]byte{0})
	h.Write([]byte(src))
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

// kept reads the reading `copiesStore` keeps under key, its mtime moved to now; false where it keeps none.
func kept(store string, key [32]byte) (pyCopyFile, bool) {
	var f pyCopyFile
	if store == "" {
		return f, false
	}
	path := filepath.Join(store, hex.EncodeToString(key[:])+".json")
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &f) != nil {
		return f, false
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return f, true
}

// keep writes a reading to `copiesStore` under key, whole (a temporary file renamed), so a run reading it
// at once finds the old entry or the new; a write that fails keeps nothing.
func keep(store string, key [32]byte, f pyCopyFile) {
	if store == "" || os.MkdirAll(store, 0o755) != nil {
		return
	}
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	path := filepath.Join(store, hex.EncodeToString(key[:])+".json")
	temporary, err := os.CreateTemp(store, ".keep-*")
	if err != nil {
		return
	}
	_, err = temporary.Write(b)
	if closed := temporary.Close(); err == nil && closed == nil {
		err = os.Rename(temporary.Name(), path)
	}
	if err != nil {
		_ = os.Remove(temporary.Name())
	}
}

// pyCopies reads each source's functions as a copy is read: those this run or an earlier one read
// (`copiesStore`) from what they kept, the rest in a single python3 run (a tree's worth of sources is too
// many to start one interpreter each). nil where python3 cannot be run.
func pyCopies(srcs []string) []pyCopyFile {
	copiesMemoLock.Lock()
	defer copiesMemoLock.Unlock()
	store := copiesStore()
	keys := make([][32]byte, len(srcs))
	var unread []string
	var unreadKeys [][32]byte
	queued := map[[32]byte]bool{}
	for i, src := range srcs {
		keys[i] = copiesKey(src)
		if _, ok := copiesMemo[keys[i]]; ok || queued[keys[i]] {
			continue
		}
		if f, ok := kept(store, keys[i]); ok {
			copiesMemo[keys[i]] = f
			continue
		}
		queued[keys[i]] = true
		unread, unreadKeys = append(unread, src), append(unreadKeys, keys[i])
	}
	if len(unread) > 0 {
		python := os.Getenv("THRESH_PYTHON")
		if python == "" {
			python = "python3"
		}
		in, _ := json.Marshal(unread)
		cmd := exec.Command(python, "-c", pyScript, "copies")
		cmd.Stdin = strings.NewReader(string(in))
		out, err := cmd.Output()
		var read []pyCopyFile
		if err == nil {
			err = json.Unmarshal(out, &read)
		}
		if err == nil && len(read) != len(unread) {
			err = fmt.Errorf("read %d sources of %d", len(read), len(unread))
		}
		if err != nil {
			pyWarned.Do(func() {
				fmt.Fprintf(os.Stderr, "thresh: cannot run %s for Python files (%v); their seams stay silent\n", python, err)
			})
			return nil
		}
		for i, key := range unreadKeys {
			copiesMemo[key] = read[i]
			keep(store, key, read[i])
		}
	}
	out := make([]pyCopyFile, len(srcs))
	for i, key := range keys {
		out[i] = copiesMemo[key]
	}
	return out
}

// pyCopyTree reads a tree's Python files (path -> source) as copies are read; false where python3 cannot be run.
func pyCopyTree(files map[string]string) (copyTree, bool) {
	var paths, srcs []string
	for _, p := range sortedKeys(files) {
		if isPython(p) {
			paths, srcs = append(paths, p), append(srcs, files[p])
		}
	}
	read := pyCopies(srcs)
	if read == nil && len(srcs) > 0 {
		return copyTree{}, false
	}
	tree := copyTree{Unread: map[string]bool{}}
	for i, p := range paths {
		if !read[i].OK {
			tree.Unread[p] = true
		}
		for _, f := range read[i].Funcs {
			tree.Funcs = append(tree.Funcs, copyFunc{p, f.Name, f.Line, f.Digest, f.Prints})
		}
	}
	return tree, true
}

// pyUses lists the references to `name` or below it (`name.x`), with the function each sits in. `name`
// may be a glob ("a.b.tcgen05_*"), matched against each dotted prefix of a use.
func pyUses(src, name string) []pyUse {
	f := pyFacts(src)
	if f == nil || !f.OK {
		return nil
	}
	var out []pyUse
	for _, u := range f.Uses {
		parts := strings.Split(u.Name, ".")
		for i := range parts {
			prefix := strings.Join(parts[:i+1], ".")
			if ok, _ := filepath.Match(name, prefix); ok || prefix == name {
				out = append(out, u)
				break
			}
		}
	}
	return out
}

// pyGuardedSites is pyUses as choke-point sites.
func pyGuardedSites(src, name string) []guardedSite {
	var out []guardedSite
	for _, use := range pyUses(src, name) {
		out = append(out, guardedSite{"uses guarded name", use.Name, use.Func, use.Line})
	}
	return out
}

// pyFuncElements lists a Python source's functions and methods, by qualified name; false where it cannot be
// read (python3 cannot be run, or the source does not parse).
func pyFuncElements(src string) ([]element, bool) {
	f := pyFacts(src)
	if f == nil || !f.OK {
		return nil, false
	}
	var out []element
	for _, fn := range f.Funcs {
		out = append(out, element{name: fn.Name, line: fn.Line, named: true})
	}
	return out, true
}

// pyRegistryElements lists the keys or elements of a module-level registry named `name`; false where the
// source cannot be read.
func pyRegistryElements(name string) func(string) ([]element, bool) {
	return func(src string) ([]element, bool) {
		f := pyFacts(src)
		if f == nil || !f.OK {
			return nil, false
		}
		var out []element
		for _, e := range f.Registries[name] {
			out = append(out, element{name: e.Key, line: e.Line, named: e.Named, constructed: e.Constructed})
		}
		return out, true
	}
}

// pyTouched reports whether a Python file's meaning changed: its AST, without docstrings, differs.
// ok is false where either side cannot be parsed (the caller falls back to text).
func pyTouched(before, after string) (changed, ok bool) {
	b, a := pyFacts(before), pyFacts(after)
	if b == nil || a == nil || !b.OK || !a.OK {
		return false, false
	}
	return b.Normalized != a.Normalized, true
}
