package describe_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/go-kanna/orm/internal/describe"
)

func TestMySQLDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url, want string
	}{
		{"mysql://kanna:p%40ss@127.0.0.1:13306/shop", "kanna:p@ss@tcp(127.0.0.1:13306)/shop"},
		{"mysql://kanna@db/shop?parseTime=true", "kanna@tcp(db:3306)/shop?parseTime=true"},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.url)
		if err != nil {
			t.Fatal(err)
		}
		got, err := describe.MySQLDSN(u)
		if err != nil || got != tt.want {
			t.Errorf("MySQLDSN(%s) = %q, %v; want %q", tt.url, got, err, tt.want)
		}
	}
}

func TestOpen_Rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, url, want string
	}{
		{"another database", "sqlite://shop.db",
			`the database URL starts "sqlite://", but kanna-orm knows postgres:// and mysql://`},
		{"a MySQL URL without its database", "mysql://kanna:secret@127.0.0.1:13306/", "the database URL names no database"},
		{"an option MySQL does not take", "mysql://kanna:secret@127.0.0.1:13306/shop?parseTime=maybe",
			"read the options of the database URL"},
		{"a URL that does not parse", "postgres://kanna:secret@[::1", "read the database URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := describe.Open(t.Context(), tt.url)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Open error = %v, want one containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("Open error = %v, which tells the password", err)
			}
		})
	}
}
