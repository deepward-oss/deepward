package strictyaml

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

type inner struct {
	Timeout sdk.Duration `yaml:"timeout"`
	Enabled *bool        `yaml:"enabled"`
}

type Base struct {
	Shared string `yaml:"shared"`
}

type cfg struct {
	Base    `yaml:",inline"`
	FailOn  sdk.Severity         `yaml:"failOn"`
	Exclude []string             `yaml:"exclude"`
	Plugins map[string]yaml.Node `yaml:"plugins"`
	Inner   inner                `yaml:"inner"`
	Ptr     *inner               `yaml:"ptr"`
	Map     map[string]inner     `yaml:"map"`
	List    []inner              `yaml:"list"`
	Count   int                  `yaml:"count"`
	Any     any                  `yaml:"any"`
	Skip    string               `yaml:"-"`
	NoTag   string
}

func decode(t *testing.T, src string) (cfg, error) {
	t.Helper()
	doc, err := Parse([]byte(src), "deepward.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var c cfg
	err = Decode(doc, &c, "", "deepward.yaml")
	return c, err
}

func TestDecodeValid(t *testing.T) {
	c, err := decode(t, `
shared: s
failOn: high
exclude: [examples/, tmp]
plugins:
  osv:
    timeout: 30s
inner: {timeout: 1m, enabled: false}
ptr: {timeout: 2s}
map:
  a: {timeout: 3s}
list:
  - timeout: 4s
count: 3
any: {x: [1, 2]}
notag: hello
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Shared != "s" || c.FailOn != sdk.SeverityHigh || len(c.Exclude) != 2 || c.Inner.Timeout.Std().String() != "1m0s" ||
		*c.Inner.Enabled || c.Ptr.Timeout.Std().Seconds() != 2 || c.Map["a"].Timeout.Std().Seconds() != 3 ||
		c.List[0].Timeout.Std().Seconds() != 4 || c.Count != 3 || c.Any == nil || c.NoTag != "hello" {
		t.Errorf("decoded: %+v", c)
	}
	node := c.Plugins["osv"]
	var sub inner
	if err := Decode(&node, &sub, "plugins.osv", "deepward.yaml"); err != nil || sub.Timeout.Std().Seconds() != 30 {
		t.Errorf("sub decode: %+v %v", sub, err)
	}
}

func TestDecodeErrorsCarryPathAndLine(t *testing.T) {
	_, err := decode(t, `failon: high
inner:
  timeout: fast
  extra: 1
count: abc
exclude: notalist
map: [1]
list:
  - timeout: [1]
ptr: 5
`)
	var es Errors
	if !errors.As(err, &es) {
		t.Fatalf("err = %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`deepward.yaml:1: failon: unknown key "failon" (did you mean "failOn"?)`,
		`deepward.yaml:3: inner.timeout: invalid duration "fast"`,
		`deepward.yaml:4: inner.extra: unknown key "extra"`,
		`deepward.yaml:5: count: cannot unmarshal !!str ` + "`abc`" + ` into int`,
		`deepward.yaml:6: exclude: expected a list, got a scalar`,
		`deepward.yaml:7: map: expected a mapping, got a sequence`,
		`deepward.yaml:9: list[0].timeout: expected a scalar value, got a sequence`,
		`deepward.yaml:10: ptr: expected a mapping, got a scalar`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
}

func TestDecodeMisc(t *testing.T) {
	if err := Decode(nil, cfg{}, "", ""); err == nil {
		t.Error("non-pointer must fail")
	}
	var c cfg
	if err := Decode(nil, &c, "", ""); err != nil {
		t.Error("nil node is a no-op")
	}
	if _, err := Parse([]byte("a: [1"), "f.yaml"); err == nil || !strings.HasPrefix(err.Error(), "f.yaml: ") {
		t.Errorf("parse error: %v", err)
	}
	// null resets, aliases resolve, scalar into int with mapping fails.
	c2, err := decode(t, "base: &b {timeout: 5s}\n")
	if err == nil || !strings.Contains(err.Error(), `unknown key "base"`) {
		t.Errorf("alias doc: %v %+v", err, c2)
	}
	c3, err := decode(t, "inner: &x {timeout: 5s}\nptr: *x\ncount: ~\n")
	if err != nil || c3.Ptr.Timeout.Std().Seconds() != 5 {
		t.Errorf("alias: %v %+v", err, c3)
	}
	_, err = decode(t, "count: {a: 1}\n")
	if err == nil || !strings.Contains(err.Error(), "expected a int value, got a mapping") {
		t.Errorf("mapping into int: %v", err)
	}
	_, err = decode(t, "map: {[1]: {}}\n")
	if err == nil {
		t.Error("non-scalar map key must fail")
	}
	_, err = decode(t, "any: !!binary zz\n")
	if err == nil {
		t.Error("bad binary into any must fail")
	}
	e := &Error{Msg: "m"}
	if e.Error() != "m" || (&Error{Line: 2, Msg: "m"}).Error() != "line 2: m" {
		t.Error("error formatting")
	}
	if suggest("zzzzzz", []string{"failOn"}) != "" {
		t.Error("no suggestion for distant keys")
	}
}
