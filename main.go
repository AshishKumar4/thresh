// Command thresh taps the shoulder about consequential boundaries a changeset
// crossed, driven by per-repo annotations under .thresh/. It notifies; it never
// gates. Every signal is a fact about the diff.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// options is every flag any command takes; each command reads the ones it needs.
type options struct {
	repo       string
	repoSet    bool
	pos        string // check: the range; verdict: the tap
	asJSON     bool
	session    string
	transcript string
	deliver    bool // check --new
	by         string
	useful     string
	evidence   string
	inaccurate bool
	taps       []string // deliver: every positional argument
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	o := options{repo: "."}
	for i, args := 0, os.Args[2:]; i < len(args); i++ {
		val := func() string {
			i++
			if i >= len(args) {
				usage()
			}
			return args[i]
		}
		switch args[i] {
		case "--repo":
			o.repo, o.repoSet = val(), true
		case "--json":
			o.asJSON = true
		case "--session":
			o.session = val()
		case "--transcript":
			o.transcript = val()
		case "--new":
			o.deliver = true
		case "--by":
			o.by = val()
		case "--useful":
			o.useful = val()
		case "--evidence":
			o.evidence = val()
		case "--inaccurate":
			o.inaccurate = true
		default:
			o.pos = args[i]
			o.taps = append(o.taps, args[i])
		}
	}

	switch cmd {
	case "init":
		if err := initRepo(o.repo); err != nil {
			fail(err)
		}
		return
	case "verdict", "pending", "stats", "deliver":
		if err := review(cmd, o, os.Stdout); err != nil {
			fail(err)
		}
		return
	case "check", "explain":
	default:
		usage()
	}

	cfg, err := loadConfig(o.repo)
	if err != nil {
		fail(err)
	}
	if len(cfg.Seams) == 0 {
		// Not a thresh repo, or nothing declared yet. Say so quietly and never
		// touch git, so a globally installed hook is silent and free everywhere else.
		switch {
		case cmd == "explain":
			fmt.Println("No seams declared. Run `thresh init` to scaffold .thresh/thresh.cue.")
		case o.deliver:
		case o.asJSON:
			fmt.Println("[]")
		default:
			fmt.Println("Thresh: no seams declared (.thresh/ absent or empty).")
		}
		return
	}
	if cmd == "explain" {
		explain(os.Stdout, cfg)
		return
	}
	if err := runCheck(o, cfg, os.Stdout, os.Stderr); err != nil {
		fail(err)
	}
}

// runCheck assesses every seam, logs the taps, and reports them. Logging is a
// side channel: if it fails, that is a warning on stderr and the taps still print.
// With --new it reports only taps this session hasn't been told about yet, for
// delivery to the agent.
func runCheck(o options, cfg config, stdout, stderr io.Writer) error {
	if o.deliver && o.session == "" {
		return fmt.Errorf("--new needs --session")
	}
	cs, err := newChangeset(o.repo, o.pos)
	if err != nil {
		return err
	}
	id, err := cs.identify()
	if err != nil {
		return err
	}
	var signals []signal
	for _, name := range sortedKeys(cfg.Seams) { // seams evaluate independently
		s := cfg.Seams[name]
		k, ok := kinds[s.Kind]
		if !ok {
			return fmt.Errorf("seam %q: unknown kind %q", name, s.Kind)
		}
		for _, sig := range k.assess(name, s, cs) {
			sig.ID = tapID(id.Root, cs.baseSHA, sig)
			signals = append(signals, sig)
		}
	}

	path, err := logPath()
	if err == nil {
		err = recordSightings(path, cfg, cs, id, signals, o.session, o.transcript)
	}
	if err != nil {
		fmt.Fprintf(stderr, "thresh: tap log not written: %v\n", err)
	}
	if o.deliver {
		if err == nil {
			signals, err = undelivered(path, o.session, signals)
		}
		if err != nil {
			fmt.Fprintf(stderr, "thresh: tap log unreadable, delivering every tap: %v\n", err)
		}
	}

	switch {
	case o.asJSON:
		if signals == nil {
			signals = []signal{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(signals)
	case o.deliver:
		if len(signals) > 0 {
			render(stdout, signals)
			fmt.Fprintln(stdout)
			fmt.Fprint(stdout, protocol)
		}
	default:
		render(stdout, signals)
	}
	return nil
}

// review runs the commands over the tap log.
func review(cmd string, o options, stdout io.Writer) error {
	path, err := logPath()
	if err != nil {
		return err
	}
	if cmd == "deliver" {
		ids, err := deliverTaps(path, o.session, o.transcript, o.taps)
		if err == nil {
			fmt.Fprintf(stdout, "delivered %d taps to %s\n", len(ids), o.session)
		}
		return err
	}
	if cmd == "verdict" {
		id, err := recordVerdict(path, verdictArgs{tap: o.pos, by: o.by, useful: o.useful,
			evidence: o.evidence, session: o.session, inaccurate: o.inaccurate})
		if err == nil {
			fmt.Fprintf(stdout, "recorded verdict on #%s\n", id)
		}
		return err
	}
	evs, err := readEvents(path)
	if err != nil {
		return err
	}
	taps := fold(evs)
	if cmd == "stats" {
		renderStats(stdout, computeStats(taps), o.asJSON)
		return nil
	}
	root := ""
	if o.repoSet {
		cs := &changeset{repo: o.repo}
		id, err := cs.identify()
		if err != nil {
			return err
		}
		root = id.Root
	}
	renderPending(stdout, pending(taps, root), o.asJSON)
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  thresh check [A..B] [--repo DIR] [--json]   tap the shoulder about boundaries the changeset crossed
                                                 (no range => uncommitted working tree vs HEAD)
  thresh explain [--repo DIR]                 render the seams as agent guidance (for AGENTS.md)
  thresh init [--repo DIR]                    scaffold .thresh/thresh.cue with every kind as an example

  check also takes --session ID --transcript PATH (provenance for the tap log) and
  --new (report only taps this session hasn't been told about; needs --session).

tap log ($THRESH_LOG, default $XDG_STATE_HOME/thresh/taps.jsonl):
  thresh verdict ID --by origin|sweep|human (--useful yes|no|unclear | --inaccurate) --evidence TEXT
                                              record whether a tap helped
  thresh pending [--repo DIR] [--json]        taps nobody has given a verdict on
  thresh deliver ID... --session ID [--transcript PATH]
                                              record that these taps reached a reader by another route
                                              (a review's brief), as check --new records its own
  thresh stats [--json]                       taps and verdicts by kind, repo and seam
`)
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "thresh: %v\n", err)
	os.Exit(1)
}

func render(w io.Writer, signals []signal) {
	if len(signals) == 0 {
		fmt.Fprintln(w, "Thresh: no consequential boundaries touched.")
		return
	}
	fmt.Fprintln(w, "## Thresh")
	for _, s := range signals {
		marker := "·"
		if s.Level == "step-back" {
			marker = "⚑ STEP BACK"
		}
		where := s.File
		if where == "" {
			where = "(seam-wide)"
		}
		fmt.Fprintf(w, "%s [%s: %s] %s  #%s\n    %s\n", marker, s.Kind, s.Seam, where, s.ID, s.Message)
	}
}

// loadConfig exports the repo's .thresh/*.cue in isolation (they are
// thresh's config, not part of the target's CUE module) unified with the
// embedded #Seam schema, which validates the data and single-sources the contract.
func loadConfig(repo string) (config, error) {
	files, _ := filepath.Glob(filepath.Join(repo, ".thresh", "*.cue"))
	if len(files) == 0 {
		return config{}, nil // no annotations: nothing consequential is declared
	}
	tmp, err := os.MkdirTemp("", "thresh-cfg-")
	if err != nil {
		return config{}, err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "zz_thresh_schema.cue"), []byte(seamSchema), 0o644); err != nil {
		return config{}, err
	}
	names := []string{"zz_thresh_schema.cue"}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return config{}, err
		}
		if err := os.WriteFile(filepath.Join(tmp, filepath.Base(f)), b, 0o644); err != nil {
			return config{}, err
		}
		names = append(names, filepath.Base(f))
	}
	cmd := exec.Command("cue", append([]string{"export", "--out", "json"}, names...)...)
	cmd.Dir = tmp
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return config{}, fmt.Errorf("invalid .thresh annotations:\n%s", stderr)
	}
	var cfg config
	if err := json.Unmarshal(out, &cfg); err != nil {
		return config{}, fmt.Errorf("parse annotations: %v", err)
	}
	return cfg, nil
}
