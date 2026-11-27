# Spec Delta

## ADDED Requirements

### Requirement: Title patterns per kind of change request
The system SHALL take a change request's title from `fix.title`, which holds one pattern per kind: `security` (a vulnerability fixed by one package change), `pin` (a vulnerability fixed by pinning a transitive version; falls back to `security`), `update` (one outdated dependency) and `group` (several package changes in one request). A kind without a pattern SHALL keep Deepward's default title.

#### Scenario: Conventional Commits titles
- **WHEN** `fix.title.security` is `fix(deps): bump {name} from {from} to {to}` and a request upgrades `org.apache.kafka:kafka-clients` 4.4.0 → 4.5.0 in a root project
- **THEN** its title is `fix(deps): bump kafka-clients from 4.4.0 to 4.5.0`

#### Scenario: Pin falls back to security
- **WHEN** only `fix.title.security` is set and a request pins `org.apache.zookeeper:zookeeper` to 3.9.5
- **THEN** its title is rendered from the `security` pattern

#### Scenario: Pin pattern
- **WHEN** `fix.title.pin` is `fix(deps): pin {name} to {to}` and a request pins `zookeeper` to 3.9.5
- **THEN** its title is `fix(deps): pin zookeeper to 3.9.5`

#### Scenario: Kind without a pattern
- **WHEN** only `fix.title.update` is set and a request fixes a vulnerability
- **THEN** the vulnerability request keeps the default title, for example `Deepward: fix vulnerabilities: lz4-java 1.11.2 → 1.11.4`

### Requirement: Title placeholders
Title patterns SHALL support `{package}` (full package name), `{name}` (short name: the part after the last `:` or `/`), `{from}` and `{to}` (versions), `{count}` (number of package changes), `{severity}` (highest severity of the fixed findings), `{project}` (project directory, empty at the repository root), `{ecosystem}`, `{group}` (group name) and `{update}` (patch, minor or major). Patterns SHALL NOT support conditions or loops.

#### Scenario: Group request
- **WHEN** `fix.title.group` is `chore(deps): update {group} ({count} packages)` and the `jackson` group rule collects three package changes
- **THEN** the title is `chore(deps): update jackson (3 packages)`

#### Scenario: Placeholders without a value
- **WHEN** a `group` pattern uses `{from}` and `{to}`
- **THEN** they render empty, and the surrounding whitespace is collapsed

### Requirement: Empty project in titles
When `{project}` is empty, the system SHALL remove it together with brackets left empty around it and a preposition directly before it (`in`, `for`, `at`), and SHALL collapse repeated spaces.

#### Scenario: Scope in brackets at the root
- **WHEN** the pattern is `fix({project}): {name} {from} → {to}` and the project is at the repository root
- **THEN** the title is `fix: kafka-clients 4.4.0 → 4.5.0`

#### Scenario: Preposition at the root
- **WHEN** the pattern is `Update {name} {from} → {to} in {project}` and the project is at the repository root
- **THEN** the title is `Update grpc-stub 1.85.1 → 1.86.0`

#### Scenario: Project outside the root
- **WHEN** the same pattern is used for a project in `services/api`
- **THEN** the title is `Update grpc-stub 1.85.1 → 1.86.0 in services/api`

### Requirement: Markers in custom titles
Whatever the pattern, the system SHALL put `[unverified] ` in front of the title of a request with unverified changes and append ` (not built)` to the title of a request whose findings are all in packages that are not built.

#### Scenario: Unverified pin
- **WHEN** `fix.title.pin` is `fix(deps): pin {name} to {to}` and the pin of `lz4-java` to 1.11.4 is published as unverified
- **THEN** the title is `[unverified] fix(deps): pin lz4-java to 1.11.4`

### Requirement: Commit subject follows the title
When the title of a request comes from a pattern, the system SHALL use the rendered title, markers included, as the subject of its commit. Without a pattern, the commit subject SHALL stay the default title without the `Deepward: ` prefix.

#### Scenario: Conventional commit
- **WHEN** a request is titled `fix(deps): bump kafka-clients from 4.4.0 to 4.5.0` from a pattern
- **THEN** its commit subject is `fix(deps): bump kafka-clients from 4.4.0 to 4.5.0`

### Requirement: Title pattern validation
The system SHALL reject, when loading the configuration, an unknown key under `fix.title` and an unknown placeholder in a pattern, naming the key and the placeholder. A pattern that renders an empty title SHALL be rejected. Rendered titles SHALL be cut to 255 characters.

#### Scenario: Unknown placeholder
- **WHEN** `fix.title.update` is `chore(deps): bump {version}`
- **THEN** configuration validation fails with an error naming `fix.title.update` and `{version}`

#### Scenario: Unknown kind
- **WHEN** `fix.title` contains a key `bugfix`
- **THEN** configuration validation fails with an error naming the unknown key

#### Scenario: Pattern without text
- **WHEN** `fix.title.security` is `{project}` and the project is at the repository root
- **THEN** configuration validation rejects the pattern because it can render an empty title
