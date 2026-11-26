package toolchain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Constraint is a conjunction of version comparisons such as ">=3.6, <4".
type Constraint struct {
	raw   string
	terms []term
}

type term struct {
	op  string
	v   []int
	pre string
}

var termPattern = regexp.MustCompile(`^(>=|<=|!=|==|=|>|<)?\s*v?([0-9][0-9A-Za-z.\-+]*)$`)

// ParseConstraint parses a constraint; the empty string accepts everything.
func ParseConstraint(s string) (Constraint, error) {
	c := Constraint{raw: strings.TrimSpace(s)}
	if c.raw == "" {
		return c, nil
	}
	for _, part := range strings.Split(c.raw, ",") {
		part = strings.TrimSpace(part)
		m := termPattern.FindStringSubmatch(part)
		if m == nil {
			return Constraint{}, fmt.Errorf("invalid version constraint %q", s)
		}
		op := m[1]
		if op == "" || op == "==" {
			op = "="
		}
		nums, pre := parseLoose(m[2])
		c.terms = append(c.terms, term{op: op, v: nums, pre: pre})
	}
	return c, nil
}

// String returns the constraint text.
func (c Constraint) String() string { return c.raw }

// Allows reports whether version satisfies the constraint.
func (c Constraint) Allows(version string) bool {
	if len(c.terms) == 0 {
		return true
	}
	nums, pre := parseLoose(version)
	if len(nums) == 0 {
		return false
	}
	for _, t := range c.terms {
		cmp := compareLoose(nums, pre, t.v, t.pre)
		ok := false
		switch t.op {
		case ">=":
			ok = cmp >= 0
		case ">":
			ok = cmp > 0
		case "<=":
			ok = cmp <= 0
		case "<":
			ok = cmp < 0
		case "!=":
			ok = cmp != 0
		default:
			ok = cmp == 0
		}
		if !ok {
			return false
		}
	}
	return true
}

// parseLoose splits "1.10.2-RC1" into [1 10 2] and "RC1".
func parseLoose(v string) ([]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	core, pre := v, ""
	if i := strings.IndexAny(v, "-+_"); i >= 0 {
		core, pre = v[:i], v[i+1:]
	}
	var nums []int
	for _, p := range strings.Split(core, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		nums = append(nums, n)
	}
	return nums, pre
}

// compareLoose compares numerically; missing components are 0, and a
// prerelease sorts before the release.
func compareLoose(a []int, apre string, b []int, bpre string) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	}
	return strings.Compare(apre, bpre)
}
