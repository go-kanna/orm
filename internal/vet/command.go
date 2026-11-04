// Package vet is kanna-orm vet, which finds the scopes given to a repository that make it fail on every path, as
// the runtime would report them.
package vet

import (
	"errors"
	"flag"
	"fmt"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"

	"github.com/go-kanna/internal/cli"
	"github.com/go-kanna/internal/diag"
)

const name = "kanna-orm vet"

// Main runs kanna-orm vet with args, and returns its exit code.
func Main(args []string) int {
	return Exec(cli.Env{Out: os.Stdout, Err: os.Stderr, Getenv: os.Getenv}, args)
}

// Exec runs kanna-orm vet in env, the way the commands of go-kanna/internal/cli run.
func Exec(env cli.Env, args []string) int {
	fset := flag.NewFlagSet(name, flag.ContinueOnError)
	fset.SetOutput(env.Err)
	fset.Usage = func() {}
	var (
		tags    string
		version bool
	)
	cli.Tags(fset, &tags)
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

	dir, err := filepath.Abs(env.Dir)
	if err != nil {
		fmt.Fprintf(env.Err, "%s: %v\n", name, err)
		return cli.ExitFailure
	}
	patterns := fset.Args()
	if len(patterns) == 0 {
		patterns = []string{"."}
	}
	ds := Run(dir, tags, patterns)
	diag.Write(env.Err, dir, ds)
	if diag.HasErrors(ds) {
		return cli.ExitFailure
	}
	return cli.ExitOK
}

// Run checks the packages patterns match in dir, and returns what it finds, the files named as from dir.
func Run(dir, tags string, patterns []string) []diag.Diag {
	l, ds := load(dir, tags, patterns)
	if diag.HasErrors(ds) {
		return ds
	}
	a := newAnalysis(l.prog, l.fset, l.files)
	a.findRepositories(l.types)
	for _, fn := range l.functions {
		a.check(fn)
	}
	return reports(a, dir)
}

func reports(a *analysis, dir string) []diag.Diag {
	slices.SortFunc(a.reports, func(x, y report) int { return int(x.pos - y.pos) })
	var (
		ds   []diag.Diag
		seen = map[token.Pos]bool{}
	)
	for _, r := range a.reports {
		if seen[r.pos] {
			continue
		}
		seen[r.pos] = true
		pos := a.fset.Position(r.pos)
		d := diag.Errorf(pos, "%s", r.fail.msg)
		if at := r.fail.at; at.pos.IsValid() && r.fail.hint != "" {
			p := a.fset.Position(at.pos)
			if p.Filename != pos.Filename || p.Line != pos.Line {
				p.Filename = diag.ShortPath(dir, p.Filename)
				d = d.WithHints(p.String() + ": " + describe(at, r.fail.hint, r.in))
			}
		}
		ds = append(ds, d)
	}
	return ds
}

func usage(w io.Writer, fset *flag.FlagSet) {
	fmt.Fprintf(w, "%s: find the scopes given to a repository that make it fail on every path\n\n"+
		"Usage:\n  %s [flags] [packages]\n\n"+
		"The packages default to the one in the current directory. A package passing finds no such\n"+
		"scope; it does not prove there is none.\n\nFlags:\n", name, name)
	fset.SetOutput(w)
	fset.PrintDefaults()
}

func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "unknown"
}
