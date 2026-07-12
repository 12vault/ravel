# Security Policy

## Supported versions

Ravel is pre-1.0. Security fixes are released for the current minor release line.

| Version | Supported |
| --- | --- |
| 0.2.x | Yes |
| < 0.2 | No |

The default branch receives fixes before the next patch release, but it is not a
stable release channel. This table must be updated whenever the active release
line changes.

## Reporting a vulnerability

Do not open a public issue or discussion for a suspected vulnerability. Use
GitHub's private vulnerability reporting for this repository instead. Include:

- the affected version or commit;
- a description of the vulnerability and its impact;
- minimal reproduction steps or a proof of concept;
- any known mitigations or suggested fix; and
- whether the report may be shared with upstream maintainers of an affected
  dependency.

Please avoid including real credentials, private source code, or unrelated
personal data. Use test fixtures and redacted logs where possible.

Maintainers should acknowledge a report within 3 business days. We aim to
provide an initial severity assessment and remediation plan within 7 business
days. Release timing depends on severity and fix complexity. We will coordinate
disclosure with the reporter and publish a GitHub security advisory when users
need to take action.

## Security model

Ravel is a local, audit-first development tool. Its normal build, update, query,
dashboard, corpus, and MCP operations do not call an LLM or make network
requests. The explicit `update-check` and `self-update` commands access GitHub
release services. Agent roles run through the host assistant under that host's
permission model.

Ravel processes untrusted repository content. A successful attack against the
tool could expose files outside the audited corpus, execute an unintended local
program, consume excessive resources, poison graph evidence, inject active
content into generated output, or compromise an update artifact.

### Trust boundaries and mitigations

| Threat | Current mitigation | Residual risk |
| --- | --- | --- |
| Secrets or credential material entering the graph | The scanner rejects environment files, key material, credential directories, ignored dependencies, archives, databases, and binary media before reading content. Audit output shows accepted and rejected paths. | Novel secret formats in otherwise accepted text files may still be read. Review audit output and do not place secrets in source files. |
| Path or symlink escape | Scanning and corpus extraction require paths rooted in the audited repository; symlinks that escape the root are rejected. | Platform-specific filesystem behavior and races remain possible. Report containment failures privately. |
| Malicious ignore rules | Ravel applies bounded, repository-local ignore matching and preserves explicit safety exclusions. | Ignore rules are not a security policy by themselves; built-in secret exclusions remain the primary boundary. |
| Parser denial of service | Configurable file and total-input limits are enforced (the default per-file limit is 1 MiB). Tree-sitter parsing is time-bounded and stopped partial parses are not reported as complete facts. | Inputs within the configured limits can still consume CPU and memory. Use conservative limits for untrusted corpora. |
| Crafted PDFs or office documents | Extraction is explicit, restricted to audited paths, and invokes only allowlisted local tools without a shell. Output is written under the corpus directory. | Vulnerabilities in `pdftotext`, `mutool`, or `pandoc` are outside Ravel's process boundary. Keep local extractors patched and use sandboxing for hostile documents. |
| Command injection through extractors | Ravel resolves known executables and passes arguments directly; it does not construct a shell command from corpus content. | An attacker who controls the executable search path may substitute a malicious binary. Run Ravel in a trusted developer environment. |
| Malicious graph fragments | Fragment ingestion validates schema, endpoints, provenance, confidence, evidence, and declared source paths before merging. | Semantically false but structurally valid agent output can still poison conclusions. Review inferred facts and their evidence. |
| HTML or script injection in the dashboard | The dashboard is generated as a self-contained, read-only artifact and graph data is serialized rather than concatenated as executable markup. | Open generated dashboards only in a patched browser and report any executable-content injection. |
| Malformed MCP input | The MCP server is stdio-only, validates protocol messages and parameters, and exposes read-only graph operations. | The host process controls who can start the server and what graph directory it can access. |
| Release or update compromise | Update checks are explicit. Self-update validates release metadata and uses the release workflow's synchronized artifacts. | GitHub, release credentials, signing keys, and the distribution channel remain supply-chain dependencies. Verify release provenance for high-assurance use. |
| Data disclosure through an assistant | The Go CLI does not invoke assistants. The installed skill limits roles to audited source paths and requires consent before external corpus upload. | The selected assistant host and model provider have their own data-handling policies. Use a local model or avoid enrichment for sensitive repositories. |

### Security invariants

Ravel intends to preserve these properties:

- analysis never executes code found in the analyzed repository;
- normal local analysis does not make network requests;
- ignored secret-like files and escaping symlinks are not read;
- external document tools run only after an explicit extraction command;
- inferred relationships are never presented as deterministically extracted;
- malformed or unresolved evidence fails closed instead of becoming a guessed
  graph relationship; and
- security, race, and vulnerability checks in CI are blocking.

These are engineering claims, not a sandbox guarantee. Run Ravel with the
privileges of a normal developer account, not as root, and isolate it when
processing actively hostile input.

## Dependency and release security

CI runs Go tests with the race detector, `go vet`, `gosec`, `govulncheck`, and
pull-request dependency review. Security checks are not marked
`continue-on-error`. Maintainers should treat changes to `go.mod`, `go.sum`,
GitHub Actions, release scripts, installers, and self-update code as
security-sensitive.

Release maintainers should keep Go and document-extraction tools on supported,
patched versions and rotate or revoke release credentials after suspected
exposure.

## Out of scope

The following are normally not vulnerabilities in Ravel by themselves:

- incorrect conclusions produced only by an external model when Ravel preserves
  the `inferred` label and provenance;
- denial of service that requires intentionally raising documented local input
  limits to unsafe values;
- vulnerabilities in unsupported Ravel versions; and
- compromise of the local machine, assistant host, or tools found earlier on a
  user-controlled executable search path.

We still welcome private reports when an out-of-scope condition can be combined
with Ravel behavior to cross one of the trust boundaries above.
