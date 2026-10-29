package gen_test

import (
	"testing"

	"github.com/go-kanna/orm/internal/gen"
)

func TestColumnName(t *testing.T) {
	t.Parallel()

	for field, want := range map[string]string{
		"ID":          "id",
		"Name":        "name",
		"CustomerID":  "customer_id",
		"PlacedAt":    "placed_at",
		"SKU":         "sku",
		"HTTPServer":  "http_server",
		"OAuthClient": "o_auth_client",
		"UserID2":     "user_id2",
		"X509Cert":    "x509_cert",
		"Line2Note":   "line2_note",
		"Größe":       "größe",
	} {
		if got := gen.ColumnName(field); got != want {
			t.Errorf("columnName(%q) = %q, want %q", field, got, want)
		}
	}
}

func TestParseTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tag, column string
		skip        bool
		fk, err     string
	}{
		{tag: ``},
		{tag: `json:"memo"`},
		{tag: `orm:"note"`, column: "note"},
		{tag: `json:"memo" orm:"note"`, column: "note"},
		{tag: `orm:"-"`, skip: true},
		{tag: `orm:"fk=order_id"`, fk: "order_id"},
		{tag: `orm:""`, err: `orm:"" is neither a column, -, nor fk=<column>`},
		{tag: `orm:"note,omitempty"`, err: `orm:"note,omitempty" is neither a column, -, nor fk=<column>`},
		{tag: `orm:"fk="`, err: `orm:"fk=" is neither a column, -, nor fk=<column>`},
		{tag: `orm:"key=id"`, err: `orm:"key=id" is neither a column, -, nor fk=<column>`},
		{tag: `orm:note`, err: `the struct tag is not one reflect can read: write it as orm:"name"`},
	}
	for _, tt := range tests {
		column, skip, fk, err := gen.ParseTag(tt.tag)
		switch {
		case tt.err == "" && err != nil:
			t.Errorf("parseTag(%s): %v", tt.tag, err)
		case tt.err != "" && (err == nil || err.Error() != tt.err):
			t.Errorf("parseTag(%s) error = %v, want %q", tt.tag, err, tt.err)
		case column != tt.column || skip != tt.skip || fk != tt.fk:
			t.Errorf("parseTag(%s) = %q, %v, %q; want %q, %v, %q", tt.tag, column, skip, fk, tt.column, tt.skip, tt.fk)
		}
	}
}
