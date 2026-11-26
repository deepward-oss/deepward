# Vulnerability data licenses

Deepward is MIT-licensed, but the *data* it reads has its own licenses.
Deepward only enables advisory sources whose data may be used commercially.
The `dependency-vulnerabilities` analyzer refuses any source whose declared
SPDX license is not in `allowedDataLicenses` (default: `CC-BY-4.0`,
`CC0-1.0`, `Apache-2.0`, `MIT`, `BSD-3-Clause`, `ODC-By-1.0`). Every report
credits the sources it used: the HTML footer, `dataSources` in JSON, and the
SARIF run properties.

| Source (plugin) | Data | License | Attribution | Enabled by default |
|-----------------|------|---------|-------------|--------------------|
| `osv` | OSV.dev API (aggregates GHSA, the Go vulnerability database and RustSec for Maven, NuGet, npm, Go and crates.io) | CC-BY-4.0 | OSV.dev and its upstream databases | yes |
| `osv-offline` | OSV.dev exports (`<Ecosystem>/all.zip`) | CC-BY-4.0 | OSV.dev and its upstream databases | yes (used only when a local copy exists) |
| `ghsa` | GitHub Advisory Database REST API | CC-BY-4.0 | GitHub Advisory Database | no |

## Review checklist

Review this list when you add a source, or at least once a year:

1. Read the source's data license, not just the license of its software.
   For example, Trivy's code is Apache-2.0, but some of its vulnerability
   feeds are restricted to non-commercial use.
2. For aggregators (OSV.dev), check the licenses of the upstream feeds that
   cover the ecosystems we query: GHSA (CC-BY-4.0) for Maven, NuGet and npm,
   the Go vulnerability database (CC-BY-4.0) for Go, and RustSec (CC0-1.0)
   for crates.io.
3. Make sure the license allows commercial use and redistribution of
   derived reports, and note any attribution requirement.
4. Declare the SPDX ID in the plugin's `DataLicense()` and add a row to the
   table above (the doc test fails for any registered source missing here).
5. If attribution is required, put it in `DataLicense().Attribution`. It is
   rendered in every report.
