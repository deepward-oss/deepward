package textedit

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

func TestApply(t *testing.T) {
	src := []byte("\ufeff<version>2.15.0</version>\r\n<x>1</x>\r\n")
	start := bytes.Index(src, []byte("2.15.0"))
	out, err := Apply(src, []sdk.TextEdit{
		{Path: "pom.xml", Start: start, End: start + 6, Replacement: "2.17.1", Expected: "2.15.0"},
		{Path: "pom.xml", Start: len(src), End: len(src), Replacement: "<!-- end -->\r\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "\ufeff<version>2.17.1</version>\r\n<x>1</x>\r\n<!-- end -->\r\n" {
		t.Errorf("out: %q", out)
	}
	cases := map[string][]sdk.TextEdit{
		"outside":  {{Start: 0, End: 999}},
		"negative": {{Start: -1, End: 0}},
		"reversed": {{Start: 5, End: 4}},
		"overlap":  {{Start: 0, End: 5}, {Start: 3, End: 6}},
		"expected": {{Start: 0, End: 3, Expected: "nope"}},
	}
	for name, edits := range cases {
		if _, err := Apply(src, edits); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
	same, _ := Apply([]byte("ab"), []sdk.TextEdit{{Start: 1, End: 1, Replacement: "x"}, {Start: 1, End: 1, Replacement: "y"}})
	if string(same) != "axyb" {
		t.Errorf("insertions at one point keep their order: %q", same)
	}
	if len(ByFile([]sdk.TextEdit{{Path: "a"}, {Path: "b"}, {Path: "a"}})["a"]) != 2 {
		t.Error("ByFile")
	}
}

// TestApplyOnlyChangesTheSpan is a property test: bytes outside the edited
// span are identical before and after.
func TestApplyOnlyChangesTheSpan(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for n := 0; n < 500; n++ {
		content := make([]byte, 1+r.Intn(200))
		for i := range content {
			content[i] = "ab\r\n <>\"x\ufeff"[r.Intn(9)]
		}
		start := r.Intn(len(content))
		end := start + r.Intn(len(content)-start+1)
		repl := strings.Repeat("Z", r.Intn(10))
		out, err := Apply(content, []sdk.TextEdit{{Start: start, End: end, Replacement: repl}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out[:start], content[:start]) || !bytes.Equal(out[start+len(repl):], content[end:]) {
			t.Fatalf("bytes outside [%d,%d) changed", start, end)
		}
	}
}

func FuzzApply(f *testing.F) {
	f.Add([]byte("<version>1.0</version>"), 9, 12, "2.0")
	f.Fuzz(func(t *testing.T, content []byte, start, end int, repl string) {
		out, err := Apply(content, []sdk.TextEdit{{Start: start, End: end, Replacement: repl}})
		if err != nil {
			return
		}
		if len(out) != len(content)-(end-start)+len(repl) {
			t.Fatalf("length mismatch")
		}
		if !bytes.Equal(out[:start], content[:start]) || !bytes.Equal(out[start+len(repl):], content[end:]) {
			t.Fatalf("bytes outside the span changed")
		}
	})
}

func TestHelpers(t *testing.T) {
	if LineEnding([]byte("a\r\nb")) != "\r\n" || LineEnding([]byte("a\nb")) != "\n" {
		t.Error("LineEnding")
	}
	src := []byte("a\n    <b>\n")
	if LineStart(src, 6) != 2 || Indentation(src, 7) != "    " || LineStart(src, 999) != len(src) {
		t.Error("LineStart/Indentation")
	}
}

func TestUnified(t *testing.T) {
	before := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n"
	after := strings.Replace(strings.Replace(before, "2\n", "two\n", 1), "14\n", "fourteen\n", 1)
	d := Unified("pom.xml", []byte(before), []byte(after))
	want := "--- a/pom.xml\n+++ b/pom.xml\n@@ -1,5 +1,5 @@\n 1\n-2\n+two\n 3\n 4\n 5\n@@ -11,5 +11,5 @@\n 11\n 12\n 13\n-14\n+fourteen\n 15\n"
	if d != want {
		t.Errorf("diff:\n%s\nwant:\n%s", d, want)
	}
	if Unified("x", []byte("a"), []byte("a")) != "" {
		t.Error("equal")
	}
	if d := Unified("x", []byte("a"), []byte("b")); !strings.Contains(d, "\\ No newline at end of file") {
		t.Errorf("no trailing newline: %s", d)
	}
	if d := Unified("x", nil, []byte("new\n")); !strings.Contains(d, "@@ -0,0 +1,1 @@\n+new") && !strings.Contains(d, "+new") {
		t.Errorf("new file: %s", d)
	}
	close := Unified("x", []byte("1\n2\n3\n4\n5\n6\n7\n8\n"), []byte("1\nB\n3\n4\n5\nF\n7\n8\n"))
	if strings.Count(close, "@@") != 2 {
		t.Errorf("nearby changes share one hunk: %s", close)
	}
}
