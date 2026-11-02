package main

import "fmt"

// published-language: a contract for consumers you cannot enumerate. You owe them
// compatibility, so the tap is a compatibility verdict read from the format.
// Fires on consequence: a correct breaking change still fires.
//
// A file under the seam's paths that the format can read nothing from is a
// misconfigured seam, not a quiet one: it gets a note saying so.
type publishedLanguage struct{}

func (publishedLanguage) assess(name string, s seam, cs *changeset) []signal {
	var out []signal
	for _, f := range cs.under(s.Paths) {
		if !touched(f) {
			continue
		}
		var level, msg string
		switch s.Format {
		case "golden":
			level, msg = goldenChange(f, s)
		default:
			vb, err := versionTuples(f.Before, s.Format)
			if err != nil {
				return []signal{{Kind: s.Kind, Seam: name, File: f.Path, Level: "note", Message: err.Error()}}
			}
			va, _ := versionTuples(f.After, s.Format)
			level, msg = versionChange(f, s.Format, vb, va)
		}
		if msg != "" {
			out = append(out, signal{Kind: s.Kind, Seam: name, File: f.Path, Level: level, Message: msg})
		}
	}
	return out
}

func versionChange(f changedFile, format string, vb, va []ver) (string, string) {
	switch {
	case f.After == "":
		return "", "" // deleted
	case len(va) == 0:
		return "note", fmt.Sprintf("format %s found no versions in this file, so this seam cannot judge its compatibility. Check the seam's paths and format.", format)
	case maxMajor(va) > maxMajor(vb):
		return "step-back", fmt.Sprintf("breaking change: max version %s → %s (major bump). Consumers you don't control depend on this contract.",
			fmtVer(maxVer(vb)), fmtVer(maxVer(va)))
	case !sameVersions(vb, va):
		return "note", fmt.Sprintf("compatible change: versions %s → %s. The contract grew.", fmtVers(vb), fmtVers(va))
	}
	return "note", "contract touched with no version change. Confirm its meaning is unchanged."
}

func goldenChange(f changedFile, s seam) (string, string) {
	before, hadBefore := recordedOutput(f.Before, s.Section)
	after, hasAfter := recordedOutput(f.After, s.Section)
	hl := ""
	if s.Headline {
		hl = fmt.Sprintf(" (headline %q)", headline(after))
	}
	switch {
	case f.After == "":
		return "step-back", "recorded output removed. Consumers you don't control may depend on what it pinned."
	case !hasAfter:
		return "note", fmt.Sprintf("format golden found no %q section in this file, so this seam cannot judge it. Check the seam's paths and section.", s.Section)
	case f.Before == "" || !hadBefore:
		return "note", fmt.Sprintf("contract grew: new recorded output%s.", hl)
	case before == after && s.Isolated:
		return "", "" // the edit was elsewhere in the file: another contract's, not this one's
	case before == after:
		return "note", "contract touched; recorded output unchanged."
	case s.Headline && headline(before) != headline(after):
		return "step-back", fmt.Sprintf("recorded headline moved: %q → %q. Consumers you don't control depend on this contract.",
			headline(before), headline(after))
	case s.Headline:
		return "note", fmt.Sprintf("recorded output reworded; headline %q unchanged.", headline(after))
	}
	return "step-back", "recorded output changed. Consumers you don't control depend on this contract."
}
