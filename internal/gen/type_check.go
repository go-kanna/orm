package gen

import (
	"go/types"
	"strconv"
	"strings"

	"github.com/go-kanna/orm/internal/snapshot"
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

// classOf classifies dbType. A type it does not know, such as an enum of PostgreSQL or json, is classUnknown. citext,
// of an extension, is named with the schema it is installed in.
func classOf(dbType string) dbClass {
	if strings.ToLower(strings.TrimSpace(dbType)) == "tinyint(1)" {
		return classTinyint1
	}
	base := baseType(dbType)
	if i := strings.LastIndexByte(base, '.'); i >= 0 && base[i+1:] == "citext" {
		base = "citext"
	}
	return dbClasses[base]
}

// fits reports whether a Go value of type t is what drv reads a value of dbType into, and writes it from, and if not,
// the type to declare instead; drv is empty when the run does not know it, and any driver will do. A type with a Scan
// method converts itself, and a database type kanna-orm does not know, or does not tell, is taken at the Go type's
// word.
func fits(t types.Type, dbType string, drv driver) (want string, ok bool) {
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
		if want, ok := fits(s.Elem(), elem, drv); !ok {
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
		// pgx reads a bytea in binary, which it scans into no string.
		return "[]byte", isBytes(t) || str && (drv != driverPgx || baseType(dbType) != "bytea")
	case classUUID:
		return "string", str || isBytes(t) || isUUID(t)
	}
	return "", true
}

// readable is fits for a value read from a column of dbType, a type of d: the Go type also has to hold every value of
// dbType, so that no row fails to scan for the number it holds, or loses precision in a float, and a bit of MySQL is
// read into bytes alone. A value written needs only fits, as a database rejects one beyond its column's range rather
// than store another.
func readable(t types.Type, dbType string, d snapshot.Dialect, drv driver) (want string, ok bool) {
	if want, ok := fits(t, dbType, drv); !ok {
		return want, false
	}
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	if scanner(t) {
		return "", true
	}
	if elem, ok := strings.CutSuffix(dbType, "[]"); ok {
		if s, ok := t.Underlying().(*types.Slice); ok {
			if want, ok := readable(s.Elem(), elem, d, drv); !ok {
				return "[]" + want, false
			}
		}
		return "", true
	}
	switch {
	case mysqlBit(d, dbType):
		// go-sql-driver/mysql reads a bit as bytes, which it converts to no number or bool, though it writes one.
		if !isBytes(t) {
			return "[]byte", false
		}
		return "", true
	case postgresBit(d, dbType):
		// pgx reads a bit in binary, which it scans into no type but one with a Scan method. database/sql reads it as
		// text, as 101, which a number would read as another value, and a bool only in a bit(1).
		if drv == driverPgx {
			return "pgtype.Bits", false
		}
		if !isBytes(t) && !isBasic(t.Underlying(), types.String) {
			return "string", false
		}
		return "", true
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return "", true
	}
	if r, ok := intRangeOf(dbType); ok && b.Info()&types.IsInteger != 0 && !r.heldBy(b) {
		return r.want(), false
	}
	if bits, ok := floatBits[baseType(dbType)]; ok && b.Kind() == types.Float32 && bits > 32 {
		return "float64", false
	}
	if classOf(dbType) == classNumeric && b.Info()&(types.IsInteger|types.IsFloat) != 0 {
		return numericWant(b, dbType)
	}
	return "", true
}

// mysqlBit reports whether dbType, a type of d, is MySQL's bit.
func mysqlBit(d snapshot.Dialect, dbType string) bool {
	return d == snapshot.MySQL && baseType(dbType) == "bit"
}

// postgresBit reports whether dbType, a type of d, is PostgreSQL's bit or varbit.
func postgresBit(d snapshot.Dialect, dbType string) bool {
	return d == snapshot.PostgreSQL && (baseType(dbType) == "bit" || baseType(dbType) == "varbit")
}

// bitHints are the hints for a Go type that cannot hold dbType, a type of d, read through drv: for MySQL's bit, the
// boolean MySQL has instead, and for PostgreSQL's bits, read through any driver, the type pgx reads them into.
func bitHints(d snapshot.Dialect, dbType string, drv driver) []string {
	switch {
	case mysqlBit(d, dbType):
		return []string{"for a boolean, make the column tinyint(1), which a bool holds"}
	case postgresBit(d, dbType) && drv == "":
		return []string{"pgx reads it into no string or []byte, but into a type with a Scan method, such as pgtype.Bits"}
	}
	return nil
}

// numericWant reports whether b, a Go number, holds every value of dbType, a numeric or decimal, and if not, the type
// to declare instead. An integer holds one of scale 0, as wide as its digits; a floating-point number none, as it
// rounds what it does not hold exactly; and none holds a numeric whose scale is not recorded.
func numericWant(b *types.Basic, dbType string) (want string, ok bool) {
	precision, scale, recorded := numericDigits(dbType)
	if b.Info()&types.IsFloat != 0 || !recorded || scale != 0 {
		return "string", false
	}
	if b.Info()&types.IsUnsigned != 0 && !strings.Contains(strings.ToLower(dbType), "unsigned") ||
		precision > intDigits(b) {
		if precision <= intDigits(types.Typ[types.Int64]) {
			return "int64", false
		}
		return "string", false
	}
	return "", true
}

// numericDigits returns the precision and scale dbType declares in parentheses, numeric(10,2) or decimal(10), whose
// scale is 0, if it declares them.
func numericDigits(dbType string) (precision, scale int, ok bool) {
	_, args, ok := strings.Cut(dbType, "(")
	if !ok {
		return 0, 0, false
	}
	args, _, _ = strings.Cut(args, ")")
	p, s, hasScale := strings.Cut(args, ",")
	precision, err := strconv.Atoi(strings.TrimSpace(p))
	if err != nil {
		return 0, 0, false
	}
	if hasScale {
		if scale, err = strconv.Atoi(strings.TrimSpace(s)); err != nil {
			return 0, 0, false
		}
	}
	return precision, scale, true
}

// intDigits is the number of decimal digits every value of which b, a Go integer, holds: 18 of an int64. int and uint
// are taken to be 64 bits wide, as on amd64.
func intDigits(b *types.Basic) int {
	bits := 8 * types.SizesFor("gc", "amd64").Sizeof(b)
	if b.Info()&types.IsUnsigned == 0 {
		bits--
	}
	maxValue := uint64(1)<<(bits-1)<<1 - 1
	return len(strconv.FormatUint(maxValue, 10)) - 1
}

// intRange is the integers a column of an integer type holds: those of bits bits, signed or not.
type intRange struct {
	bits   int
	signed bool
}

// intBits are the widths of the integer types describe records, PostgreSQL's and MySQL's, signed unless their name
// says unsigned.
var intBits = map[string]int{
	"int2": 16, "int4": 32, "int8": 64,
	"tinyint": 8, "smallint": 16, "mediumint": 24, "int": 32, "integer": 32, "bigint": 64,
}

// intRangeOf returns the integers a column of dbType holds, if it is an integer type of a known range.
func intRangeOf(dbType string) (intRange, bool) {
	switch base := baseType(dbType); base {
	case "oid":
		return intRange{bits: 32}, true
	case "year":
		// 1901 to 2155, and 0.
		return intRange{bits: 12}, true
	default:
		bits, ok := intBits[base]
		return intRange{bits: bits, signed: !strings.Contains(strings.ToLower(dbType), "unsigned")}, ok
	}
}

// heldBy reports whether a Go integer of type b holds every value of r. int and uint are taken to be 64 bits wide, as
// on amd64.
func (r intRange) heldBy(b *types.Basic) bool {
	bits := 8 * int(types.SizesFor("gc", "amd64").Sizeof(b))
	if b.Info()&types.IsUnsigned != 0 {
		return !r.signed && bits >= r.bits
	}
	if r.signed {
		return bits >= r.bits
	}
	return bits > r.bits
}

// want names the Go type to declare for r: int64, or uint64 for the unsigned integers of 64 bits.
func (r intRange) want() string {
	if !r.signed && r.bits == 64 {
		return "uint64"
	}
	return "int64"
}

// floatBits are the widths of the floating-point types describe records. MySQL's real is its double.
var floatBits = map[string]int{"float4": 32, "float": 32, "float8": 64, "double": 64, "real": 64}

// baseType returns dbType in lower case without what follows in parentheses, or unsigned.
func baseType(dbType string) string {
	t := strings.ToLower(strings.TrimSpace(dbType))
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = t[:i]
	}
	t, _, _ = strings.Cut(t, " ")
	return t
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

// castType returns dbType without the length or precision it declares, for a cast in SQL: a cast to varchar(2)
// would cut a longer value short rather than reject it, as the column does.
func castType(dbType string) string {
	if i := strings.IndexByte(dbType, '('); i >= 0 {
		if j := strings.IndexByte(dbType[i:], ')'); j >= 0 {
			return dbType[:i] + dbType[i+j+1:]
		}
	}
	return dbType
}
