<p align="center">
  <img src="assets/logo.png" alt="Ravel" width="520">
</p>

<p align="center">
  <a href="https://github.com/12vault/ravel/actions/workflows/go.yml"><img src="https://github.com/12vault/ravel/actions/workflows/go.yml/badge.svg?branch=main" alt="Go checks"></a>
  <a href="https://github.com/12vault/ravel/releases"><img src="https://img.shields.io/github/v/release/12vault/ravel?display_name=tag&sort=semver" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/12vault/ravel" alt="MIT License"></a>
</p>

<p align="center">
  <strong>Secure, local code intelligence in a single Go binary.</strong>
</p>

Ravel turns code and documents into a local knowledge graph. Developers and coding agents can find the right files, trace relationships, and understand a system without scanning the repository from scratch.

**What you get:**

- One prebuilt binary with no Python runtime.
- Fast, offline analysis for Go and a broad set of Tree-sitter grammars.
- Evidence labels that separate parsed facts from inferred relationships.
- Audit-first scanning that excludes secrets, dependencies, and ignored files.
- Compact graph context for coding agents through the CLI or MCP.
- Optional agent enrichment for architecture, domains, flows, documents, and learning tours.

> [!IMPORTANT]
> Ravel keeps evidence levels separate. Parser facts are `extracted`, name-based matches are `inferred`, and unsafe matches remain unresolved. Optional agent enrichment is validated and provenance-tagged; it is never presented as parser output.

## Why Ravel?

Coding agents are good at reading a file. The harder problem is knowing which file matters, what calls it, and how it connects to the rest of the repository.

Ravel creates that missing map:

- Find entry points and central packages.
- Trace calls and definitions between symbols.
- Search the graph without rescanning source files.
- Generate a suggested reading order.
- Give coding agents compact, local repository context.
- Audit what will be read before building anything.

### Ravel and Graphify

[Graphify](https://github.com/safishamsi/graphify) aims for a broad knowledge-graph product with rich visualization, clustering, exports, and many integrations. Ravel has a narrower systems goal: provide secure, fast, deterministic code intelligence as a small, embeddable Go binary.

| Capability | Ravel | Graphify |
| --- | :---: | :---: |
| Prebuilt native binary | ✅ | — |
| No Python runtime | ✅ | — |
| Small compiled dependency surface | ✅ | — |
| Offline code analysis | ✅ | ✅ |
| No LLM required for code graphs | ✅ | ✅ |
| Polyglot Tree-sitter extraction | ✅ | ✅ |
| Deep Go AST and type analysis | ✅ | — |
| More language-specific extraction passes | ◐ | ✅ |
| Pre-build file audit | ✅ | — |
| Built-in secret and key-material exclusions | ✅ | — |
| Extracted, inferred, and unresolved evidence labels | ✅ | ✅ |
| Embeddable Go packages | ✅ | — |
| Read-only MCP server | ✅ | ✅ |
| Self-contained HTML visualization | ✅ | ✅ |
| Automatic community clustering | ✅ | ✅ |
| Wiki and graph-database exports | — | ✅ |
| Optional semantic document enrichment | ✅ | ✅ |

**Legend:** ✅ built in · ◐ supported with less language-specific depth · — not a primary feature

Ravel does not try to win by having the longest feature list. It prioritizes a small attack and dependency surface, predictable local execution, explicit provenance, and release binaries that users can run without building the project or managing a language environment.

## Contents

- [Quick start](#quick-start)
- [Explore the graph](#explore-the-graph)
- [Cross-project graphs](#cross-project-graphs)
- [Pull-request impact](#pull-request-impact)
- [MCP server](#mcp-server)
- [Generated artifacts](#generated-artifacts)
- [Safety](#audit-first-safety)
- [Configuration](#configuration)
- [Agent workflow](#agent-workflow)
- [Integrations and hooks](#native-integrations-and-hooks)
- [Updating Ravel](#updating-ravel)
- [Capability layers](#capability-layers)
- [Benchmarks](#benchmarks)
- [Development](#development)
- [License](#license)

## Quick start

### Install a release binary

Install the latest checksum-verified release on macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/12vault/ravel/main/install.sh | sh
```

The installer writes to `~/.local/bin` by default. If that directory is not on `PATH`, it prints the exact command to enable it. Run that command before using `ravel`, then add it to your shell profile so future terminals can find the binary.

On Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/12vault/ravel/main/install.ps1 | iex
```

PowerShell prints the equivalent `PATH` command when `~/.local/bin` is not already available.

Release archives also contain a portable `ravel` binary that runs in place without installation.

### Build from source

Install Go 1.26.5 or newer, then run:

```sh
git clone https://github.com/12vault/ravel.git
cd ravel
go install ./cmd/ravel
```

`go install` writes to `GOBIN`, or to `$(go env GOPATH)/bin` when `GOBIN` is unset. Make sure that directory is on `PATH`.

To build a portable binary in the current directory instead:

```sh
go build -o ravel ./cmd/ravel
```

### Connect a coding assistant

Install the project-local Codex skill directly from a source checkout:

```sh
go run ./cmd/ravel install --project --platform codex
```

Register the bundled skill with your AI coding assistant:

```sh
# Codex user-wide install
ravel install --platform codex

# Or keep the skill and integration files inside the current project
ravel install --project --platform codex
```

That command installs the complete skill bundle: orchestration instructions, eight role prompts, references, and launchers. Marketplace packages also include native Ravel binaries for macOS, Linux, and Windows on amd64 and arm64. If `ravel` is not already on `PATH`, the skill uses the matching bundled binary in place, with no initial download or separate installation.

Project installs for supported assistants also add an owned query-first integration. When a graph exists and a non-trivial coding task has an unknown implementation location or change surface, the assistant is told to run `ravel context "<user task>"` before broad repository search, refine exact targets, run `ravel affected <target>` before changing shared code, and then verify the returned source and tests. Trivial known-file and strictly local edits skip the graph step.

Then invoke the skill in Codex:

```text
$ravel .
```

Assistants that use slash commands accept `/ravel .` instead. The installer also supports `claude`, `codebuddy`, `opencode`, `kilo`, `copilot`, `vscode`, `aider`, `openclaw`, `droid`, `trae`, `trae-cn`, `gemini`, `hermes`, `kimi`, `amp`, `agents`, `kiro`, `pi`, `cursor`, `devin`, and `antigravity` skill locations. Use `--project` for a repository-scoped installation.

### Build your first graph

Run these commands from the repository you want to analyze:

```sh
cd your-repository

# Confirm the installed version.
ravel version

# Preview which files Ravel will read.
ravel audit .

# Build the local graph in .reporavel/.
ravel build .

# Print the generated overview.
ravel report

# Generate a self-contained interactive graph.
ravel dashboard
```

## Explore the graph

### Search and retrieve context

Search for files, packages, types, functions, or methods:

```sh
ravel query "SessionManager"
```

Retrieve a connected, token-bounded explanation for a natural-language question:

```sh
ravel context "how does SessionManager create and persist a session?"
```

`ravel context` combines Unicode-aware BM25F/IDF ranking, multiple lexical seeds, and deterministic graph traversal. It defaults to bidirectional BFS at depth 2, suppresses expansion through non-seed super-hubs, and keeps every discovered node beside the edge that explains why it was included. The compact text payload is conservatively bounded to about 2,000 tokens by default.

Useful controls include:

```sh
ravel context --traversal dfs --direction out --max-depth 3 "authentication flow"
ravel context --relations calls,references --token-budget 1200 "who reaches CreateSession?"
ravel context --branch-fanout 32 "wide dispatcher dependencies"
ravel context --json "what imports the storage package?"
```

When no explicit `--relations` filter is supplied, Ravel can infer one from words such as “called,” “imports,” “inherits,” or “tested.” Use `--infer-relations=false` to traverse every available relationship. `--json` returns the same selected context in a machine-readable envelope; `estimatedTokens` measures the compact text payload, so JSON field-name and formatting overhead is intentionally not included.

Default compact output balances connected candidates and explanatory edges. The opt-in `--candidate-shortlist` profile instead spends most of the envelope on a ranked candidate list, then adds bounded explanatory edges; it is useful for broad retrieval comparisons where candidate recall matters more than edge evidence. JSON stats separate shortlist selection (`unselectedNodes`) from hard truncation (`truncated`, `omittedNodes`, and `truncatedReason`) and split the payload into header, candidate, and explanation tokens. Compact output reports every hard truncation cause. `token_budget` can be addressed by raising `--token-budget` or narrowing the output. `branch_limit` happens earlier during traversal: narrow relations/depth or raise `--branch-fanout`; more output tokens alone cannot recover a pruned branch. The default branch fanout of `0` chooses a safe automatic cap from the node, seed, and depth limits.

### Trace relationships

Explain a file or symbol and show its immediate relationships:

```sh
ravel explain "internal/auth/session.go"
```

Find a path between two graph nodes:

```sh
ravel path "main" "CreateSession"
```

`path` prefers a directed route. If only graph connectivity exists, the result is labeled `undirected_fallback`, and every hop reports whether it was followed forward or in reverse together with the original edge orientation. Duplicate exact target names for `affected`, `explain`, or `path` are ambiguity errors that list candidate node IDs instead of choosing silently.

Find incoming callers, references, implementers, importers, and other dependents of one target:

```sh
ravel affected "CreateSession"
```

`affected` defaults to reverse dependency relationships, excluding generic containment noise; explicitly requested `affects` or `flows_to` edges follow their forward causal orientation. A file target bootstraps its direct definitions. A package, module, or directory target bootstraps directly contained files and their direct definitions, with at most 20 total origins. It intentionally does not recurse through an entire repository or nested directory tree; use changed-file inputs or `ravel diff` for repository-wide impact. Use `--relations` to narrow traversal further; a target that cannot be resolved is reported as an error rather than guessed.

Add `--json` to `query`, `explain`, or `path` when another tool will consume the result.

Natural-language wording is accepted for compact lexical graph search:

```sh
ravel query "which parts handle authentication?"
```

Use `query` when a short ranked list is enough. Use `context` when the relationships around the matches are part of the answer.

### Run focused workflows

Focused graph workflows are first-class commands:

```sh
ravel tech
ravel understand
ravel learn
ravel docs
ravel pdf
ravel schema
ravel diff                 # impact from the last update
ravel diff internal/api.go # impact from explicit paths
```

In an agent session, these routes also activate the specialized semantic roles and merge their evidence-tagged fragments.

Native architecture reports also include deterministic import-cycle detection and representative local call flows. Polyglot symbol resolution checks the current file first, then directly imported files, then a unique repository-wide match. Ambiguous matches remain unresolved.

## Cross-project graphs

Merge existing Ravel graphs without rescanning their source repositories:

```sh
ravel merge --out .ravel-workspace api=../api/.reporavel web=../web/.reporavel
```

For graphs you query together often, register them once and use the global commands:

```sh
ravel global add api ../api/.reporavel
ravel global add web ../web/.reporavel
ravel global list
ravel global query "session authentication"
ravel global context "how does the web app reach the API session code?"
ravel global build --out .ravel-global
```

The registry defaults to `~/.ravel/registry.json`, is written with private permissions, and stores absolute local graph-directory paths. Use `--registry <path>` to choose another registry. Merged graph IDs and paths are project-namespaced, while the generated graph does not embed registry paths or source-root locations.

## Pull-request impact

Overlay open GitHub pull requests on the local graph:

```sh
ravel prs --repo owner/repository
ravel prs --conflicts
ravel prs 42
```

Live mode uses the authenticated `gh` CLI to load changed files, checks, and review state. Ravel maps each changed file to its graph community and reverse dependency impact, then flags PR pairs that touch the same file or community. Use `--json` for structured output or `--manifest <gh-json-file>` for an offline, reproducible analysis.

## MCP server

Expose the existing graph to MCP-capable coding agents without a network service or additional dependencies:

```sh
ravel mcp --out .reporavel
```

The stdio server provides five read-only tools: `query`, `context`, `explain`, `path`, and `affected`. `context` keeps Ravel's normal traversal, hub-suppression, confidence, evidence, and token-budget controls. `affected` uses reverse dependency traversal by default, excluding generic containment noise; explicitly requested causal edges follow their forward orientation.

Configure a client to launch the local process with `ravel` as the command and `mcp --out /absolute/path/to/.reporavel` as its arguments. The server supports standard newline-delimited MCP stdio and Content-Length framing, locks to the client's first framing mode, and writes protocol messages only to stdout. It keeps one immutable in-memory index and hot-reloads it after an atomic graph-state replacement, so `ravel update` results become available without restarting the client.

An optional Streamable HTTP transport is available for trusted local or service-to-service clients:

```sh
RAVEL_MCP_API_KEY='replace-me' ravel mcp \
  --transport http \
  --address 127.0.0.1:8080 \
  --path /mcp
```

HTTP defaults to loopback. A non-loopback bind is rejected unless the environment variable named by `--api-key-env` contains an API key (`RAVEL_MCP_API_KEY` by default). The server accepts bearer or `X-API-Key` authentication, rejects browser `Origin` requests, bounds message and session state, and uses `Mcp-Session-Id` for session lifecycle. It deliberately exposes no CORS or legacy SSE endpoint.

## Generated artifacts

`ravel build .` writes these files to `.reporavel/` by default:

| File | Purpose |
| --- | --- |
| `report.md` | Human-readable architecture summary, import cycles, call flows, and reading order |
| `graph.json` | Complete node, edge, metric, and diagnostic graph |
| `files.json` | Scanned files, hashes, sizes, languages, and ignored paths |
| `symbols.json` | Extracted functions, methods, types, variables, and related symbols |

Ravel also keeps canonical update state under `.reporavel/.state/`. Its private `cache/analysis-v1/` entries reuse analysis for unchanged content: Markdown is cached per file, while Go, SQL, and each Tree-sitter language use cross-file-safe analyzer batches. Cache keys include source hashes, analyzer settings, the Ravel version, and the cache schema. These entries only accelerate builds and updates; queries continue to use the single canonical graph. Disabling JSON output removes the public JSON exports but preserves this internal state so updates and queries continue to work.

`ravel dashboard` additionally creates `graph.html`, a dependency-free local dashboard with search, kind and community filters, a community legend, node details, and relationship navigation. Ravel assigns stable community IDs, names, and sizes from graph structure, stores them in node metadata, and uses them to group and color related nodes. Clustering is deterministic and runs locally without an LLM. High-degree utility hubs are automatically down-weighted at the graph's p99 degree threshold (with a floor of 50) so they do not pull unrelated subsystems together.

Community clustering is enabled by default. Choose `coarse`, `balanced`, or `fine` with `output.communityGranularity` or `ravel community --granularity`; use `output.communityHubDegreeThreshold: -1` only when you explicitly want to disable hub handling. Set `output.communityClustering: false` to omit generated community metadata and report sections, or run `ravel dashboard --communities=false` for an unclustered dashboard. Normal retrieval remains unchanged. `retrieval.communityBoost: true` or `ravel context --community-boost` enables a conservative same-community tie-breaker; keep it opt-in and compare it against the repository benchmark before adopting it as a default.

Deterministic names remain the community identity. Optional AI text is description-only and carries inferred provenance:

```sh
# Export a JSON template containing the current stable community IDs.
ravel community --template > community-descriptions.json

# Have your chosen assistant fill only description and rationale fields, then import it.
ravel community describe community-descriptions.json
```

The import rejects unknown or duplicate community IDs and never lets AI output change membership, stable IDs, or deterministic names. Descriptions are automatically discarded if later clustering changes the community ID.

Across updates, membership-hashed IDs remain authoritative while display labels are remapped by Jaccard overlap. Exact membership keeps both label and description. At least `0.70` overlap keeps the previous label; `0.40–0.70` keeps it provisionally and marks it for review; weaker matches receive the new deterministic label. On a split, only the strongest child inherits. A merge with multiple substantial parents receives a new label. Descriptions never transfer across changed membership.

Create a reviewable team bundle that omits raw source and private scanner state:

```sh
ravel share --out ravel-graph
git add ravel-graph
```

The bundle contains `graph.json`, `report.md`, `graph.html`, and a safety manifest. Review inferred content before committing it.

The graph models repository containment, code symbols, documents, schema entities, technical architecture, and business domains. The Go parser, pure-Go Tree-sitter polyglot parser, Markdown parser, and SQL parser add deterministic facts; SQL facts include tables, views, columns, indexes, declared foreign keys, and conservative `FROM`/`JOIN` references. Tree-sitter parses are bounded to two seconds per file and recoverable syntax errors are reported as diagnostics. When a parse stops, Ravel keeps only declaration nodes already present in the partial syntax tree, marks them with `partial: true` and `parse_complete: false`, and discards partial calls, imports, and heritage; stopped parses are never presented as complete. Agent-produced facts for any language or corpus enter through validated, provenance-tagged graph fragments:

```sh
ravel ingest fragment.json
```

## Audit-first safety

Ravel is local-first and audit-first.

See [`SECURITY.md`](SECURITY.md) for supported versions, private vulnerability
reporting, the threat model, and CI security controls.

- `ravel audit .` lists what will be analyzed and ignored.
- Analysis, graph, query, dashboard, and corpus commands make no outgoing network requests and never call an LLM. The explicit `ravel update-check` and `ravel self-update` commands access the release server; live `ravel prs` delegates GitHub access to the authenticated `gh` CLI. The optional MCP HTTP transport listens only when explicitly started.
- `ravel extract` may execute a discovered, allowlisted local extractor (`pdftotext`, `mutool`, or `pandoc`) only when the user invokes that command.
- Agent roles run only through the installed skill and the host assistant's normal permission model.
- Nested `.gitignore` and `.ravelignore` rules, symlinks, `.env` files, private-key formats, credential directories, databases, archives, binary media, dependency folders, and common build output are rejected before file content is read. Ravel-specific rules are loaded after Git rules in each directory, so they can add exclusions or negate a file-level Git pattern.
- Source discovery admits recognized code/document extensions and extensionless files with supported code shebangs; arbitrary text and binary formats are reported as unsupported without hashing their full contents.
- Default limits are 1 MiB per file and 100 MiB total input.
- Output goes to `.reporavel/` unless another directory is explicitly selected.
- Unresolved calls stay unresolved instead of being presented as certain matches.

Check the active defaults at any time:

```sh
ravel doctor
```

These defaults reduce accidental exposure; they are not a substitute for reviewing what exists in a repository before processing or sharing generated artifacts.

## Configuration

Create `.reporavel.yaml` with documented defaults:

```sh
ravel init
```

Useful command-line overrides include:

```sh
ravel audit --max-file-size 2097152 .
ravel build --out /tmp/ravel-output .
ravel build --no-call-graph .
ravel update .
```

Configuration is strict: unknown settings, invalid values, and options that are not implemented yet return an error. Set `analysis.go` to `false` to disable Go semantics and `analysis.polyglot` to `false` to disable Tree-sitter semantics. Disable both, plus `analysis.documents` and `analysis.schemas`, for topology-only output. The `output.json`, `output.markdownReport`, and `output.communityClustering` switches control generated artifacts and metadata. Community controls default to `output.communityGranularity: balanced` and `output.communityHubDegreeThreshold: 0` (automatic).

### Supported languages

Packaged binaries embed a curated, self-contained grammar set for JavaScript/TypeScript/TSX, Swift, Python, Java, Kotlin, Scala, Rust, Ruby, PHP, C/C++, C#, F#, Dart, Elixir, Erlang, Clojure, Lua, R, Objective-C, Perl, Groovy, Solidity, shell, PowerShell, HCL, Protocol Buffers, and GraphQL. Source builds without release tags embed gotreesitter's complete registry. Grammar loading is lazy in both cases. Named declarations use structural tags plus grammar-specific syntax-node shapes, so every advertised language produces symbol nodes even when its identifier or declaration node names differ from the common conventions.

### Retrieval defaults

The connected retriever can also be configured once for agents and benchmarks:

```yaml
retrieval:
  traversal: bfs
  direction: both
  inferRelations: true
  relations: all
  seedLimit: 3
  maxDepth: 2
  maxNodes: 100
  branchFanout: 0 # automatic; positive values override neighbors expanded per node
  hubDegreeThreshold: 0 # automatic p99 with a floor of 50; -1 disables
  tokenBudget: 2000
  communityBoost: false # opt-in same-community tie-breaker
```

Compare community-aware retrieval with the unchanged baseline using the same graph and dataset:

```sh
ravel benchmark --graph .reporavel --dataset benchmarks/ravel-retrieval.jsonl --out /tmp/baseline.json
ravel benchmark --graph .reporavel --dataset benchmarks/ravel-retrieval.jsonl --community-boost --out /tmp/community.json
```

## Agent workflow

The repository includes [`skills/ravel/skill.md`](skills/ravel/skill.md), a progressive agent workflow for coding-context discovery, technical maps, architecture understanding, business domains, change impact, documents, PDFs, schemas, articles, and dependency-ordered learning tours. Installers and marketplace packages publish it as the required uppercase `SKILL.md`.

For everyday coding work, the query-first loop is: run `context` with the user's task, refine with `query`, `explain`, or `path`, check the selected target with `affected`, then read the returned implementation and tests before editing. The graph narrows exploration; it does not replace source inspection or verification.

The intended loop is:

1. Audit the repository or corpus.
2. Build deterministic graph facts with user consent.
3. Run `ravel plan <route> --json` to create bounded, dependency-aware agent tasks.
4. Dispatch the eight packaged agents in ready waves and validate every returned fragment with `ravel ingest`.
5. Use `context` for bounded relationship questions, `query` for exact lookups, and `explain`, `path`, and `dashboard` for focused exploration.

Use `ravel tools` before document, PDF, or schema work. It discovers local extractors and database clients without executing them. `ravel extract <audited-file>` then processes PDF, DOCX, ODT, RTF, Markdown, or text locally into `.reporavel/corpus/`; it refuses unaudited paths. PDF content stays local unless the user separately authorizes an external service.

## Native integrations and hooks

Project installs place the portable skill in the platform's native directory. For Codex, Claude Code, Cursor, VS Code/Copilot, Gemini CLI, and OpenCode, Ravel also installs owned project instructions, rules, or hooks that apply the same query-first coding workflow. Existing configuration is preserved, repeated installs are idempotent, and uninstall removes only Ravel-owned content.

Manage a native integration directly:

```sh
ravel codex install
ravel codex uninstall
ravel claude install
ravel cursor install
ravel vscode install
ravel gemini install
ravel opencode install
```

The Claude marketplace package lives in [`.claude-plugin/marketplace.json`](.claude-plugin/marketplace.json), and the Codex marketplace package lives in [`.agents/plugins/marketplace.json`](.agents/plugins/marketplace.json). Both packages are validator-clean. The direct CLI installer remains available for every supported assistant destination.

Automatic graph refresh is opt-in. Install Git `post-commit` and `post-checkout` hooks from the repository:

```sh
ravel hook install
ravel hook status
ravel hook uninstall
```

The Git hooks launch `ravel update .` in the background and write failures to the temporary `ravel-hook.log` file. Existing hook content is preserved.

For live work without Git events, use polling watch mode:

```sh
ravel watch --interval 2s .
```

Only changed hashes trigger an update. The update invalidates stale agent enrichment and records changed paths for `ravel diff`.

The installed Ravel skill runs one local, hash-aware `ravel update .` at the beginning of a task whenever an existing `.reporavel/graph.json` is present. Missing graphs still require an audited, consented first build. The skill never starts watch mode, installs Git hooks, or leaves a background process running without explicit consent.

## Updating Ravel

Check for a newer release without downloading or changing anything:

```sh
ravel update-check
ravel update-check --json
```

`update-check` makes one explicit release-metadata request. Ravel does not check for updates in the background and never updates itself during a skill tool call.

Binary and manual skill installations can then update together:

```sh
ravel self-update --platforms codex,claude
ravel self-update --platforms codex,claude --project
```

The command downloads the selected release archive and checksum, verifies it, atomically replaces the binary, then refreshes only the explicitly listed skill destinations. Marketplace-managed skills update through their marketplace client.

Installed Ravel skills perform a zero-network local version handshake on their first invocation. Their launcher compares the global CLI with the skill's `VERSION`; when the global CLI is older, it uses the bundled binary and prints a non-blocking update hint. It does not run `update-check` or `self-update` automatically.

After CLI changes, run `python3 scripts/sync-packages.py` to rebuild all six native binaries and copy the refreshed skill bundle into both marketplace packages. Validate the result with `python3 scripts/test_release.py`.

macOS release jobs use `MACOS_CERTIFICATE_P12`, `MACOS_CERTIFICATE_PASSWORD`, `MACOS_SIGNING_IDENTITY`, `APPLE_ID`, `APPLE_APP_PASSWORD`, and `APPLE_TEAM_ID` repository secrets when available. Until those are configured, releases use ad-hoc signatures for integrity but are not notarized by Apple.

Maintainers prepare a synchronized release with `scripts/release.sh <version>`. It updates CLI, Claude, and Codex versions, rebuilds the bundled binaries, synchronizes every packaged skill resource, runs tests and validators, and verifies that no package drift remains. Committing and pushing the matching `v<version>` tag triggers the binary release workflow.

## Roadmap

- Configure Apple Developer ID signing and notarization secrets for Gatekeeper-approved macOS marketplace and release binaries.
- Replace ad-hoc macOS release signatures with mandatory Developer ID signatures after the first public MVP releases.

## Capability layers

| Capability | Offline Go binary | Agent skill |
| --- | --- | --- |
| Popular languages | Safe file topology plus pure-Go Tree-sitter semantics for the packaged grammar set | Architecture, intent, and explanations across languages |
| Code structure | Go AST plus extracted Tree-sitter definitions/call sites; inferred target matching is labeled | Language-independent specialized code analyzer |
| Docs and schemas | Markdown headings/links; SQL tables, views, columns, indexes, foreign keys, and `FROM`/`JOIN` references | Rich document, PDF, article, and schema semantics |
| Domains and flows | Validated graph model and focused views | Domain, flow, process-step, and actor inference |
| Learning | Centrality and tour graph views | Generated onboarding guides and dependency-ordered tours |
| Queries | IDF/BM25F search, bounded multi-seed BFS/DFS context, explain, shortest path, traversal, impact | Natural-language synthesis grounded in graph evidence |
| Updates | Hash-aware update, Git hooks, and watch mode | Stale enrichment invalidation and targeted re-analysis |
| Dashboard | Self-contained read-only HTML | Optional generated explanations and tours |

The distinction is intentional: a skill does not need a language allowlist, but users still need to know which facts are reproducibly parsed and which are inferred by an agent.

## Benchmarks

Run the local build/query performance suite with `./benchmarks/run.sh`. The checked-in self-repository relationship suite uses `--gate benchmarks/self-quality-gate.json` to reject stale evidence IDs and fail on metric regressions. A second CI suite runs 54 evidence-tagged questions across pinned chi, Express, and Click revisions; validate it offline with `python3 benchmarks/run_external_quality.py --check`. [`benchmarks/datasets.json`](benchmarks/datasets.json) defines the repository-question contract. Version 3 reports node recall/precision, evidence recall/precision, reciprocal rank, p50/p95 latency, compact context tokens, node and evidence recall per 1,000 tokens, truncation rate, index-build time, logical graph and dataset hashes/revisions, adapter version, Ravel version, Go version, platform, and optional quality-gate results. An optional strict `--answers` ledger adds externally adjudicated accuracy, rubric key-fact coverage, total agent tokens, total spend, and provenance without retaining raw answers; see the [benchmark guide](benchmarks/README.md). Ravel does not claim native LOCOMO/LongMemEval corpus adapters and never invokes a model or judge. Every published score must retain the raw result file and configuration.

The language-comparison suite uses pinned upstream repositories—T3 Code (TypeScript), Ghostty (Swift), ripgrep (Rust), libgit2 (C), and nlohmann/json (C++)—plus published CrossCodeEval, ContextBench, CodeSearchNet, Real-FIM, and RepoBench adapters. Source filters, revisions, manifests, and full results are documented in the [benchmark guide](benchmarks/README.md). These are retrieval-compatibility measurements, not upstream project metrics or final-answer scores.

### Latest complete Ravel vs Graphify sweep

The 2026-07-17 current-local sweep compared Ravel commit `eec9c3c` with [Graphify 0.9.17](https://github.com/Graphify-Labs/graphify/releases/tag/v0.9.17) on 23,525 primary paired cases. All cases completed with zero execution failures at a 2,000-token budget. Ravel had higher recall on eight of nine suites with retrieval gold and higher MRR on all nine; Graphify won RepoBench recall, 90.86% to 89.12%. On the separate Real-FIM scale/parser proxy, Graphify returned the target file in 99.01% of cases versus Ravel's 94.09%, driven by incomplete Go files.

The same-file promotion raised T3 Code exact declaration retrieval from 183/487 to 204/487 without increasing the token budget; Graphify retrieved 20/487. Mean Ravel payload increased from 1,855 to 1,923 estimated tokens. Scores from different adapters are not averaged because their gold definitions differ, and Real-FIM target-file rate is not retrieval correctness. See the [full interpretation](benchmarks/RESULTS.md#2026-07-17-full-latest-local-ravel-vs-graphify-0917-sweep) and [machine-readable artifact](benchmarks/results/ravel-eec9c3c-vs-graphify-0.9.17-2026-07-17.json).

### Historical ten-question T3 Code payload snapshot

Measured on 2026-07-12 on an Apple M1 Pro (`darwin/arm64`) with Ravel v0.2.5 and Graphify 0.9.12 against [`t3tools/t3code`](https://github.com/t3tools/t3code) commit `c1ec1915`:

**Bottom line:** there is no overall winner in this snapshot. Graphify built its graph faster. Ravel reclustered faster and returned a smaller context payload. Graph size is not a quality score, and this run did not test answer correctness.

| T3 Code measurement | Ravel | Graphify | Winner | Why |
| --- | ---: | ---: | --- | --- |
| Cold graph build | 166.54 s | 90.52 s | **Graphify** | 76.02 s faster (45.6% less time), but the tools scanned different input scopes |
| Resulting graph | 296,334 nodes / 366,815 edges | 49,517 nodes / 104,160 clustered edges | **No winner** | Tool-native schemas and extractors produced non-equivalent coverage; more nodes or edges does not prove better retrieval |
| Tool-native reclustering | 13.23 s | 19.71 s | **Ravel** | 6.48 s faster (32.9% less time) on each tool's own graph |
| Compact context, 10 questions | 6,821 estimated tokens | 9,009 estimated tokens | **Ravel** | 2,188 fewer estimated tokens (24.3% smaller) under the same questions and 800-token setting |

The payload result means Ravel returned less text for these ten questions. It does not show whether that text produced better answers. It also does not measure model input billing, answer quality, or factual accuracy. Ravel scanned 5,630 accepted code/document files, graphified 5,065, and skipped 565 that produced no graph content. Graphify scanned 5,448 code files with `--code-only`; it skipped non-code inputs, 805 source files that produced no nodes, and 11 SQL files because its optional SQL parser was not installed. The build and tool-native clustering rows therefore describe observed end-to-end behavior, not equal-graph algorithm microbenchmarks. The [question set](benchmarks/t3code-context-questions.json), [comparison script](benchmarks/compare_context_payloads.py), and [raw results](benchmarks/results/t3code-ravel-v0.2.5-vs-graphify-0.9.12-2026-07-12.json) are checked in for reproduction.

## Development

Run the checks:

```sh
go test ./...
go vet ./...
```

The test fixture under `testdata/simple-go-service/` covers repository topology and Go call extraction.

## License

Ravel is available under the [MIT License](LICENSE).
