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
		tag, column       string
		skip              bool
		fk, relation, err string
	}{
		{tag: ``},
		{tag: `json:"memo"`},
		{tag: `orm:"note"`, column: "note"},
		{tag: `json:"memo" orm:"note"`, column: "note"},
		{tag: `orm:"-"`, skip: true},
		{tag: `orm:"fk=order_id"`, fk: "order_id"},
		{tag: `orm:"fk=order_id,line_no"`, fk: "order_id,line_no"},
		{tag: `orm:"fk=order_lines.order_id"`, err: `orm:"fk=order_lines.order_id": fk= names the columns of the ` +
			`foreign key alone, not order_lines.order_id, a table's: the kind of the relation tells which table they are of`},
		{tag: `orm:"fk=order_id,order_id"`, err: `orm:"fk=order_id,order_id": fk= names order_id twice`},
		{tag: `orm:"fk=order_id,"`, err: `orm:"fk=order_id,": fk= names no column between two commas`},
		{tag: `orm:""`, err: `orm:"" is neither a column, -, nor fk=<columns>`},
		{tag: `orm:"note,omitempty"`, err: `unknown or duplicate orm option "omitempty"`},
		{tag: `orm:",default"`},
		{tag: `orm:"id,default"`, column: "id"},
		{tag: `orm:"note,default,nullzero"`, column: "note"},
		{tag: `orm:",default,default"`, err: `unknown or duplicate orm option "default"`},
		{tag: `orm:"fk="`, err: `orm:"fk=" is neither a column, -, nor fk=<columns>`},
		{tag: `orm:"key=id"`, err: `orm:"key=id" is neither a column, -, nor fk=<columns>`},
		{tag: `orm:note`, err: `the struct tag is not one reflect can read: write it as orm:"name"`},
		{tag: `orm:"relation=device_topics"`, relation: "device_topics."},
		{tag: `orm:"relation=device_topics,column=topic,fk=device_id"`, relation: "device_topics.topic", fk: "device_id"},
		{tag: `orm:"relation=record_topics,fk=tenant_id,record_id,column=topic"`, relation: "record_topics.topic",
			fk: "tenant_id,record_id"},
		{tag: `orm:"relation=t,column=a,b"`, err: `invalid scalar relation tag "relation=t,column=a,b"`},
		{tag: `orm:"relation=t,fk=a,,b"`, err: `invalid scalar relation tag "relation=t,fk=a,,b"`},
		{tag: `orm:"relation=t,fk=a,fk=b"`, err: `duplicate fk option`},
		{tag: `orm:"relation=t,fk=a,a"`, err: `orm:"relation=t,fk=a,a": fk= names a twice`},
		{tag: `orm:"relation=t,fk=t.a"`, err: `orm:"relation=t,fk=t.a": fk= names the columns of the foreign key ` +
			`alone, not t.a, a table's: the kind of the relation tells which table they are of`},
		{tag: `orm:"relation=t,key=a"`, err: `unknown scalar relation option "key"`},
	}
	for _, tt := range tests {
		column, skip, fk, relation, err := gen.ParseTag(tt.tag)
		switch {
		case tt.err == "" && err != nil:
			t.Errorf("parseTag(%s): %v", tt.tag, err)
		case tt.err != "" && (err == nil || err.Error() != tt.err):
			t.Errorf("parseTag(%s) error = %v, want %q", tt.tag, err, tt.err)
		case column != tt.column || skip != tt.skip || fk != tt.fk || relation != tt.relation:
			t.Errorf("parseTag(%s) = %q, %v, %q, %q; want %q, %v, %q, %q", tt.tag, column, skip, fk, relation,
				tt.column, tt.skip, tt.fk, tt.relation)
		}
	}
}
