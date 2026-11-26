// Command covercheck enforces per-package and overall coverage gates on a Go
// coverage profile (see design D11).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("covercheck", flag.ContinueOnError)
	fs.SetOutput(out)
	profile := fs.String("profile", "coverage.out", "coverage profile path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	f, err := os.Open(*profile)
	if err != nil {
		fmt.Fprintln(out, "covercheck:", err)
		return 2
	}
	defer f.Close()
	report, err := Evaluate(f, DefaultPolicy())
	if err != nil {
		fmt.Fprintln(out, "covercheck:", err)
		return 2
	}
	report.Write(out)
	if !report.OK() {
		return 1
	}
	return 0
}
