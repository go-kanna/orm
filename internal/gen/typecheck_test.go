package gen_test

import (
	"go/types"
	"testing"

	"github.com/go-kanna/orm/internal/gen"
)

// A Go type reads a column only if it holds every value of the column's type:
// as wide, and signed where the column is, or wider where it is unsigned.
func TestReadable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind   types.BasicKind
		dbType string
		want   string // the type to declare instead, or "" when it reads the column
	}{
		{types.Int16, "int2", ""},
		{types.Int8, "int2", "int64"},
		{types.Int32, "int4", ""},
		{types.Int64, "int8", ""},
		{types.Int, "int8", ""},
		{types.Uint32, "int4", "int64"},
		{types.Int8, "tinyint", ""},
		{types.Uint8, "tinyint", "int64"},
		{types.Uint8, "tinyint unsigned", ""},
		{types.Int8, "tinyint unsigned", "int64"},
		{types.Int16, "tinyint unsigned", ""},
		{types.Int16, "mediumint", "int64"},
		{types.Int32, "mediumint", ""},
		{types.Int32, "int unsigned", "int64"},
		{types.Int64, "int(11) unsigned", ""},
		{types.Int64, "bigint unsigned", "uint64"},
		{types.Uint64, "bigint unsigned", ""},
		{types.Int32, "oid", "int64"},
		{types.Uint32, "oid", ""},
		{types.Uint8, "year", "int64"},
		{types.Int16, "year", ""},
		// tinyint(1), which MySQL takes for a boolean, holds a tinyint.
		{types.Int8, "tinyint(1)", ""},
		{types.Uint8, "tinyint(1)", "int64"},
		{types.Float32, "float4", ""},
		{types.Float32, "float8", "float64"},
		{types.Float32, "double", "float64"},
		{types.Float32, "real", "float64"},
		{types.Float64, "float4", ""},
		// The range of numeric is not recorded, so any number reads it.
		{types.Int16, "numeric", ""},
	}

	for _, tt := range tests {
		t.Run(types.Typ[tt.kind].Name()+" from "+tt.dbType, func(t *testing.T) {
			t.Parallel()

			want, ok := gen.Readable(tt.kind, tt.dbType)
			if ok != (tt.want == "") || want != tt.want {
				t.Errorf("Readable = %q, %t; want %q", want, ok, tt.want)
			}
		})
	}
}

// A cast in SQL names the type without the length or precision the column
// declares, as a cast to varchar(2) would cut a longer value short.
func TestCastType(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"varchar(100)":    "varchar",
		"numeric(10,2)":   "numeric",
		"varchar(16)[]":   "varchar[]",
		"timestamptz(3)":  "timestamptz",
		"int8":            "int8",
		"bigint unsigned": "bigint unsigned",
	} {
		if got := gen.CastType(in); got != want {
			t.Errorf("CastType(%q) = %q, want %q", in, got, want)
		}
	}
}
