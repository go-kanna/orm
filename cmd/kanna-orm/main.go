// Command kanna-orm writes the database code behind repository interfaces, from what kanna-orm describe records of
// a database. kanna-orm vet finds the scopes that make the code it writes fail every time, and the calls in a
// transaction that run outside it.
package main

import (
	"os"

	"github.com/go-kanna/orm/internal/describe"
	"github.com/go-kanna/orm/internal/gen"
	"github.com/go-kanna/orm/internal/vet"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "describe" {
		os.Exit(describe.Main(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "vet" {
		os.Exit(vet.Command().Main(os.Args[2:]))
	}
	os.Exit(gen.Command().Main(os.Args[1:]))
}
