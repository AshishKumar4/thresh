# thresh

Taps your shoulder when a changeset crosses a boundary that warrants stepping back.
It **notifies, never gates**, and every tap is a **fact about the diff** you can verify
by looking at it — never an opinion.

Rice's theorem says you can't decide anything interesting about what a program *means*.
thresh doesn't argue; it threshes. It separates signal from chaff by properties you can
actually check — a version tuple moved, a guard got bypassed, a reference stopped
resolving — and leaves judging the grain to you. (A threshold is just a boundary with a
name.)

It exists because an agent (small context) and the human supervising it (can't read
the firehose) both have to work narrowly. Thresh is externalized peripheral vision:
it watches the few boundaries in a repo whose crossing is *consequential*, and says so
at the moment you'd otherwise forget to look wider.

A tap fires for one of two reasons, and the label says which:

- **consequence** — the change is far-reaching *even if correct*. A clean, intended
  breaking change to a published contract still fires. No linter serves this cell.
- **wrongness** — the change is probably a mistake (a guard was bypassed).

## The model: a seam is `purpose` + `paths` + `kind`

```cue
// .thresh/thresh.cue  — data only; the schema is in the binary
package thresh

seams: lineages: {
	kind:    "published-language"
	purpose: "every lineage is a versioned contract its consumers bind to"
	paths:   ["exemplars/*.cue"]
	format:  "thema"
}
seams: bindlineage: {
	kind:    "choke-point"
	purpose: "BindLineage is where a lineage's validity is established"
	paths:   ["*.go"]
	type:    "baseLineage"
	onlyIn:  ["BindLineage"]
}
```

- **`purpose`** says why the seam exists, in your words. It's required, and `thresh explain`
  renders it under the seam, so the reason lives with the seam instead of in AGENTS.md
  prose that drifts from it.
- **`paths`** are plain globs. Deliberately dumb — precision is never the glob's job.
- **`kind`** is an analyzer from the catalog below. It decides what to look for in the
  files under its paths and whether to fire.

That's everything a user specifies. Seams are keyed by name so files compose by CUE
unification and taps carry a stable name. Reuse a path set the CUE way (`_go: [...]`).

## The catalog

The deciding distinction between the two *consequence* kinds is whether the consumers of
a thing are **enumerable**: if you can't name them you owe **compatibility**; if you can,
you owe **consultation**.

| kind | fires when | fires on |
|---|---|---|
| `published-language` | a versioned contract changes; its format says breaking (loud) or compatible (note). `format: "thema"` reads declared version tuples. `format: "golden"` treats recorded output (a whole file, or a txtar `section`) as the contract: a moved output is loud, a new one is a note, and with `headline: true` only a moved first line is loud. A covered file the format can read nothing from gets a note: the seam is misconfigured. | consequence |
| `shared-kernel` | co-owned code changes (meaning-bearing changes only); the tap names who to `consult`. `watch` restricts it to changed lines mentioning given strings. | consequence |
| `choke-point` | a guarded `type` is constructed (Go) or the dotted name `type` or one under it is used (Python: a call, an attribute, an absolute import, through the file's imports as Python scopes them, so a parameter or local of the same name is not the import) outside the functions in `onlyIn` (a default, a decorator or a class body runs where it is defined, not in the function or class; a function's annotations are its own) (globs; a Python method is `Class.method`), on changed lines. A Python `type` may be a glob (`a.b.*_trace`), matched against each dotted prefix of a use. | wrongness |
| `declared-set` | a Go or Python function matching `funcs`, a CUE definition (at any depth) matching `defs`, or an entry of the Python registry `entries` (the keys of a module-level dict, the elements of a set, list or tuple, each `NAME[k] = ...`) is added without being in `declared` (loud), or a declared one disappears (note). Bodies are not its concern. | wrongness |
| `near-copy` | a Python function the diff writes anew (its steps: not one renamed, moved between files, or with only a name, a constant, a comment or a docstring changed) and another function under `paths` share at least `containment` (0.9 by default) of the smaller one's winnowed fingerprints: each body read as tokens, every identifier one token and every constant its type, each 30 tokens in a row hashed and the least of each 16 hashes kept, a fingerprint more than 40 functions hold being an idiom and not read, functions under 60 syntax nodes aside. Identical tokens are a duplicate checker's, not a near copy. One tap a function, naming the closest. | wrongness |
| `references-resolve` | a Go literal reference into CUE (`cue.Str/Def/Hid`) no longer resolves — checked across *all* Go files, since a CUE rename breaks files you never touched. | wrongness |

Comment-only and whitespace-only edits never fire, for any kind. A Python file is compared by its syntax
tree without docstrings, so its indentation still counts and its comments and docstrings do not.

## Usage

```sh
go install .                 # from this checkout; puts `thresh` on PATH
thresh init               # scaffold .thresh/thresh.cue (every kind as an example)
thresh check              # uncommitted working tree vs HEAD (includes untracked files)
thresh check main..HEAD   # a commit range
thresh check --json
thresh check --session S --new   # only taps this session hasn't been told about (the delivery hook)
thresh explain            # render the seams as agent guidance; pipe into AGENTS.md
```

Output:

```
## Thresh
⚑ STEP BACK [published-language: lineages] exemplars/narrowing.cue  #3f9a2c1e7b04
    breaking change: max version [1,0] → [2,0] (major bump). Consumers you don't control depend on this contract.
· [shared-kernel: lineage-kernel] lineage.cue  #b81d07e45a2c
    co-owned kernel changed: #Lineage. Consult: every lineage author; the Go facade.
```

`check` exits 0 whether or not it found anything — signals are not failures. It exits
non-zero only when it *couldn't run* (invalid annotations, not a git repo).

## Connecting it to a repo and to Claude Code

1. **Annotate:** `thresh init`, keep the seams that apply. This is the real work and
   the only per-repo work. A repo with no `.thresh/` is simply ignored.
2. **Agent guidance:** `thresh explain > AGENTS.md` (or into a section of it). It is
   generated from the annotations, so it cannot drift from them.
3. **Automatic taps at end of turn:** install `hooks/run-thresh.sh` as a Stop hook —
   globally, once, in `~/.claude/settings.json` (instructions in the script). It is silent
   and free in any directory without `.thresh/`, never blocks, and surfaces a loud
   warning (never silence) if annotations are invalid.

4. **Taps to the agent:** the Stop hook's message reaches only you. Install
   `hooks/thresh-deliver.sh` for PostToolUse (matcher `Edit|Write|MultiEdit|NotebookEdit`)
   and UserPromptSubmit (instructions in the script). It hands the agent each tap, with its
   ID, as context: once per session, and again only if the tap's message changes.

Worktrees, cmux workspaces, other agents: nothing special. It's a CLI over the working
tree of whatever git repo you're in.

## The tap log: did a tap help?

Every tap carries an ID (`#05d3ff82b7c0` in text, `id` in `--json`), and every `check` that
taps something appends it to a per-user log: `$THRESH_LOG`, default
`$XDG_STATE_HOME/thresh/taps.jsonl` (`~/.local/state/...`). A tap's identity is the repo's
root commit, the base commit, kind, seam, file and level. The same crossing re-checked every
turn is one tap, even as its message drifts; a new base is a new tap. Each record also carries
provenance: the origin remote, checkout path, base/head, session and transcript (when the
hooks pass them), the seam's config hash, and the thresh revision. Events have unique IDs and
hosts, so logs from many machines merge by concatenation.

Once it's clear whether a tap was useful, whoever knows records a verdict:

```sh
thresh verdict 05d3ff --by origin --useful yes --evidence "moved the construction into Build"
thresh verdict 05d3ff --by sweep  --inaccurate  --evidence "Sneaky() constructs *other*, not guarded"
thresh pending [--repo DIR] [--json]    # taps nobody has given a verdict on
thresh stats [--json]                   # by kind, repo and seam: delivered, verdicted, yes/no/unclear, inaccurate, useful rate
```

- **yes**: the tap caused or confirmed something observable: the diff changed, a co-owner
  was consulted, a version or compatibility decision was revisited, or the crossing was
  explicitly confirmed as intended.
- **no**: true, but nothing followed and nothing needed to.
- **unclear**: can't be determined.
- **`--inaccurate`**: the tap misstated the diff. That's a bug report, so it's kept out of
  the useful rate.

`--by` is `origin` (the agent that was tapped), `sweep` (a later reviewer) or `human`. The
effective verdict is human, then origin, then sweep, and `stats` counts origin/sweep
disagreements. The protocol is included in every delivery to the agent and in
`thresh explain`. The `skills/thresh-sweep` Claude Code skill resolves whatever the origin
agent left pending, from the transcripts and the git history that followed.

Verdicts are judgments, and they never feed back into what `check` reports.

## Runtime requirements

`git`; `cue` (only to export annotations — the per-kind `#Seam` schema is embedded in the
binary and unified with your data at load, so bad kinds and typo'd fields are rejected
with a precise error); Go stdlib (`go/parser` for Go analysis; the target need not build);
`python3` for Python files (its stdlib `ast`, run on each file's text; `$THRESH_PYTHON` names
another; the target need not import or run). Without it, seams over Python files stay silent
and `check` says so once on stderr.

## Layout

```
main.go           CLI: check / explain / init; loads + validates annotations
kinds.go          the uniform contract every kind satisfies, and the changeset (from git)
extract_text.go   format-agnostic extractors: touched(), changed lines, watched substrings
extract_go.go     Go extractors (go/parser): funcs, constructions, literal CUE references
extract_py.go     Python extractors (the target's python3, stdlib ast; extract_py.py embedded):
                  funcs, dotted-name uses through imports, registry entries, the docstring-free AST
extract_cue.go    CUE extractors: definitions + spans, labels, version tuples (thema)
kind_*.go         one file per kind; each is extract → trivial decision
taplog.go         the tap log: tap identity, repo identity, events, delivery
review.go         verdict / pending / stats over the tap log; the verdict protocol
explain.go        the consultative surface
init.go           the scaffold template
schema/seam.cue   the annotation contract (embedded); one closed definition per kind
hooks/            the Stop hook (to you) and the delivery hook (to the agent)
skills/           the thresh-sweep skill
thresh_test.go    the scenarios: every kind, loud and silent, on a fresh fixture repo each
taplog_test.go    tap identity, logging, delivery, verdicts, pending, stats
testdata/fixture/ a small synthetic repo the scenarios mutate (needs only git; cue for one test)
```

Verify with `go test ./...`.

See `AGENTS.md` for the design constraints that govern changes to this tool.
