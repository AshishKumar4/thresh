package main

// Format-agnostic extractors: work on any text file.

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	lineCommentRE  = regexp.MustCompile(`//.*$`)
	blockCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
	hunkRE         = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)
)

// stripNoise removes comments and normalizes whitespace so that two versions of
// a file compare equal iff their meaning-bearing content is the same. Covers the
// `//` and `/* */` syntaxes shared by Go and CUE. (A `//` inside a string literal
// is also stripped; acceptable for a did-meaning-change heuristic.)
func stripNoise(s string) string {
	s = blockCommentRE.ReplaceAllString(s, "")
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = lineCommentRE.ReplaceAllString(l, "")
		l = strings.Join(strings.Fields(l), " ")
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// touched reports whether a file's meaning-bearing content changed. This is the
// shared precision lever: comment-only and whitespace-only edits are not touches,
// for every kind that uses it. A Python file compares its syntax trees without
// docstrings (`pyTouched`), since its indentation is meaning; where either side
// does not parse, its text without `#` comments.
func touched(f changedFile) bool {
	if isPython(f.Path) {
		if changed, ok := pyTouched(f.Before, f.After); ok {
			return changed
		}
		return stripHashNoise(f.Before) != stripHashNoise(f.After)
	}
	return stripNoise(f.Before) != stripNoise(f.After)
}

var hashCommentRE = regexp.MustCompile(`#.*$`)

// stripHashNoise is stripNoise for `#` comments (a `#` inside a string literal is
// also stripped; the same heuristic).
func stripHashNoise(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = hashCommentRE.ReplaceAllString(l, "")
		l = strings.Join(strings.Fields(l), " ")
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// addedLines returns the line numbers added/modified on the new side of the diff.
func addedLines(cs *changeset, path string) map[int]bool {
	out, _ := cs.git("diff", "-U0", cs.diffspec, "--", path)
	added := map[int]bool{}
	for _, line := range strings.Split(out, "\n") {
		m := hunkRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		start, _ := strconv.Atoi(m[1])
		count := 1
		if m[2] != "" {
			count, _ = strconv.Atoi(m[2])
		}
		for i := 0; i < count; i++ {
			added[start+i] = true
		}
	}
	return added
}

// changedLines returns the content of added and removed lines for a file.
func changedLines(cs *changeset, path string) []string {
	out, _ := cs.git("diff", cs.diffspec, "--", path)
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l == "" || strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") {
			continue
		}
		if l[0] == '+' || l[0] == '-' {
			lines = append(lines, l[1:])
		}
	}
	return lines
}

// addedLines returns the lines the changeset adds to path, without their marker.
func addedLines(cs *changeset, path string) []string {
	out, _ := cs.git("diff", cs.diffspec, "--", path)
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
			lines = append(lines, l[1:])
		}
	}
	return lines
}

// watched returns the changed lines containing any of the substrings.
func watched(lines, subs []string) []string {
	var hits []string
	seen := map[string]bool{}
	for _, l := range lines {
		for _, s := range subs {
			if strings.Contains(l, s) {
				t := strings.TrimSpace(l)
				if !seen[t] {
					seen[t] = true
					hits = append(hits, t)
				}
				break
			}
		}
	}
	return hits
}
