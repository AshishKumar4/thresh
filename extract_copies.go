package main

// Near copies over any language's functions read as copies are (a digest of each body's tokens and the body's
// winnowed fingerprints; extract_py.go reads Python's): which functions a diff writes, and which others each
// repeats. The comparison is the language's no more than the reading is the kind's.

import "sort"

type copyFunc struct {
	File   string
	Name   string
	Line   int
	Digest string
	Prints []int64
}

// copyTree is one side of a diff: its functions, and the files that could not be read.
type copyTree struct {
	Funcs  []copyFunc
	Unread map[string]bool
}

type copyPartner struct {
	Func    copyFunc
	Share   float64 // of the smaller's fingerprints, idioms aside
	Smaller string  // the function whose fingerprints the share is of
}

type copyMatch struct {
	Fresh    copyFunc      // a function the diff writes
	Partners []copyPartner // the functions it repeats, the closest first
}

// nearCopies finds, among head's functions in `touched` files, each one the diff writes anew and the other
// functions of head it repeats. A function is written anew where it held other tokens at base (by file, name
// and digest) and the tree holds more functions of its tokens than base did, so one renamed, moved (between
// any files, deleted or renamed ones too) or with only a constant, a name, a comment or a docstring changed
// is not, and one written as a copy is. A touched file one side cannot read is left out of both sides. Two
// functions repeat each other where the smaller's fingerprints, those `common` or fewer functions hold, are
// `threshold` or more shared, both with at least `fewest`. A function another's tokens repeat exactly is a
// duplicate checker's, not a near copy, and none of its partners is found. A pair the diff writes on both
// sides is found once.
func nearCopies(head, base copyTree, touched map[string]bool, threshold float64, common, fewest int) []copyMatch {
	left := func(f copyFunc) bool { return touched[f.File] && (head.Unread[f.File] || base.Unread[f.File]) }
	key := func(f copyFunc) string { return f.File + "\x00" + f.Name + "\x00" + f.Digest }
	held, counts := map[string]bool{}, map[string]int{}
	for _, f := range base.Funcs {
		if !left(f) {
			held[key(f)] = true
			counts[f.Digest]--
		}
	}
	var funcs []copyFunc
	for _, f := range head.Funcs {
		if !left(f) {
			funcs = append(funcs, f)
			counts[f.Digest]++
		}
	}
	fresh, digests := make([]bool, len(funcs)), map[string]int{}
	for _, f := range funcs {
		digests[f.Digest]++
	}
	holders := map[int64][]int{}
	for i, f := range funcs {
		fresh[i] = touched[f.File] && !held[key(f)] && counts[f.Digest] > 0 && digests[f.Digest] == 1
		for _, fp := range f.Prints {
			holders[fp] = append(holders[fp], i)
		}
	}
	read := make([]int, len(funcs))
	for i, f := range funcs {
		for _, fp := range f.Prints {
			if len(holders[fp]) <= common {
				read[i]++
			}
		}
	}
	var out []copyMatch
	for a, u := range funcs {
		if !fresh[a] || read[a] < fewest {
			continue
		}
		shared := map[int]int{}
		for _, fp := range u.Prints {
			if len(holders[fp]) > common {
				continue
			}
			for _, b := range holders[fp] {
				if b != a {
					shared[b]++
				}
			}
		}
		type found struct {
			at int
			copyPartner
		}
		var partners []found
		for b, count := range shared {
			v := funcs[b]
			if (fresh[b] && b < a) || min(read[a], read[b]) < fewest {
				continue
			}
			smaller := u.Name
			if read[b] < read[a] {
				smaller = v.Name
			}
			if share := float64(count) / float64(min(read[a], read[b])); share >= threshold {
				partners = append(partners, found{b, copyPartner{v, share, smaller}})
			}
		}
		if len(partners) == 0 {
			continue
		}
		sort.Slice(partners, func(i, j int) bool {
			if partners[i].Share != partners[j].Share {
				return partners[i].Share > partners[j].Share
			}
			return partners[i].at < partners[j].at
		})
		match := copyMatch{Fresh: u}
		for _, p := range partners {
			match.Partners = append(match.Partners, p.copyPartner)
		}
		out = append(out, match)
	}
	return out
}
