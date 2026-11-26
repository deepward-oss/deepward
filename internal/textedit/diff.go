package textedit

import (
	"fmt"
	"strings"
)

// Unified renders a unified diff with 3 lines of context. It returns ""
// when the contents are equal.
func Unified(path string, before, after []byte) string {
	if string(before) == string(after) {
		return ""
	}
	a := splitLines(string(before))
	b := splitLines(string(after))
	ops := diffLines(a, b)
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", path, path)
	const ctx = 3
	// Group operations into hunks.
	i := 0
	for i < len(ops) {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := max(0, i-ctx)
		end := i
		for end < len(ops) {
			if ops[end].kind != ' ' {
				end++
				continue
			}
			run := end
			for run < len(ops) && ops[run].kind == ' ' {
				run++
			}
			if run-end > 2*ctx || run == len(ops) {
				end = min(len(ops), end+ctx)
				break
			}
			end = run
		}
		aStart, bStart, aLen, bLen := hunkRange(ops, start, end)
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", aStart, aLen, bStart, bLen)
		for _, op := range ops[start:end] {
			line := op.text
			sb.WriteByte(op.kind)
			sb.WriteString(strings.TrimSuffix(line, "\n"))
			sb.WriteByte('\n')
			if !strings.HasSuffix(line, "\n") {
				sb.WriteString("\\ No newline at end of file\n")
			}
		}
		i = end
	}
	return sb.String()
}

type op struct {
	kind  byte // ' ', '-', '+'
	text  string
	aLine int
	bLine int
}

func hunkRange(ops []op, start, end int) (aStart, bStart, aLen, bLen int) {
	aStart, bStart = -1, -1
	for _, o := range ops[start:end] {
		if o.kind != '+' {
			aLen++
			if aStart < 0 {
				aStart = o.aLine
			}
		}
		if o.kind != '-' {
			bLen++
			if bStart < 0 {
				bStart = o.bLine
			}
		}
	}
	if aStart < 0 {
		aStart = ops[start].aLine
	}
	if bStart < 0 {
		bStart = ops[start].bLine
	}
	return aStart, bStart, aLen, bLen
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffLines computes a line diff with an LCS table (manifests are small).
func diffLines(a, b []string) []op {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			ops = append(ops, op{kind: ' ', text: a[i], aLine: i + 1, bLine: j + 1})
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{kind: '-', text: a[i], aLine: i + 1, bLine: j + 1})
			i++
		default:
			ops = append(ops, op{kind: '+', text: b[j], aLine: i + 1, bLine: j + 1})
			j++
		}
	}
	return ops
}
