// Package all registers every built-in plugin. Import it for side effects.
package all

import (
	_ "github.com/deepward-oss/deepward/plugins/advisory/ghsa"           // advisory source
	_ "github.com/deepward-oss/deepward/plugins/advisory/osv"            // advisory source
	_ "github.com/deepward-oss/deepward/plugins/advisory/osvoffline"     // advisory source
	_ "github.com/deepward-oss/deepward/plugins/analyzer/depvuln"        // analyzer
	_ "github.com/deepward-oss/deepward/plugins/analyzer/outdated"       // analyzer
	_ "github.com/deepward-oss/deepward/plugins/analyzer/sarifimport"    // analyzer
	_ "github.com/deepward-oss/deepward/plugins/ecosystem/cargo"         // ecosystem
	_ "github.com/deepward-oss/deepward/plugins/ecosystem/golang"        // ecosystem
	_ "github.com/deepward-oss/deepward/plugins/ecosystem/maven"         // ecosystem
	_ "github.com/deepward-oss/deepward/plugins/ecosystem/npm"           // ecosystem
	_ "github.com/deepward-oss/deepward/plugins/ecosystem/nuget"         // ecosystem
	_ "github.com/deepward-oss/deepward/plugins/ecosystem/sbt"           // ecosystem
	_ "github.com/deepward-oss/deepward/plugins/fact/sbom"               // fact producer
	_ "github.com/deepward-oss/deepward/plugins/outputformat/cyclonedx"  // output format
	_ "github.com/deepward-oss/deepward/plugins/outputformat/sarif"      // output format
	_ "github.com/deepward-oss/deepward/plugins/platform/github"         // platform
	_ "github.com/deepward-oss/deepward/plugins/platform/gitlab"         // platform
	_ "github.com/deepward-oss/deepward/plugins/reporter/html"           // reporter
	_ "github.com/deepward-oss/deepward/plugins/reporter/json"           // reporter
	_ "github.com/deepward-oss/deepward/plugins/reporter/sarif"          // reporter
	_ "github.com/deepward-oss/deepward/plugins/toolresolver/configpath" // tool resolver
	_ "github.com/deepward-oss/deepward/plugins/toolresolver/container"  // tool resolver
	_ "github.com/deepward-oss/deepward/plugins/toolresolver/download"   // tool resolver
	_ "github.com/deepward-oss/deepward/plugins/toolresolver/path"       // tool resolver
	_ "github.com/deepward-oss/deepward/plugins/toolresolver/wrapper"    // tool resolver
)
