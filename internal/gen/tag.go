package gen

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// fieldTag is what the orm tag of a field says.
type fieldTag struct {
	// column is the column the field holds, when the tag names one.
	column string
	// skip says the field is no column: kanna-orm neither reads nor writes it.
	skip bool
	// fk is the foreign key a relation follows, when more than one could serve.
	fk       string
	nullzero bool
}

// parseTag reads the orm key of a struct tag. A tag that reflect cannot read is an error rather than no tag, since
// the field would otherwise go its own way without a word.
func parseTag(tag string) (fieldTag, error) {
	value, ok := reflect.StructTag(tag).Lookup("orm")
	if !ok {
		if strings.Contains(tag, "orm:") {
			return fieldTag{}, errors.New(`the struct tag is not one reflect can read: write it as orm:"name"`)
		}
		return fieldTag{}, nil
	}
	if name, option, ok := strings.Cut(value, ","); ok && option == "nullzero" && name != "-" &&
		!strings.ContainsAny(name, ",= ") {
		return fieldTag{column: name, nullzero: true}, nil
	}
	switch fk, isFK := strings.CutPrefix(value, "fk="); {
	case value == "-":
		return fieldTag{skip: true}, nil
	case isFK && fk != "" && !strings.ContainsAny(fk, ",= "):
		return fieldTag{fk: fk}, nil
	case value != "" && !strings.ContainsAny(value, ",= "):
		return fieldTag{column: value}, nil
	}
	return fieldTag{}, fmt.Errorf(`orm:%q is neither a column, -, nor fk=<column>`, value)
}
