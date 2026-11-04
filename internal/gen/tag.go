package gen

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// fieldTag is what the orm tag of a field says.
type fieldTag struct {
	// column is the column the field holds, when the tag names one.
	column string
	// skip says the field is no column: kanna-orm neither reads nor writes it.
	skip bool
	// fk names the columns of the foreign key a relation follows, when its names do not tell them.
	fk            []string
	nullzero      bool
	relation      string
	value         string
	insertDefault bool
	// insertOnly says the column is written by an insert alone, and left as it was by an update and by an upsert
	// meeting its row.
	insertOnly bool
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
	if strings.HasPrefix(value, "relation=") {
		return parseRelationTag(value)
	}
	if name, options, ok := strings.Cut(value, ","); ok && name != "-" &&
		!strings.ContainsAny(name, ",= ") {
		tag := fieldTag{column: name}
		for option := range strings.SplitSeq(options, ",") {
			switch {
			case option == "nullzero" && !tag.nullzero:
				tag.nullzero = true
			case option == "default" && !tag.insertDefault:
				tag.insertDefault = true
			case option == "insertonly" && !tag.insertOnly:
				tag.insertOnly = true
			default:
				return fieldTag{}, fmt.Errorf("unknown or duplicate orm option %q", option)
			}
		}
		return tag, nil
	}
	switch fk, isFK := strings.CutPrefix(value, "fk="); {
	case value == "-":
		return fieldTag{skip: true}, nil
	case isFK && fk != "" && !strings.ContainsAny(fk, "= "):
		var tag fieldTag
		for column := range strings.SplitSeq(fk, ",") {
			var err error
			if tag.fk, err = fkColumn(tag.fk, column); err != nil {
				return fieldTag{}, fmt.Errorf("orm:%q: %w", value, err)
			}
		}
		return tag, nil
	case value != "" && !strings.ContainsAny(value, ",= "):
		return fieldTag{column: value}, nil
	}
	return fieldTag{}, fmt.Errorf(`orm:%q is neither a column, -, nor fk=<columns>`, value)
}

// parseRelationTag reads the tag of a scalar relation: relation=, column=, and fk=, whose columns go on in the parts
// after it with no =, as in relation=record_topics,fk=tenant_id,record_id.
func parseRelationTag(value string) (fieldTag, error) {
	var tag fieldTag
	option := ""
	for part := range strings.SplitSeq(value, ",") {
		key, val, ok := strings.Cut(part, "=")
		if ok {
			option = key
		} else {
			key, val = option, part
		}
		if val == "" || strings.ContainsAny(val, "= \t\n") || !ok && option != "fk" {
			return fieldTag{}, fmt.Errorf("invalid scalar relation tag %q", value)
		}
		switch {
		case key == "relation" && tag.relation == "":
			tag.relation = val
		case key == "column" && tag.value == "":
			tag.value = val
		case key == "fk" && (!ok || tag.fk == nil):
			var err error
			if tag.fk, err = fkColumn(tag.fk, val); err != nil {
				return fieldTag{}, fmt.Errorf("orm:%q: %w", value, err)
			}
		case key == "relation" || key == "column" || key == "fk":
			return fieldTag{}, fmt.Errorf("duplicate %s option", key)
		default:
			return fieldTag{}, fmt.Errorf("unknown scalar relation option %q", key)
		}
	}
	return tag, nil
}

// fkColumn adds column to columns, those of an fk= so far. It names a column once, and its table not at all: the kind
// of the relation tells that.
func fkColumn(columns []string, column string) ([]string, error) {
	switch {
	case column == "":
		return nil, errors.New("fk= names no column between two commas")
	case strings.Contains(column, "."):
		return nil, fmt.Errorf("fk= names the columns of the foreign key alone, not %s, a table's: the kind of the "+
			"relation tells which table they are of", column)
	case slices.Contains(columns, column):
		return nil, fmt.Errorf("fk= names %s twice", column)
	}
	return append(columns, column), nil
}
