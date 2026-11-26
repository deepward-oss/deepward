// Package textedit applies byte-span edits to files and renders unified
// diffs. Edits replace only their span, so formatting, comments, line
// endings and byte-order marks outside the span are preserved exactly.
package textedit

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// Apply applies edits (all for the same file) to content. Edits must not
// overlap; an edit with Expected set fails if the current bytes differ.
func Apply(content []byte, edits []sdk.TextEdit) ([]byte, error) {
	sorted := append([]sdk.TextEdit{}, edits...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	var out bytes.Buffer
	pos := 0
	for _, e := range sorted {
		if e.Start < 0 || e.End < e.Start || e.End > len(content) {
			return nil, fmt.Errorf("%s: edit [%d,%d) is outside the file (%d bytes)", e.Path, e.Start, e.End, len(content))
		}
		if e.Start < pos {
			return nil, fmt.Errorf("%s: edit [%d,%d) overlaps the previous edit", e.Path, e.Start, e.End)
		}
		if e.Expected != "" && string(content[e.Start:e.End]) != e.Expected {
			return nil, fmt.Errorf("%s: expected %q at [%d,%d), found %q", e.Path, e.Expected, e.Start, e.End, content[e.Start:e.End])
		}
		out.Write(content[pos:e.Start])
		out.WriteString(e.Replacement)
		pos = e.End
	}
	out.Write(content[pos:])
	return out.Bytes(), nil
}

// ByFile groups edits by path, keeping input order within a file.
func ByFile(edits []sdk.TextEdit) map[string][]sdk.TextEdit {
	out := map[string][]sdk.TextEdit{}
	for _, e := range edits {
		out[e.Path] = append(out[e.Path], e)
	}
	return out
}

// LineEnding returns "\r\n" if the content uses CRLF line endings.
func LineEnding(content []byte) string {
	if bytes.Contains(content, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

// LineStart returns the offset of the start of the line containing off.
func LineStart(content []byte, off int) int {
	if off > len(content) {
		off = len(content)
	}
	return bytes.LastIndexByte(content[:off], '\n') + 1
}

// Indentation returns the leading whitespace of the line containing off.
func Indentation(content []byte, off int) string {
	start := LineStart(content, off)
	end := start
	for end < len(content) && (content[end] == ' ' || content[end] == '\t') {
		end++
	}
	return string(content[start:end])
}
