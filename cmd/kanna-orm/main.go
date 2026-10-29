// Command kanna-orm writes the database code behind repository interfaces, from what kanna-orm describe records of
// a database.
package main

import (
	"os"

	"github.com/go-kanna/orm/internal/describe"
	"github.com/go-kanna/orm/internal/gen"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "describe" {
		os.Exit(describe.Main(os.Args[2:]))
	}
	os.Exit(gen.Command().Main(os.Args[1:]))
}
