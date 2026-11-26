package domain

import (
	"strings"
	"testing"

	"github.com/deepward-oss/deepward/internal/textedit"
	"github.com/deepward-oss/deepward/pkg/sdk"
)

const buildSbt = `// "org.typelevel" %% "cats-core" % "0.0.1" in a comment is ignored
/* block "comment" /* nested */ still comment */
ThisBuild / scalaVersion := "2.13.14"
val x = 'c'
libraryDependencies ++= Seq(
  "org.typelevel" %% "cats-core" % "2.9.0",
  "io.circe" %% "circe-core" % Versions.circe,
  "io.circe" %% "circe-parser" % Versions.circe,
  "com.fasterxml.jackson.core" % "jackson-databind" % "2.12.1",
  "org.scalatest" %% "scalatest" % scalatestVersion % Test,
  "a" % "b" % s"${dyn}",
  "dup" % "lib" % dupVersion
)
`

const depsScala = `object Versions {
  val circe = "0.14.5"
}
object Other { val dupVersion = "1" }
object More { val dupVersion = "2" }
val notAString = 3
val desc = """multi
line "quoted" text"""
`

func plan(t *testing.T, to string, pin bool, org, name string) ([]sdk.TextEdit, *sdk.ManualChange) {
	t.Helper()
	return Plan([]File{{Path: "build.sbt", Data: []byte(buildSbt)}, {Path: "project/Dependencies.scala", Data: []byte(depsScala)}},
		sdk.PackageChange{To: to, Pin: pin}, org, name)
}

func TestPlanSbt(t *testing.T) {
	edits, m := plan(t, "2.13.0", false, "org.typelevel", "cats-core_2.13")
	if m != nil || len(edits) != 1 || edits[0].Expected != "2.9.0" || edits[0].Path != "build.sbt" {
		t.Fatalf("literal: %+v %+v", edits, m)
	}
	out, _ := textedit.Apply([]byte(buildSbt), edits)
	if !strings.Contains(string(out), `"org.typelevel" %% "cats-core" % "2.13.0",`) || strings.Contains(string(out), `"0.0.1" in a comment`) == false {
		t.Errorf("only the literal changes:\n%s", out)
	}
	edits, m = plan(t, "0.14.10", false, "io.circe", "circe-core_2.13")
	if m != nil || len(edits) != 1 || edits[0].Path != "project/Dependencies.scala" || edits[0].Expected != "0.14.5" {
		t.Fatalf("shared val: %+v %+v", edits, m)
	}
	out, _ = textedit.Apply([]byte(depsScala), edits)
	if !strings.Contains(string(out), `val circe = "0.14.10"`) {
		t.Errorf("val edit:\n%s", out)
	}
	if edits, _ := plan(t, "2.15.0", false, "com.fasterxml.jackson.core", "jackson-databind"); len(edits) != 1 {
		t.Error("single % with full name")
	}
	for _, c := range []struct{ org, name, want string }{
		{"org.scalatest", "scalatest_2.13", "not a single string val"},
		{"dup", "lib", "not a single string val"},
		{"a", "b", "not a literal or a val"},
		{"not", "declared", "not declared"},
		{"org.typelevel", "cats-core", "not declared"},
	} {
		_, m := plan(t, "9", false, c.org, c.name)
		if m == nil || !strings.Contains(m.Reason, c.want) || m.Suggestion == "" {
			t.Errorf("%s:%s: %+v", c.org, c.name, m)
		}
	}
	_, m = plan(t, "3.0.0", true, "com.fasterxml.jackson.core", "jackson-databind")
	if m == nil || m.Suggestion != `dependencyOverrides += "com.fasterxml.jackson.core" % "jackson-databind" % "3.0.0"` {
		t.Errorf("pin suggestion: %+v", m)
	}
	_, m = plan(t, "2.10.0", true, "org.typelevel", "cats-kernel_2.13")
	if m.Suggestion != `dependencyOverrides += "org.typelevel" %% "cats-kernel" % "2.10.0"` {
		t.Errorf("cross-version suggestion: %s", m.Suggestion)
	}
}

func TestTokenizeEdgeCases(t *testing.T) {
	toks := Tokenize([]byte(`s"a" "esc\"q" """unterminated`))
	if toks[0].Kind != TokInterpolated || toks[1].Text != `esc\"q` || toks[2].Text != "unterminated" {
		t.Errorf("tokens: %+v", toks)
	}
	toks = Tokenize([]byte(`'\n' 'sym "s" "unterminated`))
	if len(toks) != 3 || toks[0].Text != "sym" || toks[1].Text != "s" || toks[2].Text != "unterminated" {
		t.Errorf("char literal / unterminated: %+v", toks)
	}
	toks = Tokenize([]byte("/* never closed"))
	if len(toks) != 0 {
		t.Errorf("unterminated comment: %+v", toks)
	}
	toks = Tokenize([]byte(`a := (1, "x\`))
	if toks[len(toks)-1].Kind != TokString {
		t.Errorf("trailing backslash: %+v", toks)
	}
}

func FuzzTokenize(f *testing.F) {
	f.Add([]byte(buildSbt))
	f.Add([]byte(depsScala))
	f.Fuzz(func(t *testing.T, src []byte) {
		for _, tok := range Tokenize(src) {
			if tok.Start < 0 || tok.End > len(src) || tok.Start > tok.End {
				t.Fatalf("token span out of range: %+v (len %d)", tok, len(src))
			}
			if tok.Kind == TokString && string(src[tok.Start:tok.End]) != tok.Text {
				t.Fatalf("string text differs from its span")
			}
		}
	})
}
