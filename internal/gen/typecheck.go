package gen

import (
	"go/types"
	"strings"
)

// dbClass is what a column of a database type holds, as far as which Go types the drivers read it into goes.
type dbClass int

const (
	classUnknown dbClass = iota
	classInteger
	classFloat
	classNumeric
	classText
	classBool
	classTinyint1 // MySQL's boolean, an integer of one digit
	classTime
	classBytes
	classUUID
)

// dbClasses classify the types describe records, PostgreSQL's by their names, and MySQL's by their names without
// what follows in parentheses, or unsigned.
var dbClasses = map[string]dbClass{
	"int2": classInteger, "int4": classInteger, "int8": classInteger, "oid": classInteger,
	"tinyint": classInteger, "smallint": classInteger, "mediumint": classInteger, "int": classInteger,
	"integer": classInteger, "bigint": classInteger, "year": classInteger,
	"float4": classFloat, "float8": classFloat, "float": classFloat, "double": classFloat, "real": classFloat,
	"numeric": classNumeric, "decimal": classNumeric,
	"text": classText, "varchar": classText, "bpchar": classText, "char": classText, "name": classText,
	"citext": classText, "tinytext": classText, "mediumtext": classText, "longtext": classText,
	"enum": classText, "set": classText,
	"bool": classBool, "boolean": classBool,
	"timestamptz": classTime, "timestamp": classTime, "date": classTime, "datetime": classTime,
	"bytea": classBytes, "binary": classBytes, "varbinary": classBytes, "blob": classBytes, "tinyblob": classBytes,
	"mediumblob": classBytes, "longblob": classBytes,
	"uuid": classUUID,
}

// classOf classifies dbType. A type it does not know, such as an enum of PostgreSQL or json, is classUnknown.
func classOf(dbType string) dbClass {
	t := strings.ToLower(strings.TrimSpace(dbType))
	if t == "tinyint(1)" {
		return classTinyint1
	}
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = t[:i]
	}
	t, _, _ = strings.Cut(t, " ")
	return dbClasses[t]
}

// fits reports whether a Go value of type t is what a driver reads a value of dbType into, and writes it from, and if
// not, the type to declare instead. A type with a Scan method converts itself, and a database type kanna-orm does not
// know, or does not tell, is taken at the Go type's word.
func fits(t types.Type, dbType string) (want string, ok bool) {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	if scanner(t) {
		return "", true
	}
	if elem, ok := strings.CutSuffix(dbType, "[]"); ok {
		s, isSlice := t.Underlying().(*types.Slice)
		if !isSlice || isBytes(t) {
			return "a slice", false
		}
		if want, ok := fits(s.Elem(), elem); !ok {
			return "[]" + want, false
		}
		return "", true
	}

	var (
		b, isBasic = t.Underlying().(*types.Basic)
		info       types.BasicInfo
	)
	if isBasic {
		info = b.Info()
	}
	integer, float, str := info&types.IsInteger != 0, info&types.IsFloat != 0, info&types.IsString != 0
	switch class := classOf(dbType); class {
	case classUnknown:
		return "", true
	case classInteger:
		return "int64", integer
	case classFloat:
		return "float64", float
	case classNumeric:
		return "string", integer || float || str
	case classText:
		return "string", str || isBytes(t)
	case classBool:
		return "bool", info&types.IsBoolean != 0
	case classTinyint1:
		return "bool", info&types.IsBoolean != 0 || integer
	case classTime:
		return "time.Time", isTime(t)
	case classBytes:
		return "[]byte", isBytes(t) || str
	case classUUID:
		return "string", str || isBytes(t)
	}
	return "", true
}

// isBytes reports whether t is a []byte, or a type made of one.
func isBytes(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	b, ok := s.Elem().Underlying().(*types.Basic)
	return ok && b.Kind() == types.Byte
}
