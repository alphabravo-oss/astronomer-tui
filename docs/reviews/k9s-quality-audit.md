# Astronomer TUI: daily-driver quality audit and implementation plan

> This is the original audit and first implementation record. P1–P13 were subsequently implemented and verified as described in [the follow-through report](implementation-follow-through.md). The baseline findings and first-pass limitations below are historical, not the current feature list.

Date: 2026-10-12. Baseline: the repository working tree at the start of this review.

## Decision and standard of proof

Astrotui has useful foundations, but the existing “full k9s parity” and completion language are not supported by the interactive implementation. Passing helper tests does not establish operational parity. The immediate goal is a trustworthy, fast resource browser with working search, safe actions, clear state, and bounded background work. Broader k9s parity is a separate release gate, not a claim this audit can award by inspection.

This document records source inspection, external reference research, reproducible defects, implementation tasks, and validation results. Checkboxes indicate implemented and checked behavior, not merely code that exists. External references were consulted on the date above; k9s remains a moving target.

## Research references and implications

1. [k9s commands](https://k9scli.io/topics/commands/): resource/context/namespace navigation, regex/inverse/label/fuzzy filters, escape behavior, and destructive shortcuts. Astrotui should accommodate established keyboard habits, particularly avoiding deletion on the describe key.
2. [k9s configuration](https://k9scli.io/topics/config/): configurable API timeouts, read-only operation, per-context preferences, log tail/buffer limits, and terminal presentation. Settings need real runtime consumers.
3. [k9s custom views](https://k9scli.io/topics/columns/): configurable column ordering, types, and wide visibility. Astrotui's many placeholder columns are not equivalent to working metrics or readable default tables.
4. [k9s plugins](https://k9scli.io/topics/plugins/): shortcuts, resource scopes, execution mode, variables, and multiple configuration locations. Parsing a subset of YAML is partial compatibility.
5. [Kubernetes API concepts](https://kubernetes.io/docs/reference/using-api/api-concepts/): watch recovery, resource versions, conditional mutations, and patch semantics. Retain client-go informers; establish explicit ownership and reject stale UI work. Edits must detect concurrent changes and express removed fields.
6. [Kubernetes CRDs](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/): discovery, scope, versions, and printer columns are distinct concerns. Generic GVR access alone is not complete CRD support.
7. [kubectl logs](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_logs/): tail bounds, previous containers, source prefixes, and follow behavior. Stream rendering must not block ingestion or discard final partial lines.
8. [tview source and documentation](https://github.com/rivo/tview), plus the pinned v0.42.0 module locally: widget changes belong on the event loop; QueueUpdateDraw blocks the producer until consumed. Selection and backing object mappings must change together.

## Architecture and execution paths

- `cmd/astrotui`: CLI, config/session restoration, kubeconfig/client creation, interactive startup, headless and synthetic performance paths.
- `internal/cluster`: typed/dynamic/discovery clients and informer stores. Events feed resource refreshes; the cache currently spans all namespaces.
- `internal/ui`: tview event handlers, tables, palette, logs, shell/editor/port-forward workflows, AI, recovery, themes.
- `internal/ui/views`: built-in column definitions, generic renderers, YAML details, CRD printer-column helper.
- `internal/actions` and `internal/safety`: mutation execution and context policy. UI entrypoints must use these consistently.
- `internal/logs`: parsing, filtering, bounded retention, stream transport, export.
- `internal/palette`, `plugins`, `config`, `session`: discovery/search and local customization/persistence.
- Tests: helpers/fakes, tcell simulation, optional envtest, build-tagged kind integration, synthetic performance and distribution contract checks.

## Findings by aspect

| Aspect | Baseline evidence / observed gap | Required outcome |
|---|---|---|
| Startup | Mandatory 1.8-second splash consumes the first key; incompatible with the stated 300 ms goal | Show useful main view immediately; retain branding without blocking work |
| Information hierarchy | A single bordered table; no persistent context/policy/status/shortcut bar | Context and namespace always visible; row count, loading, errors, filter, and available actions discoverable |
| Table density | Pod table has 26 columns including unpopulated metrics; names can disappear horizontally | Compact useful defaults, optional wide view, fixed header and identity columns |
| Selection correctness | `refresh` replaces `visibleObjects` before queuing table drawing; row index retained when sorting changes | Atomically update data and widgets on event loop; retain selected UID through insertion/reordering |
| Namespace switching | `switchNamespace` re-renders old namespace's `objects` | Reload scoped rows from cache immediately; support all namespaces |
| Context switching | Old cache uses long-lived parent context; only resource subscriber is cancelled | Explicit cache lifetime; cancel old cache; prevent old callbacks overwriting new context |
| Watch efficiency | 256-event subscriptions accumulate without unsubscribe; every event rebuilds the table | Release subscriptions and coalesce invalidations; no redraw storm on bulk updates |
| Watch feedback | WaitForSync result ignored; list/watch failures not represented in the interface | Explicit loading and watch-error status, preserving retry behavior |
| Palette | Parent intercepts `q`; arrows never move list while input focused; resource/ns choices can leave palette visible | Full text input; navigable results; skip group headers; close correctly on navigation |
| Commands | `:ns` alone rejected, `:ctx` absent; extra arguments silently ignored | Strict parsing; namespace/context pickers and direct switch; quit command |
| Filtering | Only case-insensitive substring search, commit-only entry | Live validated regex, inverse, label, fuzzy and literal modes; Esc restores previous filter |
| Sorting | Lexical numbers/ages; equal keys inherit map iteration order | Numeric/time-aware order and deterministic identity tie-breaks |
| Destructive actions | `d` immediately deletes in non-prod contexts | `d` opens details; Ctrl+D opens cancel-first confirmation; typed prod confirmation stays |
| YAML edit correctness | Map diff ignores deleted keys and compares with fmt.Sprint; patch has no original version precondition | JSON merge patch with deletions/types preserved, original resourceVersion, unchanged identity |
| Scale input | Negative values parsed and passed to API | Reject invalid/negative counts before any request |
| Logs | Per-line redraw blocks producers; O(n) retention shifts; no bounded initial tail; long/unterminated lines mishandled | Ring retention, batched paints, bounded tail/line size, flush partial line, explicit follow/pause |
| Log lifecycle | Leaving via owner/events can keep streams alive; background context outlives application | Tie to application/log view lifetime and cancel on navigation |
| Display correctness | Resource/log/YAML strings interpreted as tview color markup | Escape external content at rendering boundaries |
| AI | Opt-in is good; sends raw selected data; streams can block on channel sends; Ollama drops Request.Context | Cancellation-aware streams and complete request context; document data handling limits |
| Plugins | Shortcuts/scopes not enforced; subprocess stdio not connected, background children not waited | Wire terminal stdio/reap children; restrict plugins in read-only policy; clarify compatibility limits |
| Session | Non-atomic write; only resource plural saved, losing custom GVR; all-ns ambiguous | Atomic private persistence and explicit all-ns/custom-GVR round trip |
| Configuration | Imported hotkeys/aliases are copied but not consumed; XDG overrides unsupported | Correct claims now; complete conversion/runtime support before claiming import parity |
| CRDs | RendererForCRD helper is not used by app; discovery helper not used by palette; hardcoded scope | Keep explicit GVR supported; track discovery/scope/printer integration as release work |
| Metrics | CPU/MEM columns fall back to missing fields; no metrics-server client | Do not imply live metrics from empty columns; explicit future metrics pipeline with unavailable states |
| Auth/RBAC | Exec auth exists; all-namespace informer can fail for namespace-only principals | Test real exec credential flows and implement scoped informers before least-privilege parity claim |
| Port forwards | Manager exists; readiness/state/cleanup need live verification | Validate local-only bind, readiness, stop, context-switch policy, and disconnect recovery |
| Recovery | Handler panic reports exist; not every callback/background task is protected | Preserve reports; exercise lifecycle tests; do not claim process-wide recovery |
| Packaging | Signed checksums published, installer does not verify downloads | Verify archive SHA-256 before extraction; reject unsupported platforms; document signature verification separately |
| Container | Distroless lacks editor, shell, and cloud exec-auth binaries | Document native-binary recommendation and container capability limits |
| Tests | Baseline `go test ./...` passes; some integration tests skip, and perf paths bypass actual TUI | Add behavioral simulation regression tests and record exact gates/limitations |
| Accessibility | Color is primary signal, long unbounded modal/help content, no narrow-screen checks | Text status plus color, scrollable help, simulated 80x24 and 120x36 coverage |

## Implementation contract

All tasks below are the committed implementation for this change. They are deliberately concrete enough to test. They improve multiple daily-driver workflows; they do not redefine unimplemented k9s features as parity.

### A. Data integrity and lifecycle

- [x] A1. Commit table rows and backing objects together on the UI loop; preserve selection by UID (namespace/name fallback for fixtures); deterministic fallback when selected object disappears. Test insertion, sorting, removal, and stale callbacks.
- [x] A2. Reload on namespace change and support `all`/`*`; own cache cancellation on context switch and shutdown. Tag resource callbacks with a generation, not only GVR.
- [x] A3. Unsubscribe abandoned resource views; coalesce invalidations; surface informer failures and initial loading state. Test cancellation and watch errors.
- [x] A4. Correct edit patches for deletion, nested values, and type changes; enforce original version and immutable identity. Reject negative scaling. Test patch application and API preconditions.

### B. Interactive quality

- [x] B1. Main layout with persistent context/namespace/policy, status/count/filter, contextual shortcuts; immediate startup; compact/wide tables, fixed header, readable state coloring, escaped content.
- [x] B2. Palette keyboard navigation works with input focus; `q` is searchable; category headers cannot execute; successful navigation closes palette. Add keyboard simulation coverage.
- [x] B3. Commands support `:ns`, `:ns NAME`, `:ctx`, `:ctx NAME`, `:q`; reject stray arguments. Escape clears resource filter; namespace Enter switches; `0` shows all namespaces.
- [x] B4. Live resource filters with regex, `!` inverse, `-l` label selectors, `-f` fuzzy, and `-t` literal text. Invalid input is visible and never replaces last valid filter; Escape restores original.
- [x] B5. Numeric and age sorting plus stable identity tie-breakers. Selection survives sort toggles.
- [x] B6. `d`/Enter/y open details; Ctrl+D deletes only through confirmation; Cancel default; read-only rejected before prompting or executing. Help matches actual shortcuts.

### C. Sustained operation

- [x] C1. Ring-buffer retention, log repaint batching, bounded initial tail, line-size bound and final-line flush; follow/pause with visible state; logs stop on exit/navigation/application shutdown.
- [x] C2. Preserve literal log/YAML content, including bracket sequences; keep AI/chat text input usable; cancellation-aware AI provider sends and include context for Ollama.
- [x] C3. Plugin stdio/child cleanup and read-only gating; full resource/context variables; describe unsupported compatibility features rather than claiming drop-in equivalence.
- [x] C4. Atomic session save; custom resource GVR and all-namespace restore without breaking old sessions.
- [x] C5. Installer checks archive SHA-256 before extraction; test success/tampering with local mocked downloads. No release publishing is part of this task.

### D. Evidence and documentation

- [x] D1. Run unit/race/vet, targeted simulation regressions, build, performance, and available API integration tests. Record results and skipped/unavailable gates.
- [x] D2. Update README and operator documentation to reflect implemented behavior, keyboard changes, compatibility limitations, and this audit. Supersede prior completion/parity assertions with evidence.

## Original parity backlog and release gates (continued in the follow-through report)

These are product-sized follow-on requirements, explicitly separate from the corrective implementation above. None may be represented as complete merely because a helper or table column exists.

| ID | Work / acceptance criteria | Dependencies / verification |
|---|---|---|
| P1 | Discovery catalog with preferred versions, kind/singular/short-name aliases, supported list/watch verbs, refresh/invalidation, and ambiguity errors | Fake discovery + envtest CRD install/remove; generic resource command resolution |
| P2 | Namespace-scoped informers for restricted RBAC; cluster-scoped CRDs resolved from discovery; cache sharing/eviction policy | P1; role limited to one namespace, no cluster-list privilege; switch loop shows no leaked watches |
| P3 | CRD printer columns including priority/type/JSONPath, server Table fallback, persisted custom view | P1; compare kubectl output for namespaced and cluster-scoped CRDs |
| P4 | Metrics-server integration for pods/nodes; quantities, requests/limits/percentages; explicit unavailable and stale values | Fake metrics + live metrics-server; missing RBAC/extension must not block resource browsing |
| P5 | Resource relationship navigation: workload→pods→containers, service→endpoints, owner chain, node→pods, events by UID | P1/P2; same names in multiple namespaces and replacement UIDs |
| P6 | Full log investigation: previous-container toggle, all containers, namespace/container source IDs, server timestamps, stable filtered regex positions and wrapped-line/source mapping | Live crash/restart fixture, multi-source order, disconnected stream/retry tests |
| P7 | Fully asynchronous discovery, action, AI-preparation and context-switch tasks with progress/cancellation and target snapshots | Slow API simulations; every input still responds under 5-second request latency |
| P8 | Complete customization import: translate k9s schema, aliases/hotkeys/views consumers, plugin scopes/shortcuts/directories, config hot reload | Real fixture corpus; unsupported entries reported, never silently called imported functionality |
| P9 | AI data preview/redaction, Secret denial, request-size bounds, provider error events, no automatic execution of model-proposed destructive actions | Provider mocks; explicit opt-in for transmission; never claim default privacy beyond AI disabled |
| P10 | Release trust: signature verification with pinned workflow identity/issuer, version and action-commit pinning, release test gating, SBOM/provenance, public registry pull checks | Release candidate in CI; installer checksums alone do not authenticate a compromised release |
| P11 | Genuine whole-process benchmarks: actual first useful terminal frame, event-to-visible-row p95, busy log ingestion, repeated switching, 5-minute RSS/CPU | Controlled 1k/5k/10k cluster fixtures, terminal size recorded; compare same hardware/k9s versions |
| P12 | Cross-platform terminal matrix, exec credential plugin fixtures, resize/Unicode/no-color/light theme/SSH/tmux verification | Linux/macOS amd64/arm64; shell/editor/container limitations documented |
| P13 | Remaining resource operations: DaemonSet restart, ReplicaSet scale, job trigger/suspend, node cordon/drain, RBAC review, storage diagnostics | Per-operation policy, preconditions and real API tests; no raw shell shortcuts for mutations |

A “better than k9s” release requires a fixed-version comparison with task completion time, error rate, responsiveness, resource consumption, keyboard coverage, and operator feedback. Proposed scenarios: diagnose CrashLoopBackOff; find pods by labels across namespaces; inspect a cluster-scoped CRD; safely edit under a concurrent rollout; recover from RBAC denial/API loss; inspect previous multi-container logs; switch 100 times between contexts; start/stop a forward; operate with read-only credentials. Report failures as well as averages.

## Validation record

Baseline: `go test ./...` passed on this checkout. No Kubernetes current context was configured. Docker is available, but existing containers belong to other work and will not be used as test targets. API integration will use isolated test assets where available.

Implemented A1–D2. The separate P1–P13 parity backlog remains open. Additional fixes made during verification: control-key/rune collisions, pod failure status overriding a misleading Running phase, UID preconditions on selected-object deletion, port bounds, session-write error reporting, and debounced theme reload with empty-file rejection. The latter was discovered by the final combined test run and verified with 20 race-enabled theme test repetitions.

| Gate | Result / scope |
|---|---|
| `go test -race ./...` | Passed, including new terminal simulation, filter, selection, patch, stream cancellation, and installer integrity regressions. Optional API tests require the separate gates below. |
| `make lint` | Passed using the repository's `go vet ./...` fallback; golangci-lint is not installed in this environment. |
| `make build` | Passed; stripped Linux amd64 binary remains below the repository's 30 MiB gate. Other release architectures were not rebuilt here. |
| `make perf` | Passed synthetic binary-size, palette/filter/render, post-sync first-frame, idle CPU, memory and startup-failure thresholds. These tests do not establish full interactive latency or five-minute RSS. |
| `make envtest` | Passed with managed Kubernetes 1.36 assets, including a real API-server edit-removal and concurrent-version-conflict test. |
| `make coverage-nfr` | Passed: cluster 82.1%, actions 75.2%, logs 77.8% at the measured run. Subsequent small UID-precondition additions have dedicated tests. |
| `ASTROTUI_E2E=1 ... make e2e` | Existing live action, log, exec and port-forward workflows passed on an isolated kind Kubernetes 1.32.2 cluster. |
| `go test -race -tags=e2e ./internal/ui -run TestLiveTUIResourceWorkflow -count=1` | Passed against that live cluster: full App.Run, initial resource list, watch arrival, live filter/cancel, namespace change, and shutdown. |
| Terminal fixture | Actual tcell frames checked at 80×24 and 120×36; 120×24 fixture exported to `docs/screenshots/astrotui-main.svg` and visually inspected. |
| `git diff --check` | Passed. |

The initially requested kind node image `v1.36.0` was unavailable; the installed kind default `v1.32.2` was used successfully. This is a compatibility data point, not a supported-version matrix. The real API envtest suite used 1.36 independently.

Not run: the five-minute `make nfr-live` gate, macOS/ARM terminal sessions, real cloud exec-auth providers, external AI endpoints, signed release publication, and comparative timed k9s operator trials. Synthetic/headless timing is not evidence of interactive first-frame performance. Optional integration skips are not passes of the underlying behavior.

## Implementation notes and regression map

- **UI ownership:** background producers submit bounded updates through a reserved synthetic F64 wake event. The event loop commits widgets and visible objects together. Pending resource paints are coalesced; generation checks discard work from previous views/contexts. Cancellation can unblock a producer even after the terminal stops. Tests cover queue cancellation and stale refresh rejection.
- **Selection:** UID is authoritative, with namespace/name fallback only when UID is absent. Filtering/removal clamps selection to an available row or no object. Sorting has deterministic ties. Destructive requests carry the selected UID so replacement objects cannot be deleted by an old confirmation.
- **Context/scope:** old cache context is cancelled before replacement. Namespace switches read the informer index immediately. The existing all-namespace watch permission requirement is unchanged and remains P2.
- **Input:** table controls distinguish actual printable keys from control-key runes. Palette navigation keeps the search field focused and skips headings. Filter changes are validated before replacing visible results. Tests cover real key events and a live API source; namespace totals account for controller-created ConfigMaps.
- **Mutations:** edits use JSON merge-patch semantics (including nulls for removal), normalize the original resource version, and reject identity changes. The historic `StrategicMergePatch` helper name remains for compatibility but its transport is JSON merge patch, not Kubernetes strategic merge patch. Version conflicts are surfaced with a diff.
- **Logs:** retention is O(1) per append once full; entries are sorted for merged display/export. Ingestion signals batched rendering rather than waiting for terminal painting. Display pause leaves ingestion active. Long lines are bounded and marked; EOF flushes the remaining fragment. Source identity and wrapped-line regex navigation still need P6.
- **AI/plugins:** protocol behavior is largely unchanged. Stream sends now honor cancellation; Ollama receives supplied context; model output and logs are rendered literally. This is not a redaction solution. Plugin foreground stdio and background process reaping are wired; arbitrary plugins are blocked in read-only policy. Scope and hotkey compatibility remain P8.
- **Persistence/distribution:** same-directory private temporary session files are synced and renamed; errors are returned. Installer downloads to a temporary directory, requires a matching SHA-256 entry, verifies before extraction, and installs only after success. Both valid and tampered archives are executed through mocked-download tests.
- **Visual evidence:** `ASTROTUI_SCREENSHOT=/absolute/path.svg go test ./internal/ui -run TestResourceScreenFixture -count=1` regenerates the fixture screenshot. It is rendered by tview/tcell, not a hand-drawn approximation or a live production cluster.

Known residual operational limits include synchronous API calls on some UI paths; retained inactive-resource informers within the active context; watch error text that may persist until the next object event on an otherwise empty recovered watch; incomplete metrics and CRD discovery; and a log view whose live paint can supersede an in-place AI summary. These are not hidden by the corrective checklist and must be resolved before an unqualified daily-driver/parity release.

