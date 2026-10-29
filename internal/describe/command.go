package describe

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/diag"
	"github.com/go-kanna/internal/output"
	"github.com/go-kanna/internal/packages"

	"github.com/go-kanna/orm/internal/snapshot"
)

const name = "kanna-orm describe"

// Main runs kanna-orm describe with args, and returns its exit code.
func Main(args []string) int {
	return Exec(cli.Env{Out: os.Stdout, Err: os.Stderr, Getenv: os.Getenv}, args)
}

// Exec runs kanna-orm describe in env, the way the commands of go-kanna/internal/cli run.
func Exec(env cli.Env, args []string) int {
	fset := flag.NewFlagSet(name, flag.ContinueOnError)
	fset.SetOutput(env.Err)
	fset.Usage = func() {}
	var (
		dbURL, snapshotPath, tags string
		check, verbose, version   bool
	)
	fset.StringVar(&dbURL, "database-url", "", "the database to describe (default: $DATABASE_URL)")
	fset.StringVar(&snapshotPath, "snapshot", "",
		"the snapshot to write (default: "+snapshot.Name+" at the root of the module)")
	fset.StringVar(&tags, "tags", "", "comma-separated build tags")
	fset.BoolVar(&check, "check", false, "check that the snapshot is up to date with the database instead of writing it")
	fset.BoolVar(&verbose, "v", false, "say whether the snapshot was written or left unchanged")
	fset.BoolVar(&version, "version", false, "print the version and exit")

	help := len(args) > 0 && args[0] == "help"
	err := fset.Parse(args)
	switch {
	case help || errors.Is(err, flag.ErrHelp):
		usage(env.Out, fset)
		return cli.ExitOK
	case err != nil:
		usage(env.Err, fset)
		return cli.ExitUsage
	case version:
		fmt.Fprintln(env.Out, "kanna-orm", buildVersion())
		return cli.ExitOK
	}

	if dbURL == "" && env.Getenv != nil {
		dbURL = env.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		fmt.Fprintf(env.Err, "%s: no database to describe: give -database-url, or set DATABASE_URL\n", name)
		return cli.ExitUsage
	}
	dir, err := filepath.Abs(env.Dir)
	if err != nil {
		fmt.Fprintf(env.Err, "%s: %v\n", name, err)
		return cli.ExitFailure
	}
	path, err := snapshot.Path(dir, snapshotPath)
	if err != nil {
		fmt.Fprintf(env.Err, "%s: %v\n", name, err)
		return cli.ExitUsage
	}
	patterns := fset.Args()
	if len(patterns) == 0 {
		patterns = []string{"."}
	}

	ctx := context.Background()
	s, ds := run(ctx, dbURL, patterns, packages.Config{Dir: dir, Tags: tags})
	diag.Write(env.Err, dir, ds)
	if diag.HasErrors(ds) {
		return cli.ExitFailure
	}

	data := snapshot.Marshal(s)
	if check {
		ds := checkSnapshot(path, s, data)
		diag.Write(env.Err, dir, ds)
		if diag.HasErrors(ds) {
			return cli.ExitFailure
		}
		return cli.ExitOK
	}

	action := output.Unchanged
	if existing, err := os.ReadFile(path); err != nil || !bytes.Equal(existing, data) { //nolint:gosec // the snapshot
		if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a file to commit, which everyone reads
			fmt.Fprintf(env.Err, "%s: %v\n", name, err)
			return cli.ExitFailure
		}
		action = output.Wrote
	}
	if verbose {
		fmt.Fprintln(env.Out, action, diag.ShortPath(dir, path))
	}
	return cli.ExitOK
}

func run(ctx context.Context, dbURL string, patterns []string, cfg packages.Config) (snapshot.Snapshot, []diag.Diag) {
	db, closeDB, err := Open(ctx, dbURL)
	if err != nil {
		return snapshot.Snapshot{}, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}
	}
	set, err := packages.Load(patterns, cfg)
	if err != nil {
		return snapshot.Snapshot{}, []diag.Diag{diag.Errorf(token.Position{}, "%v", err)}
	}
	// Only the declarations matter here: code calling what is still to be generated may well not compile, and
	// set.Diags would say so.
	s, ds := Describe(ctx, db, set.Packages)
	if err := closeDB(); err != nil {
		ds = append(ds, diag.Warningf(token.Position{}, "close the connection: %v", err))
	}
	return s, ds
}

// checkSnapshot reports how the snapshot at path differs from s, whose snapshot data is.
func checkSnapshot(path string, s snapshot.Snapshot, data []byte) []diag.Diag {
	at := token.Position{Filename: path}
	existing, err := os.ReadFile(path) //nolint:gosec // the snapshot
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return []diag.Diag{diag.Errorf(at, "not described yet").WithHints("run go tool kanna-orm describe to write it")}
	case err != nil:
		return []diag.Diag{diag.Errorf(at, "%v", err)}
	case bytes.Equal(existing, data):
		return nil
	}

	var hints []string
	if was, err := snapshot.Unmarshal(existing); err != nil {
		hints = []string{err.Error()}
	} else {
		hints = snapshot.Diff(was, s)
	}
	if len(hints) == 0 {
		hints = []string{"it holds what the database does, but not as kanna-orm writes it"}
	}
	hints = append(hints, "run go tool kanna-orm describe to update it")
	return []diag.Diag{diag.Errorf(at, "out of date with the database").WithHints(hints...)}
}

func usage(w io.Writer, fset *flag.FlagSet) {
	fmt.Fprintf(w, "%s: record what kanna-orm needs to know of a database\n\nUsage:\n  %s [flags] [packages]\n\nFlags:\n",
		name, name)
	fset.SetOutput(w)
	fset.PrintDefaults()
}

func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "unknown"
}
