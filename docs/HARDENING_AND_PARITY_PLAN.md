# Astronomer TUI hardening and k9s parity plan

## Objective

Make `astrotui` a safe daily-driver Kubernetes TUI with a smaller, clearer architecture than k9s while matching the workflows operators rely on. "Better than k9s" is treated as a measurable product goal, not a one-time claim: parity requires sustained feature, compatibility, performance, security, and release work.

Comparison baseline: k9s commit `2e9b2062d6579c6437b3666dbfb2fae1b235de89` (2026-07-05). At that snapshot k9s has roughly 74k Go LOC across 522 Go files; this project has roughly 11k Go LOC across 55 Go files.

## Release gates

Every release must pass:

- `go test -race ./...`
- `go vet ./...` and configured static/security linters
- focused envtest and kind tests for list/watch, mutation, logs, exec, and port-forward
- artifact checksum/signature verification and signed container publication
- the documented cold-start, render latency, idle CPU, RSS, and binary-size budgets
- a completed capability matrix against the current k9s release

## Phase 0 — immediate safety and security fixes

- [x] Refresh and clear selected objects atomically on namespace/context switches; never allow actions against stale rows.
- [x] Give every cache an owned cancellation context and stop old informers on namespace/context changes and application exit.
- [x] Enforce context policy for plugins and port-forward operations.
- [x] Require explicit operator approval before executing any AI-suggested action.
- [x] Redact Secret data before any AI request leaves the process.
- [x] Require HTTPS for credentialed non-loopback AI endpoints.
- [x] Preserve resource identity/version preconditions during manifest edits.
- [x] Resolve named Service `targetPort` values against the selected Pod.
- [x] Escape cluster/provider text before it reaches tview markup-aware renderers.
- [x] Bound log-line size, aggregate buffer bytes, AI output, and redraw frequency.
- [x] Use atomic private writes for session state and log exports; reject unsafe import destination symlinks.
- [x] Verify signed release checksums in the installer and document a versioned installer URL.

## Phase 1 — trustworthy Kubernetes core

- [x] Namespace-scoped caches by default, with an explicit all-namespaces mode and configurable object/byte budgets.
- [x] Discovery-driven aliases, preferred versions, namespaced/cluster-scoped metadata, and CRD printer columns.
- [x] Immutable action targets carrying context, GVR, namespace, name, UID, and resourceVersion from selection through confirmation and execution.
- [x] Server-side dry-run/diff previews for edit, delete, scale, restart, and plugin-backed mutations where supported.
- [x] Watch recovery with relist/backoff, visible stale/error state, and deterministic subscription cleanup.
- [x] Kubernetes impersonation flags and a visible active identity/RBAC summary.

Wave 2 includes discovery-driven preferred-resource aliases and mutation availability, live CRD printer columns, Kubernetes impersonation flags, `:auth` server identity and per-resource RBAC summaries, mutation `SelfSubjectAccessReview` preflight, immutable target binding, and server dry-run previews for delete, edit, scale, restart, and explicitly declared mutating plugins.

## Phase 2 — k9s workflow parity

- [ ] Complete built-in resource/action matrix, including Jobs/CronJobs, DaemonSets, RBAC, storage, networking, autoscaling, and CRDs.
- [x] Describe/events/YAML views, owner navigation, resource graph/xray, and related-resource jumps.
- [x] Multi-container logs with previous logs, timestamps, since/tail, wrap, search, save, and workload aggregation.
- [ ] Shell/exec container selection, copy, attach, port-forward lifecycle, and benchmark management.
- [ ] Context/namespace/all-namespaces switching, aliases, hotkeys, skins, columns, plugins, and read-only mode compatibility.
- [ ] Pulses/health views and optional vulnerability/image scanning without sending cluster data externally by default.

Wave 3 adds generic describe/YAML/events views, controller-owner jumps, related-resource menus, bounded owner/xray graphs, all-container workload logs with advanced request options, DaemonSet restart/log actions, Job and CronJob suspend/resume previews, Pod attach with RBAC preflight, and a bounded `:health` pulse view. Copy workflows, benchmark management, image scanning, and the remaining compatibility matrix stay open.

## Phase 3 — differentiated capabilities

- [ ] Global fuzzy palette across resources, actions, contexts, namespaces, history, and plugins with bounded top-k search.
- [ ] Production guardrails with policy-as-code, typed confirmations bound to immutable targets, and an operator-visible audit log.
- [ ] Logs deluxe: bounded multi-source merge, structured fields, correlation to events/owners, saved searches, and export redaction.
- [ ] AI remains opt-in and advisory by default, with local-provider support, per-request disclosure preview, redaction, output limits, and no implicit mutations.
- [ ] Crash recovery/session restore that never restores a target more privileged than the operator explicitly selected.

## Phase 4 — distribution and ecosystem

- [ ] Signed archives, checksums, SBOMs, provenance attestations, and signed multi-arch container images.
- [ ] Homebrew, Krew, Winget/Scoop, package-manager, and reproducible source-build paths.
- [ ] Backward-compatible configuration migrations and a compatibility test corpus for k9s aliases, skins, hotkeys, and plugins.
- [ ] Supported-version policy, security policy, disclosure process, and automated dependency/advisory monitoring.

## Capability scorecard

Track each row as `missing`, `partial`, `parity`, or `better`, with an automated or manual acceptance test linked from the row.

| Area | Current assessment | Exit criterion |
|---|---|---|
| Resource discovery/rendering | Partial | Preferred-version CRDs and all common built-ins render correctly under envtest/kind. |
| Navigation and search | Partial/better concept | Bounded global palette plus owner/related-resource navigation. |
| Mutation safety | Partial | Immutable target binding, policy enforcement, preview, conflict handling, and audit record. |
| Logs | Partial | Bounded multi-pod streaming with the operational controls listed above. |
| Exec/port-forward | Partial | Full container/Service resolution, policy enforcement, lifecycle cleanup, and E2E coverage. |
| Configuration/plugins | Partial | k9s compatibility corpus plus secure execution contract. |
| Observability/health | Missing | Pulses, resource graph, and health/problem summaries. |
| Distribution | Partial | Verified installer, signed images, SBOM/provenance, and package managers. |
| Security posture | Partial | No open high/critical findings; medium findings are fixed or explicitly accepted. |
| Performance | Partial | NFRs enforced in CI against representative 1k-resource clusters. |

## Definition of "as good as or better than k9s"

The claim is earned only when every scorecard row is `parity` or `better`, the release gates pass on all supported platforms, the compatibility suite covers the current k9s configuration surface, and no known high-severity security defect remains open. Until then, documentation should describe `astrotui` as an initial implementation or preview rather than a drop-in replacement.
