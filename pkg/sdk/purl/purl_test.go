package purl

import (
	"errors"
	"testing"
)

func TestParseAndString(t *testing.T) {
	cases := []struct {
		in, out             string
		typ, ns, name, vers string
	}{
		{"pkg:maven/org.typelevel/cats-core_2.13@2.10.0", "", "maven", "org.typelevel", "cats-core_2.13", "2.10.0"},
		{"pkg:nuget/Newtonsoft.Json@13.0.1", "pkg:nuget/newtonsoft.json@13.0.1", "nuget", "", "newtonsoft.json", "13.0.1"},
		{"pkg:maven/org.apache.logging.log4j/log4j-core", "", "maven", "org.apache.logging.log4j", "log4j-core", ""},
		{"pkg:MAVEN/a/b/c@1?type=jar&classifier=sources#sub/path", "pkg:maven/a/b/c@1?classifier=sources&type=jar#sub/path", "maven", "a/b", "c", "1"},
		{"pkg:maven/g/a@1.0%2Bbuild", "pkg:maven/g/a@1.0+build", "maven", "g", "a", "1.0+build"},
	}
	for _, c := range cases {
		p, err := Parse(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if p.Type != c.typ || p.Namespace != c.ns || p.Name != c.name || p.Version != c.vers {
			t.Errorf("%s: got %+v", c.in, p)
		}
		want := c.out
		if want == "" {
			want = c.in
		}
		if got := p.String(); got != want {
			t.Errorf("String(%s) = %s, want %s", c.in, got, want)
		}
		back, err := Parse(p.String())
		if err != nil || back.String() != p.String() {
			t.Errorf("round trip %s: %v %v", c.in, back, err)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{"maven/a/b", "pkg:maven", "pkg:/a", "pkg:maven/", "pkg:maven/a/b@%zz", "pkg:maven/a/%zz", "pkg:maven/%zz/b", "pkg:maven/a/b?x=%zz"} {
		if _, err := Parse(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) err = %v, want ErrInvalid", s, err)
		}
	}
}

func TestMustParsePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic")
		}
	}()
	MustParse("nope")
}

func TestHelpers(t *testing.T) {
	p := New("MAVEN", "org.typelevel", "cats-core_2.13", "2.9.0")
	if p.Type != "maven" {
		t.Errorf("type not normalized: %s", p.Type)
	}
	if got := p.WithoutVersion().String(); got != "pkg:maven/org.typelevel/cats-core_2.13" {
		t.Errorf("WithoutVersion = %s", got)
	}
	if got := p.WithVersion("2.10.0").Version; got != "2.10.0" {
		t.Errorf("WithVersion = %s", got)
	}
	if got := p.Coordinates(); got != "org.typelevel:cats-core_2.13" {
		t.Errorf("Coordinates = %s", got)
	}
	if got := New(TypeNPM, "@app", "lib", "1").Coordinates(); got != "@app/lib" {
		t.Errorf("npm coordinates: %s", got)
	}
	if got := New("nuget", "", "Serilog", "3.0.0").Coordinates(); got != "serilog" {
		t.Errorf("nuget Coordinates = %s", got)
	}
	if p.ScalaBase() != "cats-core" || p.ScalaSuffix() != "_2.13" {
		t.Errorf("scala split: %s %s", p.ScalaBase(), p.ScalaSuffix())
	}
	if got := MustParse("pkg:maven/a/b@1").String(); got != "pkg:maven/a/b@1" {
		t.Errorf("MustParse = %s", got)
	}
}

func TestSplitScala(t *testing.T) {
	cases := map[string][2]string{
		"cats-core_2.13":        {"cats-core", "_2.13"},
		"cats-core_3":           {"cats-core", "_3"},
		"zio_2.12":              {"zio", "_2.12"},
		"sbt-scalafmt_2.12_1.0": {"sbt-scalafmt", "_2.12_1.0"},
		"scalajs-dom_sjs1_2.13": {"scalajs-dom", "_sjs1_2.13"},
		"cats-core_native0.4_3": {"cats-core", "_native0.4_3"},
		"jackson-databind":      {"jackson-databind", ""},
		"foo_2.9":               {"foo_2.9", ""},
		"lib_3x":                {"lib_3x", ""},
	}
	for in, want := range cases {
		b, s := SplitScala(in)
		if b != want[0] || s != want[1] {
			t.Errorf("SplitScala(%s) = %s,%s want %v", in, b, s, want)
		}
	}
}
