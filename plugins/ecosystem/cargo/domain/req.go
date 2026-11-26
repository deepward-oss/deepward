package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/deepward-oss/deepward/pkg/sdk/deps"
)

var comparatorRe = regexp.MustCompile(`^(\^|~|=|>=|<=|>|<)?\s*(\*|\d+)(?:\.(\*|x|X|\d+))?(?:\.(\*|x|X|\d+))?(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

type comparator struct {
	op                  string
	major, minor, patch int
	hasMinor, hasPatch  bool
	pre                 string
}

func parseComparator(s string) (comparator, error) {
	m := comparatorRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return comparator{}, fmt.Errorf("invalid requirement %q", s)
	}
	c := comparator{op: m[1], pre: m[5]}
	wild := func(x string) bool { return x == "*" || x == "x" || x == "X" }
	if wild(m[2]) {
		c.op = "*"
		return c, nil
	}
	c.major, _ = strconv.Atoi(m[2])
	if m[3] != "" && !wild(m[3]) {
		c.minor, _ = strconv.Atoi(m[3])
		c.hasMinor = true
	}
	if m[4] != "" && !wild(m[4]) && c.hasMinor {
		c.patch, _ = strconv.Atoi(m[4])
		c.hasPatch = true
	}
	if (m[3] != "" && wild(m[3])) || (m[4] != "" && wild(m[4])) {
		if c.op == "" {
			c.op = "="
		}
	}
	if c.op == "" {
		c.op = "^"
	}
	return c, nil
}

func ver(major, minor, patch int) deps.Version {
	return deps.MustParseVersion(deps.CratesIO, fmt.Sprintf("%d.%d.%d", major, minor, patch))
}

// bounds returns the half-open interval [lo, hi) a comparator allows; a nil
// bound is unbounded.
func (c comparator) bounds() (lo, hi *deps.Version) {
	at := func(major, minor, patch int) *deps.Version { v := ver(major, minor, patch); return &v }
	base := at(c.major, c.minor, c.patch)
	if c.pre != "" {
		v := deps.MustParseVersion(deps.CratesIO, fmt.Sprintf("%d.%d.%d%s", c.major, c.minor, c.patch, c.pre))
		base = &v
	}
	// next is the smallest version above every version matching the
	// given components exactly (=1.2 → 1.3.0).
	next := func() *deps.Version {
		switch {
		case c.hasPatch:
			return at(c.major, c.minor, c.patch+1)
		case c.hasMinor:
			return at(c.major, c.minor+1, 0)
		}
		return at(c.major+1, 0, 0)
	}
	switch c.op {
	case "*":
		return nil, nil
	case "=":
		if c.hasPatch {
			return base, at(c.major, c.minor, c.patch+1)
		}
		return base, next()
	case ">=":
		return base, nil
	case ">":
		return next(), nil
	case "<":
		return nil, base
	case "<=":
		return nil, next()
	case "~":
		if c.hasMinor {
			return base, at(c.major, c.minor+1, 0)
		}
		return base, at(c.major+1, 0, 0)
	}
	// Caret.
	switch {
	case c.major > 0 || !c.hasMinor:
		return base, at(c.major+1, 0, 0)
	case c.minor > 0 || !c.hasPatch:
		return base, at(0, c.minor+1, 0)
	}
	return base, at(0, 0, c.patch+1)
}

// Satisfies reports whether a version meets a Cargo requirement such as
// "1.5", "^0.3", "~1.2.3", "=1.6.0", ">=1, <2" or "1.*".
func Satisfies(req, version string) (bool, error) {
	v, err := deps.ParseVersion(deps.CratesIO, version)
	if err != nil {
		return false, err
	}
	for _, part := range strings.Split(req, ",") {
		c, err := parseComparator(part)
		if err != nil {
			return false, err
		}
		lo, hi := c.bounds()
		if (lo != nil && v.Less(*lo)) || (hi != nil && !v.Less(*hi)) {
			return false, nil
		}
	}
	return true, nil
}

// RewriteReq returns a requirement that allows to, keeping the operator of a
// single-comparator requirement ("1.5" → "1.6.1", "=1.6.0" → "=1.6.1").
func RewriteReq(old, to string) (string, error) {
	if strings.Contains(old, ",") {
		return "", fmt.Errorf("requirement %q has several comparators", old)
	}
	s := strings.TrimSpace(old)
	c, err := parseComparator(s)
	if err != nil {
		return "", err
	}
	switch {
	case c.op == "*" || strings.ContainsAny(s, "*xX"):
		return "", fmt.Errorf("requirement %q uses a wildcard", old)
	case c.op == "^" && !strings.HasPrefix(s, "^"):
		return to, nil
	case c.op == "^" || c.op == "~" || c.op == "=":
		return c.op + to, nil
	}
	return "", fmt.Errorf("requirement %q cannot be rewritten automatically", old)
}
