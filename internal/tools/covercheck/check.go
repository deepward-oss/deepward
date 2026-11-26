package main

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Rule assigns a minimum coverage to packages whose import path matches.
type Rule struct {
	Name    string
	Match   func(pkg string) bool
	Minimum float64
}

// Policy is an ordered rule list (first match wins) plus an overall minimum.
type Policy struct {
	Rules   []Rule
	Overall float64
	Exclude func(pkg string) bool
}

// DefaultPolicy implements the gates from design D11.
func DefaultPolicy() Policy {
	contains := func(parts ...string) func(string) bool {
		return func(pkg string) bool {
			for _, p := range parts {
				if strings.Contains(pkg+"/", p) {
					return true
				}
			}
			return false
		}
	}
	return Policy{
		Rules: []Rule{
			{Name: "domain/app", Match: contains("/domain/", "/app/", "/pkg/sdk/", "/internal/engine/"), Minimum: 95},
			{Name: "adapters", Match: func(string) bool { return true }, Minimum: 85},
		},
		Overall: 90,
		Exclude: contains("/cmd/", "/internal/tools/", "/examples/", "/test/", "/sdktest/", "/depstest/", "/fakes/"),
	}
}

type counts struct{ covered, total int }

func (c counts) percent() float64 {
	if c.total == 0 {
		return 100
	}
	return float64(c.covered) * 100 / float64(c.total)
}

// PackageResult is the outcome for one package.
type PackageResult struct {
	Package string
	Rule    string
	Percent float64
	Minimum float64
}

// OK reports whether the package meets its gate.
func (p PackageResult) OK() bool { return p.Percent+1e-9 >= p.Minimum }

// Report is the evaluated coverage.
type Report struct {
	Packages []PackageResult
	Overall  float64
	Minimum  float64
}

// OK reports whether every gate passed.
func (r Report) OK() bool {
	if r.Overall+1e-9 < r.Minimum {
		return false
	}
	for _, p := range r.Packages {
		if !p.OK() {
			return false
		}
	}
	return true
}

// Write prints a human-readable report.
func (r Report) Write(w io.Writer) {
	for _, p := range r.Packages {
		mark := "ok  "
		if !p.OK() {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "%s %6.1f%% (min %.0f%%, %s) %s\n", mark, p.Percent, p.Minimum, p.Rule, p.Package)
	}
	mark := "ok  "
	if r.Overall+1e-9 < r.Minimum {
		mark = "FAIL"
	}
	fmt.Fprintf(w, "%s %6.1f%% (min %.0f%%) overall\n", mark, r.Overall, r.Minimum)
}

// Evaluate parses a Go coverage profile and applies the policy.
func Evaluate(profile io.Reader, policy Policy) (Report, error) {
	perPkg := map[string]*counts{}
	// A block can appear several times (e.g. -coverpkg); keep max hit count.
	seen := map[string]int{}
	stmts := map[string]int{}
	sc := bufio.NewScanner(profile)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "mode:") {
			continue
		}
		// file.go:l.c,l.c numStmts count
		fields := strings.Fields(text)
		if len(fields) != 3 {
			return Report{}, fmt.Errorf("line %d: malformed profile entry %q", line, text)
		}
		n, err1 := strconv.Atoi(fields[1])
		hits, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			return Report{}, fmt.Errorf("line %d: malformed counts in %q", line, text)
		}
		block := fields[0]
		if prev, ok := seen[block]; !ok || hits > prev {
			seen[block] = hits
		}
		stmts[block] = n
	}
	if err := sc.Err(); err != nil {
		return Report{}, err
	}
	var overall counts
	for block, n := range stmts {
		file := block[:strings.LastIndex(block, ":")]
		pkg := path.Dir(file)
		if policy.Exclude != nil && policy.Exclude(pkg) {
			continue
		}
		c := perPkg[pkg]
		if c == nil {
			c = &counts{}
			perPkg[pkg] = c
		}
		c.total += n
		overall.total += n
		if seen[block] > 0 {
			c.covered += n
			overall.covered += n
		}
	}
	report := Report{Overall: overall.percent(), Minimum: policy.Overall}
	for pkg, c := range perPkg {
		res := PackageResult{Package: pkg, Percent: c.percent()}
		for _, r := range policy.Rules {
			if r.Match(pkg) {
				res.Rule, res.Minimum = r.Name, r.Minimum
				break
			}
		}
		report.Packages = append(report.Packages, res)
	}
	sort.Slice(report.Packages, func(i, j int) bool { return report.Packages[i].Package < report.Packages[j].Package })
	return report, nil
}
