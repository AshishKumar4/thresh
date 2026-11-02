package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tap log is a side channel: these tests drive the check path end to end
// (minus cue: config comes from fixtureSeams) with THRESH_LOG pointed at a temp
// file, so they never touch the real log.

func TestTapIdentity(t *testing.T) {
	logTo(t)
	dir := fixture(t)

	replace(t, dir, "kernel.cue", "\tname: string\n", "\tname: string\n\tdescription?: string\n")
	first := tapFor(t, runJSON(t, dir, options{}), "kernel")
	again := tapFor(t, runJSON(t, dir, options{}), "kernel")
	if first.ID == "" || first.ID != again.ID {
		t.Fatalf("same change checked twice: IDs %q vs %q", first.ID, again.ID)
	}

	// The message drifts as the change grows; the tap is the same tap.
	replace(t, dir, "kernel.cue", "\tto:   string\n", "\tto:   string\n\tvia?: string\n")
	drifted := tapFor(t, runJSON(t, dir, options{}), "kernel")
	if drifted.Message == first.Message {
		t.Fatalf("expected the message to change, both: %q", first.Message)
	}
	if drifted.ID != first.ID {
		t.Fatalf("message drift changed the ID: %q -> %q", first.ID, drifted.ID)
	}

	// Committing something else moves the base: the still-uncommitted crossing is a new tap.
	write(t, dir, "notes.txt", "unrelated\n")
	git(t, dir, "add", "notes.txt")
	git(t, dir, "commit", "-qm", "unrelated")
	rebased := tapFor(t, runJSON(t, dir, options{}), "kernel")
	if rebased.ID == first.ID {
		t.Fatalf("base moved but the ID did not: %q", rebased.ID)
	}
}

func TestTapIdentityLevel(t *testing.T) {
	logTo(t)
	dir := fixture(t)
	replace(t, dir, "schemas/contract.cue",
		"{version: [1, 0], schema: {b: int}},",
		"{version: [1, 0], schema: {b: int}},\n\t\t{version: [1, 1], schema: {b: int, d?: string}},")
	note := tapFor(t, runJSON(t, dir, options{}), "lineages")
	replace(t, dir, "schemas/contract.cue", "{version: [1, 1],", "{version: [2, 0],")
	loud := tapFor(t, runJSON(t, dir, options{}), "lineages")
	if note.Level == loud.Level {
		t.Fatalf("expected different levels, both %q", note.Level)
	}
	if note.ID == loud.ID {
		t.Fatalf("a different level kept the ID %q", note.ID)
	}
}

func TestRepoIdentityAcrossWorktrees(t *testing.T) {
	dir := fixture(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, dir, "worktree", "add", "-q", wt)
	git(t, dir, "remote", "add", "origin", "git@github.com:Example/Fixture.git")
	a, err := (&changeset{repo: dir}).identify()
	if err != nil {
		t.Fatal(err)
	}
	b, err := (&changeset{repo: wt}).identify()
	if err != nil {
		t.Fatal(err)
	}
	if a.Root == "" || a.Root != b.Root {
		t.Fatalf("root differs across worktrees: %q vs %q", a.Root, b.Root)
	}
	if a.Toplevel == b.Toplevel {
		t.Fatalf("expected distinct toplevels, both %q", a.Toplevel)
	}
	if a.Remote != "github.com/example/fixture" || b.Remote != a.Remote {
		t.Fatalf("remote labels: %q, %q", a.Remote, b.Remote)
	}
}

func TestNormalizeRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:Owner/Name.git":          "github.com/owner/name",
		"https://github.com/owner/name":          "github.com/owner/name",
		"https://user@github.com/owner/name.git": "github.com/owner/name",
		"ssh://git@github.com:22/owner/name.git": "github.com/owner/name",
		"":                                       "",
	} {
		if got := normalizeRemote(in); got != want {
			t.Errorf("normalizeRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTapLogging(t *testing.T) {
	path := logTo(t)
	dir := fixture(t)
	write(t, dir, "sneaky.go", "package fixture\n\nfunc Sneaky() *guarded { return &guarded{} }\n")
	sigs := runJSON(t, dir, options{session: "s1", transcript: "/tmp/s1.jsonl"})
	if len(sigs) == 0 {
		t.Fatal("expected taps")
	}
	evs := mustRead(t, path)
	if len(evs) != len(sigs) {
		t.Fatalf("expected one sighting per tap, got %d events for %d taps", len(evs), len(sigs))
	}
	e := evs[0]
	if e.Ev != "sighting" || e.Tap != sigs[0].ID || e.Repo == nil || len(e.Repo.Root) != 40 ||
		len(e.Base) != 40 || e.Head != "" || e.SeamHash == "" || e.Session != "s1" ||
		e.Transcript != "/tmp/s1.jsonl" || e.EID == "" || e.At.IsZero() || e.Message != sigs[0].Message {
		t.Fatalf("sighting lacks provenance: %+v", e)
	}
}

func TestTapLoggingSilence(t *testing.T) {
	path := logTo(t)
	dir := fixture(t)
	appendTo(t, dir, "kernel.cue", "\n// just a comment\n")
	if sigs := runJSON(t, dir, options{session: "s1"}); len(sigs) != 0 {
		t.Fatalf("expected silence, got %v", sigs)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a silent run wrote the log (stat err %v)", err)
	}
}

func TestTapLogFailureNeverSuppresses(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	write(t, filepath.Dir(blocker), "file", "not a directory")
	t.Setenv("THRESH_LOG", filepath.Join(blocker, "taps.jsonl"))
	dir := fixture(t)
	write(t, dir, "sneaky.go", "package fixture\n\nfunc Sneaky() *guarded { return &guarded{} }\n")

	for _, o := range []options{{}, {session: "s1", deliver: true}} {
		stdout, stderr, err := run(t, dir, o)
		if err != nil {
			t.Fatalf("check failed because the log did: %v", err)
		}
		if !strings.Contains(stdout, "[choke-point: build]") || !strings.Contains(stdout, "#") {
			t.Errorf("taps missing (deliver=%v):\n%s", o.deliver, stdout)
		}
		if !strings.Contains(stderr, "tap log") {
			t.Errorf("expected a warning about the log (deliver=%v), got %q", o.deliver, stderr)
		}
	}
}

func TestDelivery(t *testing.T) {
	logTo(t)
	dir := fixture(t)
	replace(t, dir, "kernel.cue", "\tname: string\n", "\tname: string\n\tdescription?: string\n")

	s1 := options{session: "s1", deliver: true}
	out := mustRun(t, dir, s1)
	if !strings.Contains(out, "[shared-kernel: kernel]") || !strings.Contains(out, "Recording whether a tap helped") {
		t.Fatalf("first delivery lacks the tap or the protocol:\n%s", out)
	}
	if out := mustRun(t, dir, s1); out != "" {
		t.Fatalf("redelivered an unchanged tap:\n%s", out)
	}

	replace(t, dir, "kernel.cue", "\tto:   string\n", "\tto:   string\n\tvia?: string\n")
	if out := mustRun(t, dir, s1); !strings.Contains(out, "#Lens") {
		t.Fatalf("a changed message was not redelivered:\n%s", out)
	}
	if out := mustRun(t, dir, s1); out != "" {
		t.Fatalf("redelivered after the change was delivered:\n%s", out)
	}
	if out := mustRun(t, dir, options{session: "s2", deliver: true}); !strings.Contains(out, "[shared-kernel: kernel]") {
		t.Fatalf("another session was not told:\n%s", out)
	}

	if _, _, err := run(t, dir, options{deliver: true}); err == nil {
		t.Fatal("--new without --session should be refused")
	}
}

// TestDeliverByAnotherRoute: taps a review's brief carries are recorded delivered to
// its session, and counted delivered in the stats, as check --new's are; an unknown
// tap, or no session, records nothing.
func TestDeliverByAnotherRoute(t *testing.T) {
	path := logTo(t)
	seed(t, path, sighting("abc111", "r", "choke-point"), sighting("abc222", "r", "shared-kernel"))
	if _, err := deliverTaps(path, "", "", []string{"abc111"}); err == nil {
		t.Fatal("a delivery with no session should be refused")
	}
	if _, err := deliverTaps(path, "review:train-1", "", []string{"abc111", "fff"}); err == nil {
		t.Fatal("an unknown tap should refuse the delivery")
	}
	if got := fold(mustRead(t, path))["abc111"].delivered; len(got) != 0 {
		t.Fatalf("a refused delivery recorded %v", got)
	}
	ids, err := deliverTaps(path, "review:train-1", "brief-1.md", []string{"#abc1", "abc222"})
	if err != nil || strings.Join(ids, ",") != "abc111,abc222" {
		t.Fatalf("delivered %v, %v", ids, err)
	}
	taps := fold(mustRead(t, path))
	if taps["abc111"].delivered["review:train-1"] != msgHash(taps["abc111"].Message) {
		t.Fatalf("delivery not at the tap's message: %v", taps["abc111"].delivered)
	}
	for _, r := range computeStats(taps).ByKind {
		if r.Delivered != r.Taps {
			t.Fatalf("stats count %d of %d taps delivered for %s", r.Delivered, r.Taps, r.Group)
		}
	}
}

func TestVerdict(t *testing.T) {
	path := logTo(t)
	seed(t, path, sighting("abc111", "r", "choke-point"), sighting("abc222", "r", "choke-point"))

	for name, v := range map[string]verdictArgs{
		"no recorder":         {tap: "abc111", useful: "yes", evidence: "e"},
		"bad recorder":        {tap: "abc111", by: "me", useful: "yes", evidence: "e"},
		"neither":             {tap: "abc111", by: "origin", evidence: "e"},
		"both":                {tap: "abc111", by: "origin", useful: "yes", inaccurate: true, evidence: "e"},
		"bad useful":          {tap: "abc111", by: "origin", useful: "maybe", evidence: "e"},
		"no evidence":         {tap: "abc111", by: "origin", useful: "yes", evidence: " "},
		"unknown tap":         {tap: "fff", by: "origin", useful: "yes", evidence: "e"},
		"ambiguous prefix":    {tap: "abc", by: "origin", useful: "yes", evidence: "e"},
		"delivered-only taps": {tap: "ddd", by: "origin", useful: "yes", evidence: "e"},
	} {
		if _, err := recordVerdict(path, v); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}

	id, err := recordVerdict(path, verdictArgs{tap: "#abc1", by: "origin", useful: "yes", evidence: "reverted the bump"})
	if err != nil || id != "abc111" {
		t.Fatalf("prefix resolution: %q, %v", id, err)
	}
	if _, err := recordVerdict(path, verdictArgs{tap: "abc111", by: "origin", useful: "no", evidence: "on reflection"}); err != nil {
		t.Fatal(err)
	}
	v := fold(mustRead(t, path))["abc111"].verdicts["origin"]
	if v.Useful != "no" || v.Accurate == nil || !*v.Accurate {
		t.Fatalf("latest verdict per recorder should win: %+v", v)
	}
}

func TestPending(t *testing.T) {
	path := logTo(t)
	seed(t, path,
		sighting("yes111", "r1", "choke-point"), verdict("yes111", "origin", "yes"),
		sighting("unc111", "r1", "choke-point"), verdict("unc111", "sweep", "unclear"),
		sighting("pnd111", "r1", "choke-point"),
		sighting("pnd222", "r2", "shared-kernel"),
		event{Ev: "delivered", Tap: "nosight", Session: "s"},
	)
	taps := fold(mustRead(t, path))
	if got := ids(pending(taps, "")); got != "pnd111,pnd222" {
		t.Fatalf("pending = %s", got)
	}
	if got := ids(pending(taps, "r2")); got != "pnd222" {
		t.Fatalf("pending in r2 = %s", got)
	}
}

func TestStats(t *testing.T) {
	path := logTo(t)
	inaccurate := verdict("t3", "sweep", "")
	f := false
	inaccurate.Accurate = &f
	seed(t, path,
		sighting("t1", "r1", "choke-point"), verdict("t1", "origin", "yes"), verdict("t1", "sweep", "no"),
		sighting("t2", "r1", "choke-point"), verdict("t2", "origin", "yes"), verdict("t2", "human", "no"),
		sighting("t3", "r1", "choke-point"), inaccurate,
		sighting("t4", "r1", "choke-point"), event{Ev: "delivered", Tap: "t4", Session: "s", MsgHash: "h"},
		sighting("t5", "r2", "shared-kernel"),
	)
	st := computeStats(fold(mustRead(t, path)))
	cp := st.ByKind[0]
	if cp.Group != "choke-point" || cp.Taps != 4 || cp.Delivered != 1 || cp.Verdicted != 3 ||
		cp.Yes != 1 || cp.No != 1 || cp.Inaccurate != 1 || cp.Disagree != 1 {
		t.Fatalf("choke-point row: %+v", cp)
	}
	if cp.UsefulRate == nil || *cp.UsefulRate != 0.5 {
		t.Fatalf("useful rate should exclude the inaccurate tap: %v", cp.UsefulRate)
	}
	sk := st.ByKind[1]
	if sk.Group != "shared-kernel" || sk.Verdicted != 0 || sk.UsefulRate != nil {
		t.Fatalf("shared-kernel row: %+v", sk)
	}
	if len(st.ByRepo) != 2 || st.ByRepo[0].Group != "root:r1" || len(st.BySeam) != 2 {
		t.Fatalf("repo/seam rows: %+v / %+v", st.ByRepo, st.BySeam)
	}
}

// --- helpers --------------------------------------------------------------------

func logTo(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "taps.jsonl")
	t.Setenv("THRESH_LOG", p)
	return p
}

// run drives the check path over the working tree (vs HEAD) with the fixture seams.
func run(t *testing.T, dir string, o options) (string, string, error) {
	t.Helper()
	o.repo = dir
	var stdout, stderr bytes.Buffer
	err := runCheck(o, config{Seams: fixtureSeams}, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func mustRun(t *testing.T, dir string, o options) string {
	t.Helper()
	out, _, err := run(t, dir, o)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func runJSON(t *testing.T, dir string, o options) []signal {
	t.Helper()
	o.asJSON = true
	var sigs []signal
	if err := json.Unmarshal([]byte(mustRun(t, dir, o)), &sigs); err != nil {
		t.Fatal(err)
	}
	return sigs
}

func tapFor(t *testing.T, sigs []signal, seam string) signal {
	t.Helper()
	for _, s := range sigs {
		if s.Seam == seam {
			return s
		}
	}
	t.Fatalf("no tap for seam %q in %+v", seam, sigs)
	return signal{}
}

func mustRead(t *testing.T, path string) []event {
	t.Helper()
	evs, err := readEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

var seedClock = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func seed(t *testing.T, path string, evs ...event) {
	t.Helper()
	for i := range evs {
		evs[i].At = seedClock.Add(time.Duration(i) * time.Minute)
	}
	if err := appendEvents(path, evs); err != nil {
		t.Fatal(err)
	}
}

func sighting(tap, root, kind string) event {
	return event{Ev: "sighting", Tap: tap, Repo: &repoIdent{Root: root}, Kind: kind, Seam: kind + "-seam", Level: "step-back", Message: "m"}
}

func verdict(tap, by, useful string) event {
	accurate := true
	return event{Ev: "verdict", Tap: tap, By: by, Useful: useful, Accurate: &accurate, Evidence: "e"}
}

func ids(ts []*tapState) string {
	var out []string
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return strings.Join(out, ",")
}
