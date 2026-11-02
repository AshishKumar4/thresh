package thresh

// The thresh framework's annotation contract. Embedded in the binary and
// unified against each repo's .thresh/ data at load — repos supply data that
// conforms to it, they do not restate it. (The tool's #Seam is a published
// language; each repo's .thresh is a conformist.)
//
// A seam is paths + kind. Each kind is a closed definition, so a seam's fields
// are exactly those its kind takes; typos and misplaced fields are rejected.

seams: [string]: #Seam

// Why the seam exists: what crossing it puts at stake, in the author's words.
// Required, so the reason lives with the seam rather than in prose that drifts
// from it; thresh explain renders it under the seam.
#Purpose: string & !=""

#Seam: #PublishedLanguage | #SharedKernel | #ChokePoint | #DeclaredSet | #ReferencesResolve

// A contract for consumers you cannot enumerate. You owe them compatibility, so
// the tap is a compatibility verdict read from the format. Other formats
// (protobuf, openapi, ...) are to add.
#PublishedLanguage: {
	kind:    "published-language"
	purpose: #Purpose
	paths:   [...string]
	{
		format: "thema" // the version tuples thema makes authors declare
	} | {
		format:    "golden" // the contract is recorded output, e.g. a checker's verdict corpus
		section?:  string   // the txtar section holding the output; the whole file if absent
		headline?: bool     // the output's first line is its headline: a moved headline is loud, other changes a note
		isolated?: bool     // the section is a contract alone: an edit elsewhere in its file leaves it untouched, and silent
	}
}

// Code co-owned by contexts you can name. You owe them consultation.
#SharedKernel: {
	kind:     "shared-kernel"
	purpose:  #Purpose
	paths:    [...string]
	consult?: [...string] // who stands on this; repeated in the tap
	watch?:   [...string] // if set, only changed lines containing one of these count
	added?:   bool        // with watch: only lines the change adds count; a removed use leaves the dependency
}

// All construction of `type` goes through `onlyIn`. In a Python file, every use of
// the dotted name `type` (or a name under it) does. onlyIn entries are function
// names, globs allowed; a Python method is "Class.method".
#ChokePoint: {
	kind:    "choke-point"
	purpose: #Purpose
	paths:   [...string]
	type:    string
	onlyIn:  [...string]
}

// An intentional, enumerated set of code elements. Undeclared additions and
// stale declarations are flagged; element bodies are not this kind's concern.
// The elements are Go or Python functions, CUE definitions, or the entries of a
// Python registry: exactly one of funcs, defs or entries.
#DeclaredSet: {
	kind:     "declared-set"
	purpose:  #Purpose
	paths:    [...string]
	declared: [string]: string // name -> why it belongs in the set
	{
		funcs: string // glob on Go function name, e.g. "check*"; a Python method is "Class.method"
	} | {
		defs: string // glob on CUE definition name at any depth; hidden ones are _#X, so "*#*" covers both
	} | {
		entries: string // a module-level Python registry's name: the keys of its dict, or the elements of its set, list or tuple, or each NAME[k] = ...
	}
}

// Go reaches into CUE by literal path; fire when a reference no longer resolves.
#ReferencesResolve: {
	kind:    "references-resolve"
	purpose: #Purpose
	go:      [...string]
	cue:     [...string]
}
