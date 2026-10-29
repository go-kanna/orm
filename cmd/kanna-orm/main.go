// Command kanna-orm writes the database code behind repository interfaces, from what kanna-orm describe records of
// a database.
package main

import (
	"fmt"
	"os"

	"github.com/go-kanna/internal/cli"

	"github.com/go-kanna/orm/internal/describe"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "describe" {
		os.Exit(describe.Main(os.Args[2:]))
	}
	fmt.Fprintln(os.Stderr, "kanna-orm: generating is still to be written; kanna-orm describe works")
	os.Exit(cli.ExitUsage)
}
