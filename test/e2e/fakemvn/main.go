// Command fakemvn imitates `mvn` for end-to-end tests: it prints a version
// and writes a recorded dependency tree to -DoutputFile, with the versions
// of log4j, jackson and snakeyaml taken from the project's POMs so that
// edits made by `deepward fix` are visible to the verification rebuild.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type node struct {
	GroupID    string  `json:"groupId"`
	ArtifactID string  `json:"artifactId"`
	Version    string  `json:"version"`
	Type       string  `json:"type"`
	Scope      string  `json:"scope"`
	Classifier string  `json:"classifier"`
	Optional   string  `json:"optional"`
	Children   []*node `json:"children,omitempty"`
}

func main() {
	pom := ""
	out := ""
	for i, a := range os.Args[1:] {
		if a == "-v" {
			fmt.Println("Apache Maven 3.9.9 (fake)")
			return
		}
		if a == "-f" && i+2 < len(os.Args) {
			pom = os.Args[i+2]
		}
		if v, ok := strings.CutPrefix(a, "-DoutputFile="); ok {
			out = v
		}
	}
	if out == "" {
		fail("unexpected invocation: %v", os.Args[1:])
	}
	data, err := os.ReadFile(os.Getenv("FAKEMVN_TREE"))
	if err != nil {
		fail("%v", err)
	}
	versions := projectVersions(filepath.Dir(pom))
	dec := json.NewDecoder(strings.NewReader(string(data)))
	f, err := os.Create(out)
	if err != nil {
		fail("%v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	for {
		var n node
		if err := dec.Decode(&n); err == io.EOF {
			break
		} else if err != nil {
			fail("%v", err)
		}
		apply(&n, versions)
		_ = enc.Encode(&n)
	}
}

func apply(n *node, versions map[string]string) {
	key := n.GroupID + ":" + n.ArtifactID
	if v, ok := versions[key]; ok && v != "" {
		n.Version = v
	}
	for _, c := range n.Children {
		apply(c, versions)
	}
}

// projectVersions reads the versions the fixture's POMs declare.
func projectVersions(root string) map[string]string {
	read := func(p string) string {
		b, _ := os.ReadFile(filepath.Join(root, p))
		return string(b)
	}
	prop := func(text, name string) string {
		m := regexp.MustCompile(`<` + regexp.QuoteMeta(name) + `>([^<]+)</`).FindStringSubmatch(text)
		if m == nil {
			return ""
		}
		return strings.TrimSpace(m[1])
	}
	parent := read("pom.xml")
	web := read("web/pom.xml")
	log4j := prop(parent, "log4j.version")
	snake := ""
	if m := regexp.MustCompile(`<artifactId>snakeyaml</artifactId>\s*<version>([^<]+)</version>`).FindStringSubmatch(web); m != nil {
		snake = m[1]
	}
	return map[string]string{
		"org.apache.logging.log4j:log4j-core":         log4j,
		"org.apache.logging.log4j:log4j-api":          log4j,
		"com.fasterxml.jackson.core:jackson-databind": prop(parent, "jackson.version"),
		"org.yaml:snakeyaml":                          snake,
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[ERROR] "+format+"\n", args...)
	os.Exit(1)
}
