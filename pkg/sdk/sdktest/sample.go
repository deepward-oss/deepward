package sdktest

import (
	"time"

	"github.com/deepward-oss/deepward/pkg/sdk"
)

// SampleResult returns a normalized scan result exercising every report
// section: vulnerabilities (direct, transitive, no fix), an outdated
// dependency, a generic code issue, a suppressed finding, skipped and failed
// projects, a failed plugin, warnings and data sources. Reporters use it for
// golden-file tests.
func SampleResult() *sdk.ScanResult {
	at := time.Date(2026, 11, 26, 9, 30, 0, 0, time.UTC)
	vuln := func(rule string, sev sdk.Severity, purl string, direct bool, props map[string]any, aliases ...string) sdk.Finding {
		return sdk.Finding{
			Analyzer: "dependency-vulnerabilities", Rule: rule, Kind: sdk.KindVulnerability, Severity: sev, Title: "Issue in " + purl,
			Message: purl + " is affected by " + rule, Project: "maven:.", Aliases: aliases,
			Locations: []sdk.Location{{File: &sdk.FileLocation{Path: "core/pom.xml", StartLine: 14},
				Package: &sdk.PackageLocation{PURL: purl, Direct: direct, Scope: "compile",
					Paths: [][]string{{"pkg:maven/org.springframework.boot/spring-boot-starter-web@2.5.0", purl}}}}},
			References: []sdk.Reference{{Type: "ADVISORY", URL: "https://osv.dev/vulnerability/" + rule}},
			Properties: props,
		}
	}
	res := &sdk.ScanResult{
		SchemaVersion: sdk.SchemaVersion,
		Tool:          sdk.ToolInfo{Name: "deepward", Version: "1.0.0", Commit: "abc1234", SDKVersion: sdk.SDKVersion},
		Run: sdk.RunInfo{StartedAt: at, FinishedAt: at.Add(42 * time.Second), Root: "/builds/team/billing",
			RepoURL: "https://git.acme.local/team/billing", Commit: "0a1b2c3", Branch: "main", TargetBranch: "main", CI: "gitlab-ci"},
		Projects: []sdk.ProjectResult{
			{Project: sdk.Project{ID: "maven:.", Ecosystem: "maven", Dir: ".", Manifests: []string{"pom.xml", "core/pom.xml"}}, Status: sdk.ProjectOK},
			{Project: sdk.Project{ID: "sbt:backend", Ecosystem: "sbt", Dir: "backend", Manifests: []string{"backend/build.sbt"}}, Status: sdk.ProjectSkipped,
				Reason: "tool-not-found: sbt (tried: config-path, wrapper, path)"},
			{Project: sdk.Project{ID: "nuget:client", Ecosystem: "nuget", Dir: "client", Manifests: []string{"client/App.csproj"}}, Status: sdk.ProjectFailed,
				Reason: "dotnet restore failed", Output: []string{"error NU1101: Unable to find package Acme.Internal"}},
		},
		Findings: []sdk.Finding{
			vuln("CVE-2021-44228", sdk.SeverityCritical, "pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1", true, map[string]any{
				"package": map[string]any{"name": "org.apache.logging.log4j:log4j-core", "version": "2.14.1", "ecosystem": "Maven"},
				"cvss":    map[string]any{"score": 10.0, "vector": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H", "version": "3"},
				"fixedIn": "2.17.1", "affected": []string{">=2.0-beta9, <2.15.0"}, "sources": []string{"ghsa", "osv"},
				"recommendation": "upgrade org.apache.logging.log4j:log4j-core to 2.17.1", "published": "2021-12-10",
				"details": "Apache Log4j2 JNDI features <script>alert(1)</script> do not protect against attacker controlled LDAP.",
			}, "GHSA-jfh8-c2jp-5v3q"),
			vuln("CVE-2020-36518", sdk.SeverityHigh, "pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.12.1", false, map[string]any{
				"package": map[string]any{"name": "com.fasterxml.jackson.core:jackson-databind", "version": "2.12.1", "ecosystem": "Maven"},
				"fixedIn": "2.12.6.1", "ancestors": []string{"org.springframework.boot:spring-boot-starter-web@2.5.0"},
			}),
			vuln("GHSA-noff-ix00-0000", sdk.SeverityMedium, "pkg:maven/junit/junit@4.13.1", true, map[string]any{"noFixAvailable": true}),
			{Analyzer: "outdated-dependencies", Rule: "outdated", Kind: sdk.KindOutdated, Severity: sdk.SeverityNone, Project: "maven:.",
				Message:   "org.yaml:snakeyaml 1.33 → 2.3 available",
				Locations: []sdk.Location{{Package: &sdk.PackageLocation{PURL: "pkg:maven/org.yaml/snakeyaml@1.33", Direct: true}}},
				Properties: map[string]any{"package": map[string]any{"name": "org.yaml:snakeyaml"}, "current": "1.33", "minor": "", "major": "2.3",
					"latest": "2.3", "updateType": "major"}},
			{Analyzer: "sarif-import", Tool: "semgrep", Rule: "java.lang.security.audit.xxe", Kind: sdk.KindCodeIssue, Severity: sdk.SeverityHigh,
				Project: "", Message: "XML parser is vulnerable to XXE",
				Locations: []sdk.Location{{File: &sdk.FileLocation{Path: "core/src/main/java/Parser.java", StartLine: 42, StartColumn: 5}}}},
			{Analyzer: "dependency-vulnerabilities", Rule: "CVE-2022-1471", Kind: sdk.KindVulnerability, Severity: sdk.SeverityHigh, Project: "maven:.",
				Message:     "snakeyaml 1.33 is affected by CVE-2022-1471",
				Locations:   []sdk.Location{{Package: &sdk.PackageLocation{PURL: "pkg:maven/org.yaml/snakeyaml@1.33", Direct: true}}},
				Suppression: &sdk.Suppression{Rule: "CVE-2022-1471", Reason: "SnakeYAML Constructor is not used", Expires: "2027-01-01"}},
		},
		PluginRuns: []sdk.PluginRun{
			{Plugin: "dependency-vulnerabilities", Kind: sdk.KindAnalyzer, Project: "maven:.", Status: sdk.RunOK, Findings: 4},
			{Plugin: "license-check", Kind: sdk.KindAnalyzer, Project: "maven:.", Status: sdk.RunFailed, Reason: "plugin panicked: boom"},
		},
		Warnings: []sdk.Warning{{Code: "project-skipped", Project: "sbt:backend", Message: "tool-not-found: sbt"}},
		DataSources: []sdk.DataSource{
			{Name: "osv", License: "CC-BY-4.0", Attribution: "OSV.dev and its upstream databases"},
			{Name: "ghsa", License: "CC-BY-4.0", Attribution: "GitHub Advisory Database"},
		},
		Gate: sdk.Gate{Threshold: sdk.SeverityHigh, Failed: true, Count: 3},
	}
	res.Normalize()
	return res
}
