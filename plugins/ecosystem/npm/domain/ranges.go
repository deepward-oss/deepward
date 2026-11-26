package domain

import (
	"fmt"
	"regexp"
	"strings"
)

var simpleRange = regexp.MustCompile(`^(\^|~|=|v)?\s*(\d+(?:\.\d+){0,2}(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)

// RewriteRange returns the range for a new target version keeping the
// operator of the old range: "^1.2.3" → "^2.0.1", "~1.2.3" → "~1.2.9",
// "1.2.3" → "2.0.1", "npm:real@^1" → "npm:real@^2.0.1". Ranges that cannot be
// rewritten safely return an error with the reason.
func RewriteRange(old, to string) (string, error) {
	r := strings.TrimSpace(old)
	if rest, ok := strings.CutPrefix(r, "npm:"); ok {
		name, inner := SplitDescriptor(rest)
		if inner == "" {
			return "", fmt.Errorf("alias %q has no version range", old)
		}
		nr, err := RewriteRange(inner, to)
		if err != nil {
			return "", err
		}
		return "npm:" + name + "@" + nr, nil
	}
	m := simpleRange.FindStringSubmatch(r)
	if m == nil {
		return "", fmt.Errorf("range %q cannot be rewritten automatically", old)
	}
	switch m[1] {
	case "^", "~", "=":
		return m[1] + to, nil
	default:
		return to, nil
	}
}
