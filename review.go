package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// The review surface over the tap log: recording verdicts, listing what is
// unresolved, and the numbers. Verdicts are judgments about taps, kept apart from
// the signal path on purpose: they never change what check reports.

// protocol is the verdict protocol, shown with every delivery and in explain.
const protocol = `### Recording whether a tap helped

Each tap carries an ID (` + "`#<id>`" + `). Once it is clear whether a tap was useful (not
before), record a verdict:

    thresh verdict <id> --by origin --useful yes|no|unclear --evidence "what happened"

Use ` + "`--by origin`" + ` if the tap was delivered to you; ` + "`--by sweep`" + ` if you are reviewing
the work afterwards.

- **yes**: the tap caused or confirmed something observable: the diff changed, a
  co-owner was consulted, a version or compatibility decision was revisited, or you
  explicitly checked that the crossing was intended.
- **no**: the tap was true, but nothing followed and nothing needed to.
- **unclear**: it can't be determined (e.g. the work was abandoned).
- If the tap misstated the diff, pass ` + "`--inaccurate`" + ` instead of ` + "`--useful`" + `: that is a
  thresh bug, not a judgment.

A verdict is a record, never a request: it does not change what thresh reports.
`

type verdictArgs struct {
	tap, by, useful, evidence, session string
	inaccurate                         bool
}

// recordVerdict validates and appends a verdict, returning the full tap ID.
func recordVerdict(path string, v verdictArgs) (string, error) {
	switch v.by {
	case "origin", "sweep", "human":
	default:
		return "", fmt.Errorf("--by must be origin, sweep or human")
	}
	if v.inaccurate == (v.useful != "") {
		return "", fmt.Errorf("give exactly one of --useful yes|no|unclear or --inaccurate")
	}
	switch v.useful {
	case "", "yes", "no", "unclear":
	default:
		return "", fmt.Errorf("--useful must be yes, no or unclear")
	}
	if strings.TrimSpace(v.evidence) == "" {
		return "", fmt.Errorf("--evidence is required: say what happened")
	}
	evs, err := readEvents(path)
	if err != nil {
		return "", err
	}
	id, err := resolveTap(fold(evs), v.tap)
	if err != nil {
		return "", err
	}
	accurate := !v.inaccurate
	return id, appendEvents(path, []event{{
		Ev: "verdict", Tap: id, By: v.by, Useful: v.useful, Accurate: &accurate,
		Evidence: v.evidence, Session: v.session,
	}})
}

// deliverTaps records each tap (a full ID or a unique prefix of a sighted tap) as
// delivered to session at its current message, as check --new records the taps it
// reports: for taps a reader is given by another route, such as a review's brief.
// It refuses an unknown or ambiguous tap and records none of them then.
func deliverTaps(path, session, transcript string, refs []string) ([]string, error) {
	if session == "" {
		return nil, fmt.Errorf("deliver needs --session: who the taps reached")
	}
	evs, err := readEvents(path)
	if err != nil {
		return nil, err
	}
	taps := fold(evs)
	var ids []string
	var marks []event
	for _, ref := range refs {
		id, err := resolveTap(taps, ref)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
		marks = append(marks, event{Ev: "delivered", Tap: id, Session: session, Transcript: transcript,
			MsgHash: msgHash(taps[id].Message)})
	}
	return ids, appendEvents(path, marks)
}

// resolveTap accepts a full ID or a unique prefix of a sighted tap.
func resolveTap(taps map[string]*tapState, ref string) (string, error) {
	ref = strings.TrimPrefix(ref, "#")
	if ref == "" {
		return "", fmt.Errorf("which tap? give its ID")
	}
	var hits []string
	for id, t := range taps {
		if t.Sightings > 0 && strings.HasPrefix(id, ref) {
			hits = append(hits, id)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no tap %q in the log", ref)
	case 1:
		return hits[0], nil
	}
	sort.Strings(hits)
	return "", fmt.Errorf("tap %q is ambiguous: %s", ref, strings.Join(hits, ", "))
}

// pending lists sighted taps nobody has given a verdict on, oldest first.
// root, if set, restricts it to one repo.
func pending(taps map[string]*tapState, root string) []*tapState {
	var out []*tapState
	for _, t := range taps {
		if t.Sightings == 0 || len(t.verdicts) > 0 || (root != "" && t.Repo.Root != root) {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FirstSeen.Before(out[j].FirstSeen) })
	return out
}

func renderPending(w io.Writer, ps []*tapState, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if ps == nil {
			ps = []*tapState{}
		}
		_ = enc.Encode(ps)
		return
	}
	if len(ps) == 0 {
		fmt.Fprintln(w, "No taps awaiting a verdict.")
		return
	}
	for _, t := range ps {
		fmt.Fprintf(w, "#%s [%s: %s] %s (%s)\n    %s\n", t.ID, t.Kind, t.Seam, t.File, t.Level, t.Message)
		fmt.Fprintf(w, "    repo %s at %s, base %s\n", repoLabel(t.Repo), t.Repo.Toplevel, short(t.Base))
		fmt.Fprintf(w, "    seen %s → %s (%d sightings), delivered to an agent: %v\n",
			t.FirstSeen.Format("2006-01-02 15:04"), t.LastSeen.Format("2006-01-02 15:04"), t.Sightings, len(t.delivered) > 0)
		for _, s := range t.Sessions {
			fmt.Fprintf(w, "    session %s: %s\n", s.Session, s.Transcript)
		}
	}
}

// --- stats ----------------------------------------------------------------------------

type statRow struct {
	Group      string   `json:"group"`
	Taps       int      `json:"taps"`
	Delivered  int      `json:"delivered"` // told to an agent at least once
	Verdicted  int      `json:"verdicted"`
	Yes        int      `json:"yes"`
	No         int      `json:"no"`
	Unclear    int      `json:"unclear"`
	Inaccurate int      `json:"inaccurate"`
	Disagree   int      `json:"disagree"`   // origin and sweep verdicts differ
	UsefulRate *float64 `json:"usefulRate"` // yes / (yes+no) over accurate verdicts; null if none
}

type stats struct {
	ByKind []statRow `json:"byKind"`
	ByRepo []statRow `json:"byRepo"`
	BySeam []statRow `json:"bySeam"` // repo/seam
}

func computeStats(taps map[string]*tapState) stats {
	byKind, byRepo, bySeam := map[string]*statRow{}, map[string]*statRow{}, map[string]*statRow{}
	row := func(m map[string]*statRow, k string) *statRow {
		if m[k] == nil {
			m[k] = &statRow{Group: k}
		}
		return m[k]
	}
	for _, t := range taps {
		if t.Sightings == 0 {
			continue
		}
		repo := repoLabel(t.Repo)
		for _, r := range []*statRow{row(byKind, t.Kind), row(byRepo, repo), row(bySeam, repo+"/"+t.Seam)} {
			r.Taps++
			if len(t.delivered) > 0 {
				r.Delivered++
			}
			if o, s := t.verdicts["origin"], t.verdicts["sweep"]; o.Ev != "" && s.Ev != "" && verdictOf(o) != verdictOf(s) {
				r.Disagree++
			}
			v, ok := t.effective()
			if !ok {
				continue
			}
			r.Verdicted++
			switch {
			case v.Accurate != nil && !*v.Accurate:
				r.Inaccurate++
			case v.Useful == "yes":
				r.Yes++
			case v.Useful == "no":
				r.No++
			case v.Useful == "unclear":
				r.Unclear++
			}
		}
	}
	flat := func(m map[string]*statRow) []statRow {
		out := []statRow{}
		for _, k := range sortedKeys(m) {
			r := *m[k]
			if r.Yes+r.No > 0 {
				rate := float64(r.Yes) / float64(r.Yes+r.No)
				r.UsefulRate = &rate
			}
			out = append(out, r)
		}
		return out
	}
	return stats{ByKind: flat(byKind), ByRepo: flat(byRepo), BySeam: flat(bySeam)}
}

func verdictOf(v event) string {
	if v.Accurate != nil && !*v.Accurate {
		return "inaccurate"
	}
	return v.Useful
}

func renderStats(w io.Writer, st stats, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(st)
		return
	}
	if len(st.ByKind) == 0 {
		fmt.Fprintln(w, "No taps logged yet.")
		return
	}
	for i, sec := range []struct {
		title string
		rows  []statRow
	}{{"kind", st.ByKind}, {"repo", st.ByRepo}, {"seam", st.BySeam}} {
		if i > 0 {
			fmt.Fprintln(w)
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintf(tw, "%s\ttaps\tdelivered\tverdicted\tyes\tno\tunclear\tinaccurate\tdisagree\tuseful\n", sec.title)
		for _, r := range sec.rows {
			useful := "-"
			if r.UsefulRate != nil {
				useful = fmt.Sprintf("%.0f%%", *r.UsefulRate*100)
			}
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%s\n", r.Group, r.Taps,
				pct(r.Delivered, r.Taps), pct(r.Verdicted, r.Taps), r.Yes, r.No, r.Unclear, r.Inaccurate, r.Disagree, useful)
		}
		tw.Flush()
	}
}

func pct(n, of int) string {
	if of == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", float64(n)/float64(of)*100)
}

func repoLabel(r repoIdent) string {
	if r.Remote != "" {
		return r.Remote
	}
	return "root:" + short(r.Root)
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
