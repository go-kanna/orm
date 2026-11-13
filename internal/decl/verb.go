package decl

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Verb is what a method of a repository does, as the start of its name tells.
type Verb string

const (
	VerbGet    Verb = "Get"
	VerbFind   Verb = "Find"
	VerbFirst  Verb = "First"
	VerbList   Verb = "List"
	VerbCount  Verb = "Count"
	VerbExists Verb = "Exists"
	VerbInsert Verb = "Insert"
	VerbUpdate Verb = "Update"
	VerbTouch  Verb = "Touch"
	VerbUpsert Verb = "Upsert"
	VerbDelete Verb = "Delete"
	VerbCopy   Verb = "Copy"
	VerbSum    Verb = "Sum"
	VerbMin    Verb = "Min"
	VerbMax    Verb = "Max"
	VerbAvg    Verb = "Avg"
)

var verbs = []Verb{
	VerbGet, VerbFind, VerbFirst, VerbList, VerbCount, VerbExists, VerbInsert, VerbUpdate, VerbTouch, VerbUpsert,
	VerbDelete, VerbCopy, VerbSum, VerbMin, VerbMax, VerbAvg,
}

// VerbOf tells the verb name starts with: a verb, followed by nothing or by a word of its own, as in GetByNumber, but
// not in Getaway.
func VerbOf(name string) (Verb, bool) {
	for _, v := range verbs {
		rest, ok := strings.CutPrefix(name, string(v))
		if r, _ := utf8.DecodeRuneInString(rest); ok && (rest == "" || unicode.IsUpper(r) || unicode.IsDigit(r)) {
			return v, true
		}
	}
	return "", false
}

// Writes reports whether v writes rows.
func (v Verb) Writes() bool {
	return slices.Contains([]Verb{VerbInsert, VerbUpdate, VerbTouch, VerbUpsert, VerbDelete, VerbCopy}, v)
}
