package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureSeams mirrors testdata/fixture/.thresh/thresh.cue. Kinds are exercised
// directly through the uniform contract, so these tests need git but not cue.
var fixtureSeams = map[string]seam{
	"lineages":       {Kind: "published-language", Paths: []string{"schemas/*.cue"}, Format: "thema"},
	"verdicts":       {Kind: "published-language", Paths: []string{"corpus/*.txtar"}, Format: "golden", Section: "out", Headline: true},
	"kernel":         {Kind: "shared-kernel", Paths: []string{"kernel.cue"}, Consult: []string{"every consumer of #Kernel", "the Go binding"}},
	"cue-module":     {Kind: "shared-kernel", Paths: []string{"cue.mod/module.cue"}},
	"cue-dependency": {Kind: "shared-kernel", Paths: []string{"go.mod"}, Watch: []string{"cuelang.org/go"}},
	"cue-uses":       {Kind: "shared-kernel", Paths: []string{"go.mod"}, Watch: []string{"cuelang.org/go"}, Added: true},
	"verdict-out":    {Kind: "published-language", Paths: []string{"corpus/*.txtar"}, Format: "golden", Section: "out", Isolated: true},
	"build":          {Kind: "choke-point", Paths: []string{"*.go"}, Type: "guarded", OnlyIn: []string{"Build"}},
	"checks":         {Kind: "declared-set", Paths: []string{"bind.go"}, Funcs: "check*", Declared: map[string]string{"checkOne": "first", "checkTwo": "second"}},
	"shapes":         {Kind: "declared-set", Paths: []string{"shapes.cue"}, Defs: "*#*", Declared: map[string]string{"#Shape": "shape", "#Edge": "edge"}},
	"binding":        {Kind: "references-resolve", Go: []string{"*.go"}, Cue: []string{"*.cue"}},
	"profiler":       {Kind: "choke-point", Paths: []string{"pkg/*.py"}, Type: "jax.profiler", OnlyIn: []string{"Profiler.*"}},
	"traces":         {Kind: "choke-point", Paths: []string{"pkg/*.py"}, Type: "jax.profiler.*_trace", OnlyIn: []string{"Profiler.*"}},
	"cells":          {Kind: "declared-set", Paths: []string{"pkg/cells.py"}, Entries: "CELLS", Declared: map[string]string{"decode": "decode", "prefill": "prefill"}},
	"cell-funcs":     {Kind: "declared-set", Paths: []string{"pkg/cells.py"}, Funcs: "cell_*", Declared: map[string]string{"cell_decode": "decode", "cell_prefill": "prefill"}},
	"routes":         {Kind: "declared-set", Paths: []string{"pkg/cells.py"}, Entries: "ROUTES", Declared: map[string]string{"(wgmma, forward)": "the port"}},
	"copies":         {Kind: "near-copy", Paths: []string{"pkg/*.py"}, Containment: 0.9},
}

// Every kind has at least one loud scenario and one silence scenario. The
// silence scenarios are not optional: a kind you can't picture staying quiet is
// one nobody would leave switched on. Each scenario runs on a fresh fixture repo.
// TestMain keeps every test's readings of copies in a directory of the run's own, never the user's cache.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "thresh-cache-")
	if err != nil {
		panic(err)
	}
	os.Setenv("THRESH_CACHE", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestScenarios(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, dir string)
		want   []string // substrings of the rendered signals; empty => expect silence
	}{
		{
			"comment-only edit to the kernel is silent",
			func(t *testing.T, dir string) { appendTo(t, dir, "kernel.cue", "\n// just a comment\n") },
			nil,
		},
		{
			"breaking major bump is loud",
			func(t *testing.T, dir string) {
				replace(t, dir, "schemas/contract.cue",
					"{version: [1, 0], schema: {b: int}},",
					"{version: [1, 0], schema: {b: int}},\n\t\t{version: [2, 0], schema: {c: bool}},")
			},
			[]string{"[published-language: lineages]", "breaking change", "[1,0] → [2,0]"},
		},
		{
			"compatible minor is a note",
			func(t *testing.T, dir string) {
				replace(t, dir, "schemas/contract.cue",
					"{version: [1, 0], schema: {b: int}},",
					"{version: [1, 0], schema: {b: int}},\n\t\t{version: [1, 1], schema: {b: int, d?: string}},")
			},
			[]string{"[published-language: lineages]", "compatible change", "note"},
		},
		{
			"comment-only edit to the contract is silent",
			func(t *testing.T, dir string) { appendTo(t, dir, "schemas/contract.cue", "\n// nothing to see\n") },
			nil,
		},
		{
			"a contract file the format reads no versions from is noted as a misconfiguration",
			func(t *testing.T, dir string) {
				write(t, dir, "schemas/notes.cue", "package fixture\n\nnotes: \"no versions here\"\n")
			},
			[]string{"[published-language: lineages] schemas/notes.cue", "found no versions", "note"},
		},
		{
			"a moved recorded headline is loud",
			func(t *testing.T, dir string) {
				replace(t, dir, "corpus/tightened.txtar", "-- out --\nincompatible\n", "-- out --\ncompatible\n")
			},
			[]string{"[published-language: verdicts]", "step-back", `headline moved: "incompatible" → "compatible"`},
		},
		{
			"reworded output under an unchanged headline is a note",
			func(t *testing.T, dir string) {
				replace(t, dir, "corpus/tightened.txtar", "(rule narrowed)", "(rule disjunction-narrowed)")
			},
			[]string{"[published-language: verdicts]", "note", `reworded; headline "incompatible" unchanged`},
		},
		{
			"an input-only edit with the same recorded output is a note",
			func(t *testing.T, dir string) {
				replace(t, dir, "corpus/tightened.txtar", "s: \"a\" | \"b\"", "s: \"a\" | \"c\"")
			},
			[]string{"[published-language: verdicts]", "note", "recorded output unchanged"},
		},
		{
			"a new recorded output is a note that the contract grew",
			func(t *testing.T, dir string) {
				write(t, dir, "corpus/widened.txtar", "-- old.cue --\ns: \"a\"\n-- new.cue --\ns: string\n-- out --\ncompatible\n")
			},
			[]string{"[published-language: verdicts] corpus/widened.txtar", "note", `contract grew: new recorded output (headline "compatible")`},
		},
		{
			"a removed recorded output is loud",
			func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "corpus", "tightened.txtar")); err != nil {
					t.Fatal(err)
				}
			},
			[]string{"[published-language: verdicts] corpus/tightened.txtar", "step-back", "recorded output removed"},
		},
		{
			"a golden file without the declared section is noted as a misconfiguration",
			func(t *testing.T, dir string) {
				write(t, dir, "corpus/broken.txtar", "-- old.cue --\ns: string\n-- output --\ncompatible\n")
			},
			[]string{"[published-language: verdicts] corpus/broken.txtar", "note", `no "out" section`},
		},
		{
			"whitespace-only edit to a golden file is silent",
			func(t *testing.T, dir string) {
				replace(t, dir, "corpus/tightened.txtar", "s: string\n", "s:   string\n")
			},
			nil,
		},
		{
			"construction outside the choke point is loud",
			func(t *testing.T, dir string) {
				write(t, dir, "sneaky.go", "package fixture\n\nfunc Sneaky() *guarded { return &guarded{} }\n")
			},
			[]string{"[choke-point: build]", "Sneaky()", "guarded"},
		},
		{
			"construction inside the sanctioned function is silent",
			func(t *testing.T, dir string) {
				// parse-only fixture: a construction inside a function named Build
				write(t, dir, "sanctioned.go", "package fixture\n\nfunc Build() *guarded { return &guarded{v: 1} }\n")
			},
			nil,
		},
		{
			"type switch and assertion are not construction",
			func(t *testing.T, dir string) {
				write(t, dir, "kindof.go", "package fixture\n\nfunc kindOf(x any) string {\n\tswitch x.(type) {\n\tcase *guarded:\n\t\treturn \"g\"\n\t}\n\t_, _ = x.(*guarded)\n\treturn \"\"\n}\n")
			},
			nil,
		},
		{
			"dependency bump and cue.mod change are both loud",
			func(t *testing.T, dir string) {
				replace(t, dir, "go.mod", "cuelang.org/go v0.5.0", "cuelang.org/go v0.11.0")
				appendTo(t, dir, "cue.mod/module.cue", "language: version: \"v0.14.0\"\n")
			},
			[]string{"[shared-kernel: cue-dependency]", "cuelang.org/go v0.11.0", "[shared-kernel: cue-module]"},
		},
		{
			"unrelated dependency is silent",
			func(t *testing.T, dir string) {
				replace(t, dir, "go.mod", "\tcuelang.org/go v0.5.0\n", "\tcuelang.org/go v0.5.0\n\texample.com/other v1.0.0\n")
			},
			nil,
		},
		{
			"field added to the kernel names the definition and the co-owners",
			func(t *testing.T, dir string) {
				replace(t, dir, "kernel.cue", "\tname: string\n", "\tname: string\n\tdescription?: string\n")
			},
			[]string{"[shared-kernel: kernel]", "#Kernel", "Consult: every consumer of #Kernel; the Go binding"},
		},
		{
			"undeclared check added is loud",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "bind.go", "\nfunc (m *maybe) checkThree() error { return nil }\n")
			},
			[]string{"[declared-set: checks]", "checkThree"},
		},
		{
			"body edit of a declared check is silent",
			func(t *testing.T, dir string) {
				replace(t, dir, "bind.go", "func (m *maybe) checkOne() error { return nil }",
					"func (m *maybe) checkOne() error {\n\t_ = m\n\treturn nil\n}")
			},
			nil,
		},
		{
			"undeclared CUE definition added, nested, is loud",
			func(t *testing.T, dir string) {
				replace(t, dir, "shapes.cue", "\tname: string\n", "\tname: string\n\t#Helper: string\n")
			},
			[]string{"[declared-set: shapes]", "#Helper"},
		},
		{
			"undeclared hidden CUE definition added is loud",
			func(t *testing.T, dir string) { appendTo(t, dir, "shapes.cue", "\n_#Scratch: {}\n") },
			[]string{"[declared-set: shapes]", "_#Scratch"},
		},
		{
			"regular field added to a declared CUE definition is silent",
			func(t *testing.T, dir string) {
				replace(t, dir, "shapes.cue", "\tname: string\n", "\tname: string\n\tnote?: string\n")
			},
			nil,
		},
		{
			"declared CUE definition removed is a note",
			func(t *testing.T, dir string) {
				replace(t, dir, "shapes.cue", "\n#Edge: {\n\tfrom: string\n\tto:   string\n}\n", "\n")
			},
			[]string{"[declared-set: shapes]", "#Edge no longer exists", "note"},
		},
		{
			"declared check removed is a note",
			func(t *testing.T, dir string) {
				replace(t, dir, "bind.go", "func (m *maybe) checkTwo() error { return nil }\n", "")
			},
			[]string{"[declared-set: checks]", "checkTwo no longer exists", "note"},
		},
		{
			"a guarded Python name used outside the choke point is loud",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\ndef sneak(where):\n    jax.profiler.start_trace(where)\n")
			},
			[]string{"[choke-point: profiler] pkg/profile.py", "uses guarded name jax.profiler.start_trace in sneak() (line 19)"},
		},
		{
			"a guarded Python name reached through a from-import alias is loud",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\nfrom jax import profiler as p\n\n\ndef sneak():\n    p.stop_trace()\n")
			},
			[]string{"uses guarded name jax.profiler in module level (line 18)", "uses guarded name jax.profiler.stop_trace in sneak() (line 22)"},
		},
		{
			"a guarded Python name given as a glob matches each use under it",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\ndef sneak():\n    jax.profiler.stop_trace()\n")
			},
			[]string{"[choke-point: traces] pkg/profile.py", "uses guarded name jax.profiler.stop_trace in sneak()"},
		},
		{
			"a guarded Python name used inside the choke point is silent",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/profile.py", "        jax.profiler.stop_trace()",
					"        jax.profiler.stop_trace()\n        jax.profiler.start_trace(\"again\")")
			},
			nil,
		},
		{
			"comment- and docstring-only Python edits are silent",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/profile.py", "def step(state):\n", "def step(state):  # still pure\n    \"\"\"One step.\"\"\"\n")
				replace(t, dir, "pkg/profile.py", "\"\"\"A choke point in Python", "\"\"\"A choke point, in Python")
			},
			nil,
		},
		{
			"an indentation-only Python edit that moves a guarded use out of the choke point is loud",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/profile.py", "        self.stopped = True\n        jax.profiler.stop_trace()\n",
					"        self.stopped = True\njax.profiler.stop_trace()\n")
			},
			[]string{"uses guarded name jax.profiler.stop_trace in module level"},
		},
		{
			"an undeclared registry entry added is loud",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/cells.py", "    \"prefill\": \"the prefill cell\",\n", "    \"prefill\": \"the prefill cell\",\n    \"verify\": \"a new cell\",\n")
			},
			[]string{"[declared-set: cells] pkg/cells.py", "undeclared element added: verify (line 6)"},
		},
		{
			"an undeclared registry entry added by subscript is loud",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/cells.py", "\nCELLS[\"draft\"] = \"a drafted cell\"\n")
			},
			[]string{"undeclared element added: draft"},
		},
		{
			"a declared registry entry removed is a note",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/cells.py", "    \"decode\": \"the decode cell\",\n", "")
			},
			[]string{"[declared-set: cells]", "declared element decode no longer exists", "note"},
		},
		{
			"a registry value's edit is silent",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/cells.py", "\"the prefill cell\"", "\"the prefill cell, renamed\"")
			},
			nil,
		},
		{
			"an undeclared registry key that reads as a path is loud",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/cells.py", "\nCELLS[\"/admin\"] = \"an admin cell\"\n")
			},
			[]string{"[declared-set: cells]", "undeclared element added: /admin"},
		},
		{
			"a Python file that stops parsing claims no set change",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/cells.py", "\n\ndef broken(:\n    pass\n")
			},
			nil,
		},
		{
			"a parameter named as a guarded import is not the import",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\ndef unrelated(jax):\n    return jax.profiler\n\n\ndef listed():\n    return [jax.profiler for jax in range(3)]\n")
			},
			nil,
		},
		{
			"a module that rebinds an imported name no longer reads it as the import",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\njax = None\n\n\ndef later():\n    return jax.profiler\n")
			},
			nil,
		},
		{
			"a rebinding in an ordinary except handler unbinds an import",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\ntry:\n    pass\nexcept ValueError:\n    jax = None\n\n\ndef later():\n    return jax.profiler\n")
			},
			nil,
		},
		{
			"an optional import's fallback leaves the import",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\ntry:\n    import jax\nexcept ImportError:\n    jax = None\n\n\ndef later():\n    jax.profiler.stop_trace()\n")
			},
			[]string{"uses guarded name jax.profiler.stop_trace in later()"},
		},
		{
			"a fallback that rebinds a name its try does not import unbinds it",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\ntry:\n    import os\nexcept ImportError:\n    jax = None\n\n\ndef later():\n    return jax.profiler\n")
			},
			nil,
		},
		{
			"a module's global declaration binds nothing",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\nglobal tracing\nimport jax as tracing\n\n\ndef outside():\n    tracing.profiler.stop_trace()\n")
			},
			[]string{"uses guarded name jax.profiler.stop_trace in outside()"},
		},
		{
			"a class body's global declaration reads the module's import",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\ndef outer(jax):\n    class Capture:\n        global jax\n        trace = jax.profiler.stop_trace()\n    return Capture\n")
			},
			[]string{"uses guarded name jax.profiler.stop_trace in outer() (line 21)"},
		},
		{
			"a global declaration reads the module's import past an enclosing parameter",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/profile.py", "\n\ndef outer(jax):\n    def inner():\n        global jax\n        jax.profiler.stop_trace()\n    return inner\n")
			},
			[]string{"uses guarded name jax.profiler.stop_trace in outer.inner() (line 21)"},
		},
		{
			"an annotation naming a guarded name is its function's",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/profile.py", "    def stop(self):\n", "    def stop(self, options: jax.profiler.ProfileOptions = None) -> jax.profiler.TraceAnnotation:\n")
			},
			nil,
		},
		{
			"a default, a class keyword and a class body run where they are defined, outside the choke point",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/profile.py", "class Profiler:\n",
					"class Profiler(object, options=jax.profiler.ProfileOptions()):\n    capture = jax.profiler.trace\n\n"+
						"    def again(self, where=jax.profiler.TraceAnnotation(\"x\")):\n        return where\n\n")
			},
			[]string{"uses guarded name jax.profiler.ProfileOptions in module level (line 5)",
				"uses guarded name jax.profiler.trace in module level (line 6)",
				"uses guarded name jax.profiler.TraceAnnotation in module level (line 8)"},
		},
		{
			"an undeclared tuple-keyed registry entry is loud, named by its elements",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/cells.py", "    (\"wgmma\", \"forward\"): \"the port\",\n",
					"    (\"wgmma\", \"forward\"): \"the port\",\n    (\"mma\", None): \"an A100's\",\n")
			},
			[]string{"[declared-set: routes]", "undeclared element added: (mma, None)"},
		},
		{
			"an undeclared registry entry keyed by an object's constructor is loud, named as written",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/cells.py", "    (\"wgmma\", \"forward\"): \"the port\",\n",
					"    (\"wgmma\", \"forward\"): \"the port\",\n    (MMA,  Kind(True, made = Made.FORWARD)): \"an object's\",\n")
			},
			[]string{"[declared-set: routes]", "undeclared element added: (MMA, Kind(True, made=Made.FORWARD))"},
		},
		{
			"a constructed key whose arguments split differently is another key",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/cells.py", "    (\"wgmma\", \"forward\"): \"the port\",\n",
					"    (\"wgmma\", \"forward\"): \"the port\",\n    Kind(\"a, b\"): \"one\",\n")
				commitAll(t, dir, "one constructed key")
				replace(t, dir, "pkg/cells.py", "    Kind(\"a, b\"): \"one\",\n",
					"    Kind(\"a, b\"): \"one\",\n    Kind(\"a\", \"b\"): \"two\",\n")
			},
			[]string{"undeclared element added: Kind('a', 'b')"},
		},
		{
			"a constructed key is never the string that reads as it",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/cells.py", "    \"prefill\": \"the prefill cell\",\n",
					"    \"prefill\": \"the prefill cell\",\n    \"Kind(True)\": \"a string\",\n")
				commitAll(t, dir, "a string key")
				replace(t, dir, "pkg/cells.py", "    \"Kind(True)\": \"a string\",\n",
					"    \"Kind(True)\": \"a string\",\n    Kind(True): \"an object\",\n")
			},
			[]string{"[declared-set: cells]", "undeclared element added: Kind(True)"},
		},
		{
			"an empty string is a key like any other",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/cells.py", "    \"prefill\": \"the prefill cell\",\n",
					"    \"prefill\": \"the prefill cell\",\n    \"\": \"the empty cell\",\n")
			},
			[]string{"[declared-set: cells]", "undeclared element added:  (line"},
		},
		{
			"a registry entry thresh cannot name is loud, never dropped",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/cells.py", "    (\"wgmma\", \"forward\"): \"the port\",\n",
					"    (\"wgmma\", \"forward\"): \"the port\",\n    (\"mma\", made()[0]): \"computed\",\n")
			},
			[]string{"[declared-set: routes]", "cannot be named"},
		},
		{
			"an undeclared Python function in a declared set is loud",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/cells.py", "\n\ndef cell_verify():\n    return 3\n")
			},
			[]string{"[declared-set: cell-funcs]", "undeclared element added: cell_verify"},
		},
		{
			"a new function repeating another's steps is a note",
			func(t *testing.T, dir string) {
				write(t, dir, "pkg/report.py", "\"\"\"Reports.\"\"\"\n\n\ndef tally(lines, factor):\n    sums, peaks = [], []\n    for line in lines:\n        weighted = [item * factor for item in line]\n        sums.append(sum(weighted))\n        peaks.append(max(weighted) if weighted else 0.0)\n    bound = sum(sums) / max(len(sums), 1)\n    over = [at for at, value in enumerate(sums) if value < bound]\n    return {\"totals\": sums, \"largest\": peaks, \"past\": over}\n")
			},
			[]string{"[near-copy: copies] pkg/report.py note",
				"tally (line 4) and summarize_before (pkg/legacy.py:4) share 92% of summarize_before's winnowed fingerprints (those at most 40 functions hold)"},
		},
		{
			"a function edited into another's steps is a note",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/steps.py", "\n\ndef tally(lines):\n    return sorted(lines)\n")
				commitAll(t, dir, "a short tally")
				replace(t, dir, "pkg/steps.py", "def tally(lines):\n    return sorted(lines)\n", "def tally(lines, factor):\n    sums, peaks = [], []\n    for line in lines:\n        weighted = [item * factor for item in line]\n        sums.append(sum(weighted))\n        peaks.append(max(weighted) if weighted else 0.0)\n    bound = sum(sums) / max(len(sums), 1)\n    over = [at for at, value in enumerate(sums) if value < bound]\n    return {\"totals\": sums, \"largest\": peaks, \"past\": over}\n")
			},
			[]string{"[near-copy: copies] pkg/steps.py note", "tally (line 16) and summarize_before (pkg/legacy.py:4) share 92%"},
		},
		{
			"an identical copy is a duplicate checker's, not a near copy, near partners and all",
			func(t *testing.T, dir string) {
				write(t, dir, "pkg/report.py", "def tally(lines, factor):\n    sums, peaks = [], []\n    for line in lines:\n        weighted = [item * factor for item in line]\n        sums.append(sum(weighted))\n        peaks.append(max(weighted) if weighted else 0.0)\n    bound = sum(sums) / max(len(sums), 1)\n    over = [at for at, value in enumerate(sums) if value > bound]\n    return {\"totals\": sums, \"largest\": peaks, \"past\": over}\n")
			},
			nil,
		},
		{
			"a file renamed with its near copy in it is not written anew",
			func(t *testing.T, dir string) {
				git(t, dir, "mv", "pkg/legacy.py", "pkg/older.py")
			},
			nil,
		},
		{
			"repairing a file that did not parse claims no copy in it",
			func(t *testing.T, dir string) {
				appendTo(t, dir, "pkg/legacy.py", "\n\ndef broken(:\n    pass\n")
				commitAll(t, dir, "broken")
				replace(t, dir, "pkg/legacy.py", "\n\ndef broken(:\n    pass\n", "")
			},
			nil,
		},
		{
			"an edit that leaves a copy's steps as they were is silent",
			func(t *testing.T, dir string) {
				replace(t, dir, "pkg/steps.py", "def summarize(rows, scale):\n    \"\"\"Each row scaled,",
					"def summarize_rows(table, scale):\n    \"\"\"Each of a table's rows scaled,")
				replace(t, dir, "pkg/steps.py", "    for row in rows:\n        scaled = [value * scale for value in row]\n        totals.append(sum(scaled))\n        largest.append(max(scaled) if scaled else 0.0)\n    limit",
					"    for row in table:\n        scaled = [value * scale for value in row]\n        totals.append(sum(scaled))\n        largest.append(max(scaled) if scaled else -1.0)  # an empty row\n    limit")
			},
			nil,
		},
		{
			"a short function written twice is silent",
			func(t *testing.T, dir string) {
				write(t, dir, "pkg/report.py", "def first(rows):\n    return [row[0] for row in rows if row]\n\n\ndef head(lines):\n    return [line[0] for line in lines if line]\n")
			},
			nil,
		},
		{
			"a new function unlike any other is silent",
			func(t *testing.T, dir string) {
				write(t, dir, "pkg/report.py", "def spread(points, width):\n    \"\"\"Points binned by width, each bin's count and its centre.\"\"\"\n"+
					"    bins = {}\n    for point in points:\n        key = int(point // width)\n        bins.setdefault(key, []).append(point)\n"+
					"    centres = {key: (key + 0.5) * width for key in bins}\n    counts = sorted((len(members), key) for key, members in bins.items())\n"+
					"    while counts and counts[0][0] < 2:\n        counts.pop(0)\n    return [(centres[key], count) for count, key in counts], len(bins)\n")
			},
			nil,
		},
		{
			"renamed CUE label breaks Go references, including an untouched file",
			func(t *testing.T, dir string) {
				replace(t, dir, "kernel.cue", "\tlenses: [...#Lens]", "\tlensList: [...#Lens]")
			},
			[]string{"[references-resolve: binding]", `"lenses"`, "bind.go", "other.go", "[shared-kernel: kernel]"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := fixture(t)
			tc.mutate(t, dir)
			commitAll(t, dir, "change")
			got := check(t, dir)
			if len(tc.want) == 0 {
				if got != "" {
					t.Fatalf("expected silence, got:\n%s", got)
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
		})
	}
}

// TestNearCopyReadsAreKeptAcrossRuns: a source's reading as copies is kept by its content and the
// extractor's (copiesStore), so a later run reads only what changed: one whose Python cannot run taps
// alike from what an earlier run read (2026-11-09: each pre-push check of nine files read Dew's whole
// Python tree again, 24 s of its 26).
func TestNearCopyReadsAreKeptAcrossRuns(t *testing.T) {
	t.Setenv("THRESH_CACHE", t.TempDir())
	forget := func() { // a run of its own: nothing read in this process
		copiesMemoLock.Lock()
		copiesMemo = map[[32]byte]pyCopyFile{}
		copiesMemoLock.Unlock()
	}
	forget()
	dir := fixture(t)
	write(t, dir, "pkg/report.py", "\"\"\"Reports.\"\"\"\n\n\ndef tally(lines, factor):\n    sums, peaks = [], []\n    for line in lines:\n        weighted = [item * factor for item in line]\n        sums.append(sum(weighted))\n        peaks.append(max(weighted) if weighted else 0.0)\n    bound = sum(sums) / max(len(sums), 1)\n    over = [at for at, value in enumerate(sums) if value < bound]\n    return {\"totals\": sums, \"largest\": peaks, \"past\": over}\n")
	commitAll(t, dir, "change")
	first := check(t, dir)
	if !strings.Contains(first, "[near-copy: copies] pkg/report.py note") {
		t.Fatalf("the first run taps no near copy:\n%s", first)
	}
	// Another run, with no Python to read with.
	forget()
	t.Setenv("THRESH_PYTHON", "/bin/false")
	if again := check(t, dir); again != first {
		t.Fatalf("a later run taps otherwise:\n%s\nnot\n%s", again, first)
	}
}

// TestQuietSeams: an isolated golden section is silent where an edit elsewhere in
// its file leaves it unchanged, and loud as any where it moves; a watch on added
// lines is silent where the change only removes a use, and loud where it adds one.
func TestQuietSeams(t *testing.T) {
	scenarios := []struct {
		name          string
		mutate        func(t *testing.T, dir string)
		want, notWant []string
	}{
		{
			"an input-only edit leaves an isolated section silent",
			func(t *testing.T, dir string) {
				replace(t, dir, "corpus/tightened.txtar", "s: \"a\" | \"b\"", "s: \"a\" | \"c\"")
			},
			[]string{"[published-language: verdicts]"},
			[]string{"[published-language: verdict-out]"},
		},
		{
			"a moved recorded output is loud for an isolated section",
			func(t *testing.T, dir string) {
				replace(t, dir, "corpus/tightened.txtar", "-- out --\nincompatible\n", "-- out --\ncompatible\n")
			},
			[]string{"[published-language: verdict-out]", "recorded output changed"},
			nil,
		},
		{
			"a removed use is silent for a watch on added lines",
			func(t *testing.T, dir string) { replace(t, dir, "go.mod", "\tcuelang.org/go v0.5.0\n", "") },
			[]string{"[shared-kernel: cue-dependency]"},
			[]string{"[shared-kernel: cue-uses]"},
		},
		{
			"an added use is loud for a watch on added lines",
			func(t *testing.T, dir string) {
				replace(t, dir, "go.mod", "cuelang.org/go v0.5.0", "cuelang.org/go v0.11.0")
			},
			[]string{"[shared-kernel: cue-uses]", "cuelang.org/go v0.11.0"},
			nil,
		},
	}
	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			dir := fixture(t)
			tc.mutate(t, dir)
			commitAll(t, dir, "change")
			got := check(t, dir)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("unexpected %q in:\n%s", w, got)
				}
			}
		})
	}
}

// TestAnnotationsValidate exercises the embedded schema through loadConfig: the
// fixture annotations are accepted and match fixtureSeams; bad ones are rejected
// with a precise error. Needs cue on PATH.
func TestAnnotationsValidate(t *testing.T) {
	if _, err := exec.LookPath("cue"); err != nil {
		t.Skip("cue not on PATH")
	}
	dir := fixture(t)
	cfg, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range sortedKeys(fixtureSeams) {
		if _, ok := cfg.Seams[name]; !ok {
			t.Errorf("fixture .thresh/thresh.cue lacks seam %q declared in fixtureSeams", name)
		}
	}
	for _, name := range sortedKeys(cfg.Seams) {
		if _, ok := fixtureSeams[name]; !ok {
			t.Errorf("fixtureSeams lacks seam %q declared in fixture .thresh/thresh.cue", name)
		}
	}

	// An unknown kind fails the per-kind disjunction and is named in the error.
	bad := t.TempDir()
	write(t, bad, ".thresh/thresh.cue", "package thresh\nseams: oops: {kind: \"frobnicate\", purpose: \"p\", paths: [\"*.go\"]}\n")
	_, err = loadConfig(bad)
	if err == nil || !strings.Contains(err.Error(), "invalid .thresh annotations") || !strings.Contains(err.Error(), "frobnicate") {
		t.Fatalf("expected a validation error naming the bad kind, got: %v", err)
	}

	// A valid kind with a misspelled field is rejected because every kind's
	// definition is closed. CUE only reports the field once the kind matches a
	// branch, which is why this is a separate case from the unknown-kind one.
	typo := t.TempDir()
	write(t, typo, ".thresh/thresh.cue", "package thresh\nseams: oops: {kind: \"choke-point\", purpose: \"p\", paths: [\"*.go\"], type: \"x\", onlyIn: [\"f\"], onlyInn: [\"g\"]}\n")
	_, err = loadConfig(typo)
	if err == nil || !strings.Contains(err.Error(), "onlyInn") {
		t.Fatalf("expected a validation error naming the misspelled field, got: %v", err)
	}

	// Every seam must say why it exists: a missing or empty purpose is rejected.
	for name, body := range map[string]string{
		"missing purpose": `{kind: "shared-kernel", paths: ["x.go"]}`,
		"empty purpose":   `{kind: "shared-kernel", purpose: "", paths: ["x.go"]}`,
	} {
		bare := t.TempDir()
		write(t, bare, ".thresh/thresh.cue", "package thresh\nseams: oops: "+body+"\n")
		if _, err := loadConfig(bare); err == nil || !strings.Contains(err.Error(), "purpose") {
			t.Errorf("%s: expected a validation error naming purpose, got: %v", name, err)
		}
	}

	// explain renders each seam's purpose ahead of its obligation.
	var b strings.Builder
	explain(&b, cfg)
	if !strings.Contains(b.String(), "- **kernel** (`shared-kernel`): #Kernel is what both the CUE and Go sides stand on\n  kernel.cue is co-owned kernel.") {
		t.Errorf("explain lacks the kernel seam's purpose:\n%s", b.String())
	}
}

func TestNoSeamsNeverTouchesGit(t *testing.T) {
	dir := t.TempDir() // not a git repo, no .thresh/
	if cfg, err := loadConfig(dir); err != nil || len(cfg.Seams) != 0 {
		t.Fatalf("expected empty config without error, got %v, %v", cfg, err)
	}
}

func TestInitScaffold(t *testing.T) {
	dir := t.TempDir()
	if err := initRepo(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".thresh", "thresh.cue")); err != nil {
		t.Fatal(err)
	}
	if err := initRepo(dir); err == nil {
		t.Fatal("expected init to refuse to overwrite an existing .thresh/")
	}
}

// The near-copy seam's guidance states the tap's own predicate: the smaller one's
// fingerprints, the idiom filter, and exact copies left to a duplicate checker.
func TestNearCopyExplainStatesItsPredicate(t *testing.T) {
	got := obligation(fixtureSeams["copies"])
	for _, want := range []string{"write anew", "at least 90% of the smaller one's winnowed fingerprints", "(those at most 40 functions hold)", "An exact copy of another is a duplicate checker's"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

// --- helpers --------------------------------------------------------------------

// fixture copies testdata/fixture into a fresh temp git repo and commits it.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src, err := filepath.Abs(filepath.Join("testdata", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		dst := filepath.Join(dir, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "thresh@test")
	git(t, dir, "config", "user.name", "thresh test")
	git(t, dir, "config", "commit.gpgsign", "false")
	commitAll(t, dir, "base")
	return dir
}

// check runs every fixture seam against HEAD~1..HEAD and renders the signals
// in a greppable form.
func check(t *testing.T, dir string) string {
	t.Helper()
	cs, err := newChangeset(dir, "HEAD~1..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, name := range sortedKeys(fixtureSeams) {
		s := fixtureSeams[name]
		for _, sig := range kinds[s.Kind].assess(name, s, cs) {
			b.WriteString("[" + sig.Kind + ": " + sig.Seam + "] " + sig.File + " " + sig.Level + ": " + sig.Message + "\n")
		}
	}
	return b.String()
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", msg)
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, dir, rel, s string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, rel, string(b)+s)
}

// replace substitutes old with new exactly once, failing loudly if old is absent
// so fixture drift is caught rather than silently producing a vacuous scenario.
func replace(t *testing.T, dir, rel, old, new string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s: fixture drift — %q not found", rel, old)
	}
	write(t, dir, rel, strings.Replace(string(b), old, new, 1))
}
