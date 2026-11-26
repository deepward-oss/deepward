package domain

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
)

// poms is a fake repository: "group:artifact:version" → POM.
type poms map[string]string

func (p poms) fetch(calls *atomic.Int64) FetchPOM {
	return func(_ context.Context, g, a, v string) ([]byte, error) {
		calls.Add(1)
		s, ok := p[g+":"+a+":"+v]
		if !ok {
			return nil, errors.New("not found: " + g + ":" + a + ":" + v)
		}
		return []byte(s), nil
	}
}

func TestResolverEffectiveDependencies(t *testing.T) {
	repo := poms{
		"org.acme:parent:1": `<project>
  <groupId>org.acme</groupId><artifactId>parent</artifactId><version>1</version>
  <properties><lz4.version>1.11.2</lz4.version><zk.version>3.9.3</zk.version><bom.version>1</bom.version></properties>
  <dependencyManagement><dependencies>
    <dependency><groupId>org.apache.zookeeper</groupId><artifactId>zookeeper</artifactId><version>${zk.version}</version></dependency>
    <dependency><groupId>org.acme</groupId><artifactId>bom</artifactId><version>${bom.version}</version><type>pom</type><scope>import</scope></dependency>
  </dependencies></dependencyManagement>
  <dependencies>
    <dependency><groupId>org.slf4j</groupId><artifactId>slf4j-api</artifactId><version>2.0.0</version></dependency>
  </dependencies>
</project>`,
		"org.acme:bom:1": `<project><groupId>org.acme</groupId><artifactId>bom</artifactId><version>1</version>
  <dependencyManagement><dependencies>
    <dependency><groupId>org.xerial.snappy</groupId><artifactId>snappy-java</artifactId><version>1.1.10.9</version></dependency>
    <dependency><groupId>org.apache.zookeeper</groupId><artifactId>zookeeper</artifactId><version>0.0.1</version></dependency>
  </dependencies></dependencyManagement></project>`,
		"org.acme:lib:2": `<project>
  <parent><groupId>org.acme</groupId><artifactId>parent</artifactId><version>1</version></parent>
  <artifactId>lib</artifactId><version>2</version>
  <properties><lz4.version>1.11.4</lz4.version></properties>
  <dependencies>
    <dependency><groupId>at.yawk.lz4</groupId><artifactId>lz4-java</artifactId><version>${lz4.version}</version></dependency>
    <dependency><groupId>org.xerial.snappy</groupId><artifactId>snappy-java</artifactId></dependency>
    <dependency><groupId>org.apache.zookeeper</groupId><artifactId>zookeeper</artifactId></dependency>
    <dependency><groupId>${project.groupId}</groupId><artifactId>core</artifactId><version>${project.version}</version></dependency>
    <dependency><groupId>org.junit</groupId><artifactId>junit</artifactId><version>5</version><scope>test</scope></dependency>
    <dependency><groupId>org.opt</groupId><artifactId>opt</artifactId><version>1</version><optional>true</optional></dependency>
    <dependency><groupId>org.r</groupId><artifactId>ranged</artifactId><version>[1.0,2.0)</version></dependency>
    <dependency><groupId>org.u</groupId><artifactId>unknown</artifactId><version>${nope}</version></dependency>
  </dependencies>
</project>`,
	}
	var calls atomic.Int64
	r := &Resolver{Fetch: repo.fetch(&calls)}
	got, err := r.Dependencies(context.Background(), "org.acme", "lib", "2")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"at.yawk.lz4:lz4-java":           "1.11.4",   // own property overrides the parent's
		"org.xerial.snappy:snappy-java":  "1.1.10.9", // from the imported BOM
		"org.apache.zookeeper:zookeeper": "3.9.3",    // explicit inherited management beats the BOM
		"org.acme:core":                  "2",        // built-in properties
		"org.slf4j:slf4j-api":            "2.0.0",    // inherited dependency
		"org.r:ranged":                   "",         // ranges are not concrete
		"org.u:unknown":                  "",         // unresolved property
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dependencies:\n got  %v\n want %v", got, want)
	}
	before := calls.Load()
	if _, err := r.Dependencies(context.Background(), "org.acme", "lib", "2"); err != nil || calls.Load() != before {
		t.Errorf("results are memoized: %v, %d → %d fetches", err, before, calls.Load())
	}
	if _, err := r.Dependencies(context.Background(), "org.acme", "missing", "1"); err == nil {
		t.Error("a missing POM is an error")
	}
}

func TestResolverFailures(t *testing.T) {
	var calls atomic.Int64
	repo := poms{
		"g:orphan:1": `<project><parent><groupId>g</groupId><artifactId>gone</artifactId><version>1</version></parent><artifactId>orphan</artifactId></project>`,
		"g:loop:1":   `<project><parent><groupId>g</groupId><artifactId>loop</artifactId><version>1</version></parent><artifactId>loop</artifactId></project>`,
		"g:broken:1": `<project>`,
		"g:badbom:1": `<project><dependencyManagement><dependencies>
  <dependency><groupId>g</groupId><artifactId>nobom</artifactId><version>1</version><type>pom</type><scope>import</scope></dependency>
</dependencies></dependencyManagement>
<dependencies><dependency><groupId>x</groupId><artifactId>y</artifactId></dependency></dependencies></project>`,
	}
	r := &Resolver{Fetch: repo.fetch(&calls)}
	for _, a := range []string{"orphan", "loop", "broken"} {
		if _, err := r.Dependencies(context.Background(), "g", a, "1"); err == nil {
			t.Errorf("%s: want an error", a)
		}
	}
	got, err := r.Dependencies(context.Background(), "g", "badbom", "1")
	if err != nil || !reflect.DeepEqual(got, map[string]string{"x:y": ""}) {
		t.Errorf("an unreadable BOM leaves versions unknown: %v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := &Resolver{Fetch: func(ctx context.Context, _, _, _ string) ([]byte, error) { return nil, ctx.Err() }}
	if _, err := cancelled.Dependencies(ctx, "g", "a", "1"); err == nil || len(cancelled.models) != 0 {
		t.Error("cancellations are errors and are not memoized")
	}
}
