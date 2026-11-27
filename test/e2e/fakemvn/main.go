// Command fakemvn imitates `mvn` for end-to-end tests: it prints a version
// and writes a recorded dependency tree to -DoutputFile, with the versions
// of the dependencies the project's POMs declare (dependencies and
// dependencyManagement, ${property} references resolved) so that edits made
// by `deepward fix` are visible to the verification rebuild.
//
// FAKEMVN_COUNTER names a file that gets one line per call ("version" or
// "tree"); FAKEMVN_DELAY (a Go duration) makes every tree call that slow.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
			count("version")
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
	count("tree")
	if d, err := time.ParseDuration(os.Getenv("FAKEMVN_DELAY")); err == nil {
		time.Sleep(d)
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

// count appends a line to the FAKEMVN_COUNTER file, if set.
func count(kind string) {
	path := os.Getenv("FAKEMVN_COUNTER")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fail("%v", err)
	}
	defer f.Close()
	_, _ = fmt.Fprintln(f, kind)
}

var (
	dependencyRe = regexp.MustCompile(`(?s)<dependency>(.*?)</dependency>`)
	propertiesRe = regexp.MustCompile(`(?s)<properties>(.*?)</properties>`)
	elementRe    = regexp.MustCompile(`<([\w.\-]+)>([^<]*)</([\w.\-]+)>`)
	propertyRef  = regexp.MustCompile(`\$\{([^}]+)\}`)
)

// projectVersions reads the versions the fixture's POMs declare, then the
// versions of log4j, jackson and snakeyaml the maven-multi fixture aligns
// through properties.
func projectVersions(root string) map[string]string {
	out := declaredVersions(root)
	for k, v := range alignedVersions(root) {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// declaredVersions reads every <dependency> with a version in the POMs
// under root, resolving ${property} from the <properties> of all POMs.
func declaredVersions(root string) map[string]string {
	var poms []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == "target" {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "pom.xml" {
			b, _ := os.ReadFile(p)
			poms = append(poms, string(b))
		}
		return nil
	})
	props := map[string]string{}
	for _, pom := range poms {
		for _, block := range propertiesRe.FindAllStringSubmatch(pom, -1) {
			for _, m := range elementRe.FindAllStringSubmatch(block[1], -1) {
				if m[1] == m[3] {
					props[m[1]] = strings.TrimSpace(m[2])
				}
			}
		}
	}
	out := map[string]string{}
	for _, pom := range poms {
		for _, dep := range dependencyRe.FindAllStringSubmatch(pom, -1) {
			fields := map[string]string{}
			for _, m := range elementRe.FindAllStringSubmatch(dep[1], -1) {
				if m[1] == m[3] {
					fields[m[1]] = strings.TrimSpace(m[2])
				}
			}
			v := propertyRef.ReplaceAllStringFunc(fields["version"], func(ref string) string {
				return props[ref[2:len(ref)-1]]
			})
			if fields["groupId"] != "" && fields["artifactId"] != "" && v != "" {
				out[fields["groupId"]+":"+fields["artifactId"]] = v
			}
		}
	}
	return out
}

// alignedVersions reads the versions of the maven-multi fixture.
func alignedVersions(root string) map[string]string {
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
