# astrotui — k9s Parity-Plus Implementation Plan

Status: **IN PROGRESS** — Phase 0 (refactors), Phase 1 (metrics), Phase 2
(drill-down/selection), Phase 3 (filter/coloring/multi-select), Phase 5
(scale/restart/cronjob/kill/attach/secret/cordon/drain), Phase 6 (logs), and a
Phase 4 subset (namespace favorites, contexts view, discovery commands)
implemented and tested under `-race`. Remaining: Phase 4 remainder
(autocomplete, views.yaml, hotkeys.yaml, screendump, config flags), Phase 7
(pulses/xray/popeye/helm/rbac), Phase 8 (palette/AI v2), Phase 9
(perf/docs/release). See [`docs/reviews/k9s-capability-review.md`](../reviews/k9s-capability-review.md)
for the running status and [`docs/k9s-migration.md`](../k9s-migration.md) for the key map.
Date: 2026-07-06
Baseline commit: `180848c` (branch `claude/capability-review-k9s-gt2k39`)
Companion doc: [`docs/reviews/k9s-capability-review.md`](../reviews/k9s-capability-review.md) (verified gap audit this plan is built from)
Product spec: [`docs/specs/i-want-to-make-a-clone-of-k9s-but-call-it-astronomer-tui-and.md`](../specs/i-want-to-make-a-clone-of-k9s-but-call-it-astronomer-tui-and.md)

---

## 1. Purpose and Definition of Done

This plan takes astrotui from "solid foundation, behind k9s in daily-driver
ergonomics" to **100% k9s-capable or beyond, plus our differentiators fully
hardened**. It is written to be executed task-by-task with no additional
design work required: every task names the files to touch, the shapes to add,
the edge cases to handle, the acceptance criteria, and the tests that prove it.

**Definition of Done for the whole plan:**

1. Every row of the Feature Inventory (§3) is `DONE` or explicitly
   `WON'T-DO` with a written rationale in this file.
2. Every keybinding in the Keybinding Parity Map (§4) behaves as specified.
3. A k9s user can switch to astrotui and lose **zero** daily workflows:
   browse → filter → drill down → describe → logs → exec → edit → scale →
   port-forward → cordon/drain → bulk delete, across builtins and CRDs, with
   metrics visible, in any namespace scope, on any context.
4. Our five headline differentiators (palette, AI, logs deluxe, prod guard,
   QoL/session) each have at least one capability k9s cannot match, all
   covered by tests.
5. All NFRs re-validated post-implementation (§9.4): cold start ≤ 300ms on a
   1k-pod cluster, re-render ≤ 100ms, idle CPU ≤ 2%, RSS ≤ 150MB, binary
   ≤ 30MB. Metrics polling and the navigation stack must not regress these.
6. `make` (lint + race tests + build), `make envtest`, and
   `ASTROTUI_E2E=1 make e2e` against kind all green in CI.

**Explicit non-goals (unchanged from spec, restated so nobody re-litigates
mid-implementation):** web UI, OIDC token refresh, in-cluster SA auth,
cost overlay, Homebrew/Krew. Helm and benchmarking get scoped decisions in
§7 (they are k9s features, so "100% capable" requires a stance — see
tasks P7-1/P7-2).

---

## 2. Current State (what implementation starts from)

Verified at baseline commit. Read this before touching anything.

### 2.1 What exists and works

| Area | Where | Notes |
| --- | --- | --- |
| Kubeconfig + exec auth | `internal/cluster/client.go` | QPS 50 / Burst 100, memcached discovery |
| Informer cache | `internal/cluster/cache.go` | `Ensure/Subscribe/List/WaitForSync`, cluster-wide informers, client-side ns filter via indexer |
| Discovery resolution | `cluster.ResolveResource` | plural/singular/shortname/kind/`plural.group` |
| Table UI | `internal/ui/app.go` | single `tview.Table`, root-swapping modals, typed sort (`views.CompareCellValues`), substring filter |
| Renderers | `internal/ui/views/renderers.go` | ~30 builtins; pure functions of one `*unstructured.Unstructured`; CRD printer columns via `App.rendererFor` |
| Actions | `internal/actions/` | delete/edit(+3-way conflict diff)/scale/restart (Deploy+STS), exec (SPDY), port-forward + manager |
| Safety guard | `internal/safety/guard.go` | per-context policy: `read-only` / `confirm-destructive` / `full`; typed context-name confirmation |
| Logs | `internal/logs/`, `internal/ui/app_logs.go` | multi-pod merge, ring buffer (10k), JSON/logfmt parser + `key=val`/`key~=regex` filter exprs, regex search n/N, save, jump events/owner, AI summary/chat |
| Palette | `internal/palette/`, `app_palette.go` | fuzzy, categories: resources/actions/contexts/namespaces/plugins/AI (`>`/`?` NL intent) |
| Plugins | `internal/plugins/` | k9s `plugins.yaml` drop-in, `$VAR`/`${VAR}` expansion, confirm + background |
| Config | `internal/config/` | strict-schema YAML, `config import-k9s` (skin/hotkeys/aliases migration) |
| Session | `internal/session/` | context/ns/view/filter restore |
| Theme | `internal/ui/theme/` | skins + live file-watch reload |
| AI | `internal/ai/` | anthropic/openai/ollama, streaming, failure/logs/manifest/NL-action features, off by default |
| Recovery | `internal/ui/recovery.go` | panic capture → crash file → modal |
| Test harness | `test/envtest`, `test/kind`, `test/distribution` | envtest CRD/watch, kind e2e, perf/NFR gates, release checks |

### 2.2 Known architectural constraints this plan must break

These four constraints cause most remaining gaps. The refactors in §5 remove
them **first**, because nearly every feature task depends on at least one.

1. **C1 — No navigation container.** Every view swaps `app.SetRoot(...)`.
   There is no stack, so there is no Esc-back, no breadcrumbs, no drill-down.
2. **C2 — Renderer is a pure function of one object.**
   `Row(*unstructured.Unstructured) []Cell` cannot render metrics (needs a
   join against metrics.k8s.io), relationship columns (pod count per node),
   or state-derived colors that depend on config.
3. **C3 — Errors are swallowed.** `refresh()` returns silently on list
   errors; port-forward run errors go to `io.Discard`; informer sync failures
   render as an empty table. There is no toast/flash primitive.
4. **C4 — One hardcoded key table.** `Run()`'s input capture is a fixed if
   ladder; k9s `hotkeys.yaml`, per-view keys, numbered namespace favorites,
   and command autocomplete all need a registry instead.

---

## 3. Feature Inventory — the parity contract

Legend: ✅ done at baseline · 🟡 partial · ❌ missing · ➕ beyond-k9s.
"Task" points into §6. This table is the checklist for Done-ness; update the
Status column as tasks land.

### 3.1 Navigation & core UX

| # | Capability (k9s reference behavior) | Status | Task |
| --- | --- | --- | --- |
| N1 | Resource browse via `:alias` incl. CRDs, shortnames | ✅ | — |
| N2 | View stack: Enter drills down, Esc goes back, breadcrumb trail visible | ❌ | P2-1..P2-4 |
| N3 | Workload → pods drill-down (selector-scoped), pod → containers view | ❌ | P2-3 |
| N4 | Container view: per-container status/restarts/image + logs/exec on selection | ❌ | P2-3 |
| N5 | Node → pods-on-node drill-down | ❌ | P2-3 |
| N6 | Service/Ingress → backing pods/endpoints | ❌ | P2-3 (stretch within task) |
| N7 | Header pane: context, cluster, user, k8s version, astrotui version, CPU/MEM gauges, key hints | ❌ | P1-4, P2-5 |
| N8 | Crumbs pane at bottom (`pods > api-7f9 > containers`) | ❌ | P2-2 |
| N9 | `:q`/`:quit`, `Ctrl+C` quit | ✅ | — |
| N10 | Command autocomplete + history in `:` prompt (Tab complete, ↑/↓ history) | ❌ | P4-2 |
| N11 | All-namespaces (`0`, `:all`, `:ns all`) | ✅ | — |
| N12 | Numbered namespace favorites `1`–`9` (recent/configured namespaces) | ❌ | P4-3 |
| N13 | Contexts view `:ctx` as a first-class table (not just palette) | ❌ | P4-4 |
| N14 | Namespace view Enter = switch active namespace | ❌ | P2-3 |
| N15 | Aliases view `:alias` listing every command/alias | ❌ | P4-5 |
| N16 | Screen-dump/snapshot current table to file (`:screendump`, k9s `s` on some views) | ❌ | P4-6 |

### 3.2 Table features

| # | Capability | Status | Task |
| --- | --- | --- | --- |
| T1 | Live watch-driven updates | ✅ | — |
| T2 | Typed column sort + indicator (numeric/age/fraction) | ✅ | — |
| T3 | Sort hotkeys per semantic column (k9s: `shift-n` name, `shift-a` age, `shift-s` status, `shift-r` ready/restarts, `shift-c`/`shift-m` cpu/mem) | ❌ | P1-5 |
| T4 | Regex filter `/foo.*bar` | ❌ | P3-1 |
| T5 | Inverse filter `/!pattern` | ❌ | P3-1 |
| T6 | Fuzzy filter `/-f term` | ❌ | P3-1 |
| T7 | Label-selector filter `/-l app=x,tier!=db` (server-side where possible) | ❌ | P3-2 |
| T8 | Row coloring by state (pending yellow, error red, terminating purple, completed gray) | ❌ | P3-3 |
| T9 | Multi-select: Space marks, `Ctrl+Space` range-mark, marked-count in title | ❌ | P3-4 |
| T10 | Bulk actions on marked set (delete, label?, at minimum delete) | ❌ | P3-5 |
| T11 | Wide mode toggle (`Ctrl+W`): show/hide LABELS + extra columns | ❌ | P3-6 |
| T12 | Custom per-resource columns (k9s `views.yaml` equivalent) | ❌ | P4-7 |
| T13 | Column truncation/ellipsis + full value in describe (don't blow table width) | 🟡 (tview default) | P3-6 |
| T14 | Auto-refresh cadence config + manual refresh key (`Ctrl+R` in k9s reloads) | ❌ | P3-7 |
| T15 | Deterministic order on sort ties | ✅ | — |

### 3.3 Resource verbs

| # | Capability | Status | Task |
| --- | --- | --- | --- |
| V1 | describe (`d`, with events) / yaml (`y`) / edit (`e`) / delete (`Ctrl+D`) | ✅ | — |
| V2 | Delete options: cascade/orphan, grace period prompt (k9s delete dialog) | ❌ | P5-1 |
| V3 | Kill = delete `--grace-period=0 --force` (`Ctrl+K`) | ❌ | P5-1 |
| V4 | Scale: Deployment, StatefulSet | ✅ | — |
| V5 | Scale: ReplicaSet; scale prompt shows current replicas | 🟡 | P5-2 |
| V6 | Restart: Deployment, StatefulSet, **DaemonSet** | 🟡 | P5-2 |
| V7 | Rollout undo / rollback to previous revision (deploy/sts/ds) | ❌ | P5-3 |
| V8 | Rollout status / revision history in describe for workloads | ❌ | P5-3 |
| V9 | CronJob: trigger now (create Job from template) | ❌ | P5-4 |
| V10 | CronJob: suspend / resume toggle | ❌ | P5-4 |
| V11 | Node: cordon / uncordon | ❌ | P5-5 |
| V12 | Node: drain (with grace, ignore-daemonsets, delete-emptydir flags dialog) | ❌ | P5-5 |
| V13 | Node shell (privileged debug pod with nsenter, opt-in config) | ❌ | P5-6 |
| V14 | Pod: attach (`a`) | ❌ | P5-7 |
| V15 | Secret: base64 decode view (`x` in k9s) | ❌ | P5-8 |
| V16 | ConfigMap/Secret: copy value to clipboard (OSC52) | ❌ | P5-8 |
| V17 | Port-forward: choose container/port from declared list, custom address, multiple simultaneous | 🟡 (first port only, localhost only) | P5-9 |
| V18 | Port-forward failure surfaced; manager shows status column | ❌ (errors discarded) | P5-9 |
| V19 | PVC/PV navigation (pvc → pv → storageclass describe hops) | ❌ | P2-3 (stretch) |
| V20 | Toggle decode on `describe` for secrets referenced in env/volumes | ❌ | P5-8 (stretch) |

### 3.4 Metrics

| # | Capability | Status | Task |
| --- | --- | --- | --- |
| M1 | Pod CPU/MEM current usage columns | ❌ (columns removed at baseline pending data source) | P1-1..P1-3 |
| M2 | Pod %CPU/R, %CPU/L, %MEM/R, %MEM/L vs requests/limits | ❌ | P1-3 |
| M3 | Node CPU/MEM usage + allocatable + % columns | ❌ | P1-3 |
| M4 | Node pod-count column (pods on node / allocatable pods) | ❌ | P1-3 |
| M5 | Cluster CPU/MEM gauges in header | ❌ | P1-4 |
| M6 | Graceful degradation when metrics-server absent (`n/a`, no errors, no polling storm) | ❌ | P1-2 |
| M7 | Sort by CPU/MEM columns numerically (needs unit-aware compare: `123m`, `1.2Gi`) | ❌ | P1-5 |

### 3.5 Logs

| # | Capability | Status | Task |
| --- | --- | --- | --- |
| L1 | Follow, multi-pod merge, per-pod color | ✅ | — |
| L2 | Container picker for multi-container pods | ✅ | — |
| L3 | Previous-container logs toggle (`p`) | ❌ (plumbing exists: `RequestOptions.Previous`) | P6-1 |
| L4 | Since selector (`1`=1m `2`=5m `3`=15m `4`=1h `0`=all in k9s style) + tail-lines config | ❌ (plumbing: `TailLines`) | P6-1 |
| L5 | Timestamps toggle (`t`) | 🟡 (always on when present) | P6-1 |
| L6 | Autoscroll toggle (`s` in k9s; we use `Ctrl+S` to avoid save clash) + scroll position preserved when off | ❌ | P6-2 |
| L7 | Fullscreen toggle (`f`) | ❌ | P6-2 |
| L8 | Wrap toggle (`w`) | ✅ | — |
| L9 | Structured filter exprs `key=val`, `key~=regex` wired into UI (parser exists, UI uses raw substring) | 🟡 | P6-3 |
| L10 | Regex search + n/N | ✅ | — |
| L11 | Save to file | ✅ | — |
| L12 | Clear buffer (`c`) | ❌ | P6-2 |
| L13 | Jump to events/owner from log line | ➕ | — |
| L14 | AI summarize/chat over filtered window | ➕ | — |
| L15 | JSON pretty-toggle: render parsed fields as aligned columns | ➕ (parser exists) | P6-3 |

### 3.6 Cluster health & extra views

| # | Capability | Status | Task |
| --- | --- | --- | --- |
| X1 | Pulses view (`:pulses`): live tiles of deployments/pods/events health deltas | ❌ | P7-3 |
| X2 | XRay view (`:xray <kind>`): dependency tree (deploy→rs→pod→container, with status glyphs) | ❌ | P7-4 |
| X3 | Popeye scan integration (`:popeye`) | ❌ | P7-5 |
| X4 | RBAC views: `:rbac` (who-can), can-i checks on current user | ❌ | P7-6 |
| X5 | Events view with type coloring + auto-follow | 🟡 (plain table) | P3-3 covers coloring |
| X6 | Helm views (`:helm` releases, history, values) | ❌ — **decision task** | P7-1 |
| X7 | Benchmark (`hey`) integration | ❌ — **decision task** | P7-2 |
| X8 | Cluster-connectivity indicator + reconnect banner | ❌ | P3-8 |

### 3.7 Config & customization

| # | Capability | Status | Task |
| --- | --- | --- | --- |
| K1 | Skins + live reload | ✅ | — |
| K2 | `hotkeys.yaml` native runtime support (F-keys / custom chords → commands) | ❌ (import-only) | P4-8 |
| K3 | `aliases.yaml` native runtime support (custom `:alias` → command) | ❌ (import-only) | P4-8 |
| K4 | Per-view custom columns (`views.yaml`) | ❌ | P4-7 |
| K5 | Config for: refresh rate, max log tail, default since, readOnly global flag, noIcons | ❌ | P4-9 |
| K6 | `--readonly`, `--command`, `--namespace`, `--context` CLI flags (k9s CLI surface) | 🟡 (`--context/--namespace` exist) | P4-9 |
| K7 | Strict-schema config errors with line/col | ✅ | — |
| K8 | import-k9s migration | ✅ | — |

### 3.8 Beyond-k9s differentiators (harden + extend)

| # | Capability | Status | Task |
| --- | --- | --- | --- |
| B1 | Palette: fuzzy everything | ✅ | — |
| B2 | Palette: index *live object names* of every cached GVR (jump to any pod by name from anywhere) | 🟡 (current view only) | P8-1 |
| B3 | Palette: recent-commands section + frecency ranking | ❌ | P8-1 |
| B4 | AI: explain selected failure (pod crash/oom/imagepull) with events context | ✅ | — |
| B5 | AI: `:ai` chat view with cluster context injection (current view, selection) | ❌ | P8-2 |
| B6 | AI: NL → action with argument extraction ("scale api to 5") incl. safety-guard passthrough | 🟡 (verb-only mapping) | P8-2 |
| B7 | Prod guard: typed confirmation, per-context policy | ✅ | — |
| B8 | Prod guard: bulk-action aware (multi-delete requires one typed confirm listing all N targets) | ❌ | P3-5 |
| B9 | Session restore incl. nav stack + marks | 🟡 (view/ns/filter only) | P2-6 |
| B10 | Port-forward manager: persistent across restarts (reattach on startup prompt) | ❌ | P5-9 (stretch) |
| B11 | Live `?` overlay generated from real keymap incl. plugins & hotkeys | 🟡 (static list + catalog) | P4-1 |

---

## 4. Keybinding Parity Map (the contract for muscle memory)

Rules: k9s bindings win when they don't conflict with our differentiators;
every binding must appear in the `?` overlay, sourced from the registry
(P4-1), never hardcoded in help text. Table = final state after this plan.

### 4.1 Global (any view)

| Key | Action | Source |
| --- | --- | --- |
| `?` | Help overlay (generated from keymap registry) | exists |
| `Ctrl+P` | Command palette | exists (ours) |
| `:` | Command prompt with autocomplete + history | P4-2 |
| `/` | Filter prompt (regex/inverse/fuzzy/label modes) | P3-1 |
| `Esc` | Pop navigation stack / clear filter if set / no-op at root | P2-1 |
| `Ctrl+C`, `:q` | Quit | exists |
| `0` | All namespaces | exists |
| `1`–`9` | Namespace favorites | P4-3 |
| `Ctrl+R` | Force refresh current view + clear informer error banner | P3-7 |
| `Ctrl+W` | Wide-columns toggle | P3-6 |
| `Space` | Mark row | P3-4 |
| `Ctrl+Space` | Mark range | P3-4 |
| `Ctrl+\` | Clear all marks | P3-4 |
| `Shift+J`/`Shift+K` | Jump to next/prev marked row | P3-4 |
| F-keys / user chords | `hotkeys.yaml` | P4-8 |

### 4.2 Resource table

| Key | Action | Source |
| --- | --- | --- |
| `Enter` | Drill down (contextual: workload→pods, pod→containers, ns→switch, node→pods, else describe) | P2-3 |
| `d` | Describe (yaml + events + rollout history where applicable) | exists, P5-3 extends |
| `y` | YAML | exists |
| `e` | Edit in `$EDITOR` (3-way merge conflict detection) | exists |
| `Ctrl+D` | Delete dialog (cascade/orphan/grace) — bulk-aware | P5-1, P3-5 |
| `Ctrl+K` | Kill (grace 0) — bulk-aware | P5-1 |
| `l` | Logs | exists |
| `x` | Exec shell (pod/container) | exists |
| `a` | Attach (pod/container) | P5-7 |
| `f` | Port-forward dialog (container/port list, address, local port) | P5-9 |
| `s` | Scale (workloads) / decode toggle (secrets) / shell (node, if enabled) — per-view registry | P5-2/P5-8/P5-6 |
| `r` | Rollout restart (deploy/sts/**ds**) | P5-2 |
| `Ctrl+L` | Rollout undo | P5-3 |
| `t` | Trigger (cronjob) | P5-4 |
| `z` | Suspend/resume toggle (cronjob) | P5-4 |
| `c`/`u` | Cordon / Uncordon (node) | P5-5 |
| `Shift+D` | Drain dialog (node) | P5-5 |
| `Shift+N`/`Shift+A`/`Shift+S`/`Shift+R`/`Shift+C`/`Shift+M` | Sort by NAME/AGE/STATUS/READY|RESTARTS/CPU/MEM | P1-5 |
| `Shift+1..9` | Sort by column N (kept from baseline) | exists |
| `Ctrl+E` | AI explain selection | exists (ours) |
| `Ctrl+S` | Screendump table to CSV/txt | P4-6 |

### 4.3 Logs view

| Key | Action | Source |
| --- | --- | --- |
| `/` | Filter (raw regex or `key=val` / `key~=re` structured) | P6-3 |
| `Ctrl+R` | Regex search prompt | exists (renamed in help) |
| `n`/`N` | Next/prev match | exists |
| `p` | Previous-container logs toggle | P6-1 |
| `t` | Timestamps toggle | P6-1 |
| `w` | Wrap toggle | exists |
| `Ctrl+S` | Autoscroll toggle | P6-2 |
| `F` | Fullscreen toggle | P6-2 |
| `C` | Clear buffer | P6-2 |
| `1/2/3/4/0` | Since 1m/5m/15m/1h/all (restart streams) | P6-1 |
| `s` | Save to file | exists |
| `j` | JSON/pretty parsed-field rendering toggle | P6-3 |
| `g e` / `g o` / `g a` | Jump events / owner / AI summary | exists (ours) |
| `Ctrl+E` | AI chat about logs | exists (ours) |
| `Esc`/`q` | Back (pops to table via nav stack) | P2-1 |

Conflicts resolved by decision: k9s uses `s` for shell-on-pod; we keep `x`
for exec (already shipped, documented) and reuse `s` contextually per view.
k9s uses `Ctrl+K` kill — adopted. k9s `Ctrl+W` wrap in logs — ours is `w`
(kept); `Ctrl+W` is wide-mode globally. Every divergence must be listed in
`docs/k9s-migration.md` (P9-3).

---

## 5. Architecture refactors (do these first)

### R1 — Navigation stack + persistent layout  *(unblocks N2–N8, L-view back behavior, X-views)*

**Problem.** `app.SetRoot()` swaps destroy history (C1). No breadcrumbs, no
Esc-back, no header/crumb chrome.

**Design.**

- New file `internal/ui/nav.go`:

  ```go
  type Page struct {
      Name    string           // breadcrumb label, e.g. "pods", "api-7f9c(logs)"
      Widget  tview.Primitive
      OnFocus func()           // re-arm timers/streams when revealed
      OnPop   func()           // cancel contexts, stop streams
  }

  type Nav struct {
      pages *tview.Pages
      stack []Page
  }
  func (n *Nav) Push(p Page)
  func (n *Nav) Pop() bool          // false at root
  func (n *Nav) Replace(p Page)     // for :resource switches (root swap, stack reset)
  func (n *Nav) Crumbs() []string
  ```

- New root layout in `App.Run` (replaces bare table root):

  ```
  ┌ header (P2-5): ctx | cluster | user | k8s ver | astrotui ver | CPU▕▎ | MEM▕▎ | hints ┐
  │ Nav.pages (resource table = root page)                                              │
  ├ crumbs bar: pods > api-7f9c > containers                                            ┤
  └ flash/toast line (R3)                                                               ┘
  ```

- **Every** current `a.app.SetRoot(view, true)` call site is converted:
  - modal prompts (filter/command/confirm inputs) → `Nav.PushModal` variant
    using `tview.Pages.AddPage(..., true /*overlay*/)` so the table stays
    visible behind them (k9s look);
  - full views (logs, detail, port-forward manager, palette) → `Nav.Push`.
- `Esc` policy in one place (`App.handleEsc`): (1) if a modal/overlay is up →
  close it; (2) else if filter non-empty → clear filter; (3) else →
  `Nav.Pop()`; (4) at root → no-op.
- All pushed pages that own goroutines (log streams, watch subscriptions,
  metrics tickers) must register cancel in `Page.OnPop`. Grep-audit
  checklist: `showLogsView` (stream ctx), `switchResource` watcher (root only),
  splash timer, AI streams.

**Files touched:** `app.go` (Run/root layout/every SetRoot), `app_logs.go`,
`app_palette.go`, `app_exec.go`, `app_portforward.go`, `app_ai.go`,
`splash.go`, new `nav.go`, new `nav_test.go`.

**Acceptance:** deep chain deploy→pods→pod→containers→logs then 4×Esc lands
back on deployments with prior filter/sort/selection intact at every level
(selection restore = store selected row name per stacked page, reselect by
name on reveal). Unit tests drive Nav directly; e2e drives keys via
`tcell.SimulationScreen` (pattern already used in `app_test.go`).

**Est:** L (2–3 focused days of work incl. call-site migration + tests).

### R2 — RowContext-aware renderers  *(unblocks M1–M7, T8, N4, X-views)*

**Problem.** C2: `Row(u)` can't join metrics or sibling resources.

**Design.** Additive interface, zero breakage of the 30 builtins:

```go
// internal/ui/views/context.go
type RowContext struct {
    PodMetrics  map[string]PodMetrics   // key ns/name
    NodeMetrics map[string]NodeMetrics  // key name
    PodsByNode  map[string]int          // for node PODS column
    Now         time.Time               // deterministic AGE in tests
}
type ContextRenderer interface {
    Renderer
    RowCtx(*unstructured.Unstructured, RowContext) []Cell
}
```

- `BuildRows` gains a `RowContext` parameter; if the renderer implements
  `ContextRenderer` call `RowCtx`, else `Row`. All call sites (app refresh,
  headless render, tests) updated in one pass.
- `Cell` gains styling fields for T8/P1: `type Cell struct { Text string;
  Color string; Align int }` — renderers set semantic color names
  ("error","warn","ok","muted"); the app maps them through the active Skin
  (never hardcode tcell colors in views).
- Golden snapshot test regenerates via existing
  `ASTROTUI_UPDATE_GOLDEN=1` hook; snapshot must include a metrics-populated
  row and a metrics-absent (`n/a`) row.

**Est:** M.

### R3 — Flash/toast + error surfacing bus  *(unblocks T14, X8, V18, all silent failures)*

**Design.**

```go
// internal/ui/flash.go
type FlashLevel int // Info, Warn, Error
func (a *App) Flash(level FlashLevel, format string, args ...any)
```

- Renders into the bottom flash line (R1 layout), auto-clears after 5s
  (timer per message, latest wins), history viewable via `:flash` (last 50,
  ring buffer).
- Wire immediately at: `refresh()` list error; `WaitForSync` timeout (new —
  add 10s timeout + banner "waiting for <gvr>… RBAC?"); informer watch error
  handler (`SetWatchErrorHandler` on informers — add in `cluster.Ensure`);
  port-forward run error (V18); plugin exec error; AI provider error.
- `ResourceCache.Ensure` change: `informer.SetWatchErrorHandler(func(r, err){ publishErr(gvr, err) })`,
  new `SubscribeErrors(gvr) <-chan error`.

**Est:** M.

### R4 — Keymap + command registry  *(unblocks K2/K3, N10, B11, per-view keys)*

**Design.**

```go
// internal/ui/keymap.go
type KeyBinding struct {
    Key         tcell.Key; Rune rune; Mod tcell.ModMask
    Name        string  // "describe"
    Description string
    Scope       string  // "global" | "table" | "logs" | "pods" | "nodes" | ...
    Action      func(*App)
    Visible     bool    // show in ? overlay
}
type Keymap struct { bindings []KeyBinding }
func (k *Keymap) Handler(scopes ...string) func(*tcell.EventKey) *tcell.EventKey
func (k *Keymap) HelpRows(scopes ...string) [][2]string
```

- `Run()`'s if-ladder becomes registration calls; logs/detail/manager views
  register their own scopes. `?` overlay = `HelpRows` of active scopes +
  plugin shortcuts + hotkeys.
- Command registry replaces `parseCommand` switch:

  ```go
  // internal/ui/commands.go
  type CommandSpec struct {
      Names []string        // "q","quit"
      Args  int             // -1 variadic
      Run   func(*App, []string)
      Complete func(*App, string) []string // for P4-2 autocomplete
      Help  string
  }
  ```

  Resource fallthrough (alias table → discovery) remains the default when no
  command matches. `aliases.yaml` (K3) prepends user aliases; `hotkeys.yaml`
  (K2) binds keys to command strings through the same registry.

**Est:** M-L.

---

## 6. Phased task breakdown

Every task lists: goal, implementation detail, files, acceptance criteria
(AC), tests, size (S/M/L), dependencies. IDs are stable — reference them in
commits (`P1-2: ...`).

### Phase 0 — Refactors

| ID | Task | Size | Deps |
| --- | --- | --- | --- |
| P0-1 | R1 navigation stack + layout shell | L | — |
| P0-2 | R2 RowContext + styled Cell | M | — |
| P0-3 | R3 flash bus + informer/watch error wiring | M | P0-1 (layout line) |
| P0-4 | R4 keymap + command registry migration | M-L | P0-1 |

Phase-0 exit gate: all existing tests green, zero behavior change visible to
users except (a) Esc-back works for logs/detail, (b) errors flash instead of
vanishing. **Do not start Phase 1 until this gate passes** — everything
downstream builds on these seams.

### Phase 1 — Metrics (the #1 missed k9s feature)

**P1-1 — Metrics client + poller.** (M, deps P0-2)
New `internal/cluster/metrics.go`:

```go
type PodMetrics struct{ CPU, Mem resource.Quantity; Containers map[string]ContainerMetrics }
type NodeMetrics struct{ CPU, Mem resource.Quantity }
type MetricsPoller struct { /* dynamic client, interval, mu, maps, subs */ }
func NewMetricsPoller(dyn dynamic.Interface, interval time.Duration) *MetricsPoller
func (m *MetricsPoller) Start(ctx context.Context)
func (m *MetricsPoller) Snapshot() (map[string]PodMetrics, map[string]NodeMetrics, bool /*available*/)
func (m *MetricsPoller) Subscribe() <-chan struct{}   // tick after each successful poll
```

- Polls `metrics.k8s.io/v1beta1` `pods` + `nodes` via the **dynamic client**
  (no new dependency; parse unstructured into Quantities with
  `resource.ParseQuantity`). Interval from config `metrics.interval`
  (default 15s, min 5s).
- Availability probe once at startup via discovery (`ServerPreferredResources`
  contains the group); if absent → `available=false`, re-probe every 5min,
  **zero** error flashes (M6). 404/403 during polling → mark unavailable,
  back off to the 5min re-probe.
- AC: unit tests with `dynamicfake` covering: happy path quantities parsed;
  API absent; API disappears mid-run; namespace-scoped totals.

**P1-2 — Wire poller into App.** (S, deps P1-1, P0-2)
`main.go` constructs poller (skippable via `Options.MetricsPoller` for
tests); `App.refresh` builds `RowContext` from `Snapshot()`; poller tick
subscribes into the same redraw path as cache events (coalesce: max one
redraw per 250ms — reuse/introduce a debounce in `refresh`). Context switch
(P0 note): stop old poller, start new one (`switchContext`).

**P1-3 — Metrics columns.** (M, deps P1-2)
Restore to pods view: `CPU`, `MEM`, `%CPU/R`, `%CPU/L`, `%MEM/R`, `%MEM/L`;
nodes view: `CPU`, `%CPU`, `MEM`, `%MEM`, `PODS`. Rendering rules:
CPU always milli (`142m`), MEM in Mi/Gi (`512Mi`, `1.2Gi` — `humanizeQty`
helper + table-driven tests), `n/a` when unavailable (M6), percentages need
requests/limits summed from pod spec containers (helper
`podResourceTotals(u)`), node % vs `status.allocatable`. `PodsByNode` built
in App from the pods informer (only when nodes view active — don't Ensure
pods informer just for a column unless it already exists; if pods informer
not synced, render `n/a`).
AC: golden snapshot rows for with-metrics/without-metrics; percent math
unit-tested incl. zero-requests (render `n/a`, never div-by-zero).

**P1-4 — Header gauges.** (S, deps P1-3, P0-1)
Cluster CPU%/MEM% = Σ node usage / Σ allocatable, drawn as 10-char bar +
percent in header; hidden when metrics unavailable.

**P1-5 — Unit-aware sort + semantic sort keys.** (S, deps P1-3)
Extend `views.CompareCellValues` to parse `resource.Quantity` forms
(`142m` **CPU-milli vs minutes ambiguity**: resolve per-column — sort gets
column name; quantities only for CPU/MEM columns; implement via optional
`CompareCellValuesFor(column, a, b)`). Add `Shift+N/A/S/R/C/M` semantic keys
resolving to the column index by header name in the active renderer (T3, M7).

### Phase 2 — Navigation & drill-down

**P2-1 — Esc-back everywhere.** (S, deps P0-1) Single `handleEsc`; remove all
per-view Esc captures that SetRoot to table. AC in R1.

**P2-2 — Crumbs bar.** (S, deps P0-1) Renders `Nav.Crumbs()`, skin-colored.

**P2-3 — Contextual Enter / drill-down registry.** (L, deps P0-1, P0-4)
New `internal/ui/drill.go`:

```go
type DrillTarget struct{ GVR schema.GroupVersionResource; Filter string; LabelSelector string; Title string }
func drillFor(obj *unstructured.Unstructured, gvr schema.GroupVersionResource) (DrillTarget, bool)
```

Rules (each with unit test):
- Deployment/STS/DS/RS/Job → pods, **label-selector scoped** (uses P3-2
  selector plumbing, not name-substring), pushed as stacked page (not root
  Replace) so Esc returns.
- CronJob → jobs filtered by owner name.
- Pod → **container pseudo-view** (new `ContainersRenderer`, columns: NAME,
  IMAGE, READY, STATE, RESTARTS, CPU, MEM (per-container metrics from P1-1),
  PORTS; keys: `l` logs, `x` exec, `a` attach scoped to that container).
  Data source = the pod object itself, no informer needed; refresh by
  re-reading pod from cache on cache events for that pod.
- Node → pods with `spec.nodeName` field filter (client-side predicate on the
  informer list — add optional predicate to `BuildRows` path).
- Namespace → switch active namespace to selection (N14).
- Service → pods matching `spec.selector` (N6).
- PVC → its PV describe; PV → claim describe (V19, stretch — behind the same
  registry, cheap once registry exists).
- Everything else → describe (current behavior).
AC: e2e SimulationScreen test walks deploy→pods→containers→logs→Esc×3.

**P2-4 — Selection preservation.** (S, deps P0-1) On refresh keep selection
pinned to object key not row index (store `ns/name` of selected row; after
rebuild, reselect its new index; fall back to same index). Fixes selection
jumping on watch updates — table stakes vs k9s.

**P2-5 — Header pane.** (M, deps P0-1, P1-4) Context, cluster server host,
user, k8s server version (`Discovery.ServerVersion()`, cached 10min),
astrotui version, gauges, top-3 key hints for the active scope (from P0-4
registry). Live-updates on context switch.

**P2-6 — Session v2.** (S, deps P2-3) Persist nav stack summary (root view +
namespace + filter + sort col/dir), marks are **not** persisted (stale keys
risk), plus namespace favorites MRU list (feeds P4-3). Bump session schema
with backward-compat load (old files still parse; add `Version int`).

### Phase 3 — Table power features

**P3-1 — Filter modes.** (M, deps P0-4)
Grammar (documented in help + README):
`/text` substring (default) · `/re:pattern` or auto-detect regex metachars ·
`/!pattern` inverse · `/-f term` fuzzy (reuse palette's matcher) ·
`/-l selector` label (P3-2). Implementation: `internal/ui/filter.go`
`type RowFilter struct{ Mode, Expr string; re *regexp.Regexp; sel labels.Selector }`,
compiled once per change, `Matches(row []views.Cell, obj *unstructured.Unstructured) bool`;
`BuildRows` accepts `RowFilter` instead of raw string. Invalid regex/selector
→ Flash(Error) + keep previous filter. Title shows mode:
`filter=re:foo (12/240)` with match count (k9s shows counts — adopt).
AC: table-driven tests across all modes incl. invalid patterns.

**P3-2 — Label-selector filtering, server-side aware.** (M, deps P3-1)
`ResourceCache.List` already accepts a `labels.Selector` — pass parsed
selector down (cheap, client-side vs synced cache = fine, no new API load).
Drill-down (P2-3) reuses this. Session-restores with filter (P2-6).

**P3-3 — Row state coloring.** (M, deps P0-2)
`internal/ui/views/colors.go`: `func StateColor(gvr, u) string` → semantic
color per rules: pods (Running+ready=ok default fg, Pending/ContainerCreating
=warn, CrashLoop/Error/ImagePull=error, Terminating=muted-purple,
Completed=muted); nodes NotReady/cordoned=warn; events Warning=warn;
workloads ready<desired=warn; jobs failed=error. Skin gains
`ok/warn/error/muted` fields with dark/light defaults (theme.go). Applied in
one place where cells become `tview.TableCell`. AC: renderer tests assert
semantic names; skin mapping test.

**P3-4 — Multi-select.** (M, deps P0-4, P2-4)
`marks map[string]struct{}` keyed `ns/name` on App, cleared on view switch
(root Replace) but preserved across refresh & drill-return. Space toggles;
`Ctrl+Space` marks range from last-toggled to cursor; `Ctrl+\` clears;
mark glyph column 0 prefix (`◉ `) + row bold; title shows `[3 marked]`.

**P3-5 — Bulk actions + guard integration.** (M, deps P3-4, P5-1)
`Ctrl+D`/`Ctrl+K` with marks → single dialog listing all targets
(scrollable if >10), one typed-confirmation covering the batch (B8) —
`safety.Authorize` called once, then per-object delete with per-failure
Flash and a summary flash (`deleted 8/9 — 1 failed: ...`). Sequential
deletes with 4-way parallelism cap; context timeout 30s total.

**P3-6 — Wide mode + truncation.** (S, deps P0-2)
Renderers tag columns `Wide bool` (`Column{Name string; Wide bool}` —
LABELS, IMAGES, SELECTOR, OS-IMAGE, KERNEL, SERVICE-ACCOUNT, etc. wide).
Default hidden; `Ctrl+W` toggles (persist in session). Cell text truncated
to configurable max (default 50) with `…` — full values always in describe.

**P3-7 — Manual refresh + cadence.** (S, deps P0-3)
`Ctrl+R` → resync informer (`cache.Resync()` wrapper: re-list via informer
store is instant — actually re-runs `refresh()`; plus clears error banner and
re-probes metrics). Config `ui.refreshDebounceMs` for the redraw coalescing
introduced in P1-2.

**P3-8 — Connectivity indicator.** (S, deps P0-3, P2-5)
Header shows `●` green/yellow/red: green = last watch event or successful
poll <30s; yellow = reconnecting (informer error stream firing); red = API
unreachable >60s (background ping `/readyz` every 15s when yellow). Flash on
state transitions.

### Phase 4 — Commands, customization, views

**P4-1 — `?` overlay from registry.** (S, deps P0-4) Two-column, grouped by
scope, includes plugins (with their `shortCut`) and hotkeys; replaces
`HelpText` string builder; keep `HelpText` fn for AI catalog input (B6 uses
it) but source it from the registry too.

**P4-2 — Command autocomplete + history.** (M, deps P0-4)
Tab cycles completions (commands, then resource aliases, then discovery
resources, then arg completion: namespaces for `:ns`, contexts for `:ctx`);
inline ghost-text suggestion; ↑/↓ history (last 100, persisted in session
dir `command_history`, dedup consecutive). AC: unit tests on the completer
fn per prefix class.

**P4-3 — Namespace favorites `1`–`9`.** (S, deps P2-6)
MRU namespaces list (session) merged with config `namespaces.favorites`;
keys 1–9 switch; shown in `:ns` completion first; `?` overlay lists current
assignments.

**P4-4 — Contexts view.** (S, deps P0-4)
`:ctx` bare → table view (NAME, CLUSTER, USER, NAMESPACE, ACTIVE✓) from
RawConfig; Enter switches (existing `switchContext`), `d` shows context
detail. Palette entry preserved.

**P4-5 — Aliases view.** (S, deps P4-8) `:alias` → table of every command,
alias, source (builtin/user/discovery-shortname).

**P4-6 — Screendump.** (S) `Ctrl+S` on table → write current visible rows as
CSV to `~/.local/state/astrotui/screendumps/<gvr>-<ts>.csv`, Flash path.

**P4-7 — Custom columns (`views.yaml`).** (M, deps P0-2)
`~/.config/astrotui/views.yaml`:

```yaml
views:
  v1/pods:
    columns: [NAME, STATUS, IP, NODE, AGE]
  apps/v1/deployments:
    columns: [NAME, READY, "CUSTOM:.metadata.annotations.team"]
```

`CUSTOM:<jsonpath>` reuses the CRD jsonPath evaluator. Loaded at startup +
live-reloaded (reuse theme watcher pattern). Overrides builtin column list;
unknown names → Flash(Warn) once.

**P4-8 — Native `hotkeys.yaml` + `aliases.yaml`.** (M, deps P0-4)
Same schema as k9s (drop-in): hotkeys map keys → command strings executed
through the command registry; aliases add `:name` → command/GVR. Load from
astrotui dir first, k9s dir as fallback (same precedence as plugins today).
Conflict with builtin key → user wins, Flash(Warn) once at startup, overlay
shows override.

**P4-9 — Config surface + CLI flags.** (S)
Config adds: `metrics.interval`, `ui.refreshDebounceMs`, `ui.wideDefault`,
`logs.tailLines` (default 200), `logs.sinceSeconds` (default 300),
`readOnly` global bool (maps every context to read-only policy unless
overridden), `namespaces.favorites`, `nodeShell.enabled/image`. Flags:
`--readonly`, `--command "<initial :cmd>"` (runs after first render). Strict
schema tests for each new key.

### Phase 5 — Verb completion

**P5-1 — Delete dialog v2 + kill.** (M, deps P0-4)
Dialog fields: cascade policy (Background default / Foreground / Orphan),
grace seconds (default = resource default, `0` allowed), propagated to
`DeleteOptions{PropagationPolicy, GracePeriodSeconds}` in
`actions.DeleteResource` (extend signature; keep old behavior for callers
passing zero-value opts). `Ctrl+K` = preset grace=0 + `Force` semantics
(grace 0 is sufficient; no force field in API — document). Guard: kill is
`safety.Delete` verb. Bulk-aware via P3-5.

**P5-2 — Scale/restart coverage.** (S)
`scalableKind`: +ReplicaSet (plain scale; note it fights its owner — Flash
(Warn) if ownerReferences non-empty). `restartableKind`: +DaemonSet
(`RestartDaemonSet` mirrors existing patch fn). Scale prompt pre-fills
current `spec.replicas` and shows ready count. Executor switch extended +
tests with kubefake asserting patches.

**P5-3 — Rollout undo + history.** (M)
`internal/actions/rollout.go`: list ControllerRevisions (sts/ds) /
ReplicaSets (deploy) for selection; undo = k8s-standard rollback: deploy →
patch template from previous RS; sts/ds → patch from previous
ControllerRevision data. `Ctrl+L` opens revision picker (REVISION, AGE,
CHANGE-CAUSE annotation), Enter confirms through guard (`safety.Restart`
verb class). Describe (workloads) appends `ROLLOUT HISTORY` section (V8).
Tests: kubefake with fabricated RS/CR chains, assert resulting patch.

**P5-4 — CronJob trigger + suspend.** (S)
`t` → create Job from `spec.jobTemplate` named `<cj>-manual-<unix>` with
`ownerReferences` **not** set (k9s parity: manual jobs survive CJ deletion;
add label `astrotui.io/triggered-by=<cj>`); `z` → patch
`spec.suspend=!current`. Guard verbs: trigger=Restart-class,
suspend=Edit-class. Tests via kubefake actions.

**P5-5 — Cordon / uncordon / drain.** (L)
`internal/actions/node.go`:
- cordon/uncordon: patch `spec.unschedulable`.
- drain: dialog (grace seconds, timeout, ignore-daemonsets bool default
  true, delete-emptydir-data bool default false) → implement eviction loop
  ourselves (no kubectl dep): list pods on node via field selector, skip
  mirror pods + DS pods (when ignore), create
  `policy/v1` Evictions, poll until gone or timeout, stream progress to a
  pushed progress view (Nav page with live log lines + cancel), Flash
  summary. Guard: drain requires typed confirm **always** (even non-prod) —
  it's the most destructive verb we have; policy `read-only` blocks.
Tests: kubefake eviction reactor counting evictions, DS/mirror skip logic,
timeout path, PDB-rejection retry path (429 → retry with backoff up to
timeout).

**P5-6 — Node shell (opt-in).** (M, deps P4-9)
Config-gated (`nodeShell.enabled=false` default). Creates privileged pod
(hostPID/hostNetwork, `nsenter -t 1 -m -u -i -n` image from config, default
`busybox:1.36`), attaches via existing exec runner, deletes pod on exit
(defer + best-effort). Guard: Exec verb + typed confirm always. e2e-kind
test behind env flag.

**P5-7 — Attach.** (S) `actions.RunAttach` mirrors `RunExec` with
`Attach` SPDY executor (`pods/attach` subresource, stdin/tty from container
spec `tty` flag). Key `a` on pod/container views.

**P5-8 — Secret decode + clipboard.** (S)
Secrets view `s` → describe variant with `data` values base64-decoded
(binary-safe: non-UTF8 → `<binary: N bytes>`); `y` remains encoded YAML.
`c` on the decoded view copies selected key's value via OSC52 escape
(works over SSH; document terminal support). **Never** log or flash decoded
values. Redact decoded view from screendumps.

**P5-9 — Port-forward v2.** (M, deps P0-3)
Dialog lists all declared container ports (pod) / service ports with names;
fields: local port (default = remote), address (default `localhost`,
allow `0.0.0.0` with Flash(Warn)); multiple forwards per target allowed.
`RunPortForward` errors → captured into manager entry state
(`Status: active|failed(err)|closed`), STATUS column in `:pf` view, failed
rows red (P3-3), auto-remove closed after 60s. Stretch (separate commit):
persist active forward specs in session; on startup prompt "restore 3
port-forwards?".

### Phase 6 — Logs completion

**P6-1 — Stream options.** (M)
Logs page state: `since` (config default) + keys `1/2/3/4/0`, `tailLines`
(config default 200), `previous` toggle `p`, `timestamps` toggle `t`
(request server timestamps via `PodLogOptions.Timestamps` instead of local
receive time — fixes ordering for merged streams; parse RFC3339 prefix into
`Entry.Time`, keep receive-time fallback). Changing any option cancels and
restarts all streams (existing cancel plumbing) preserving buffer (flag:
clear on since-change, keep on timestamps toggle).

**P6-2 — View behaviors.** (S)
Autoscroll on by default; `Ctrl+S` toggles (indicator in title
`[follow|paused]`); any manual scroll pauses; `F` fullscreen (hide
header/crumbs via Nav flag); `C` clears buffer.

**P6-3 — Structured filtering + pretty JSON.** (M)
`/` prompt in logs accepts `key=val` / `key~=regex` (route through existing
`logs.Filter` over `ParseLine` results — connect the two, they're currently
disjoint) alongside raw regex; `j` toggles pretty mode: parsed JSON/logfmt
lines rendered as `level=info msg="..." extra…` with level-colored severity
(error/warn/info) — severity detection from `level`/`severity`/`lvl` fields.
AC: parser-to-view integration tests with mixed json/logfmt/text buffers.

### Phase 7 — Cluster health & big views

**P7-1 — DECISION + Helm read-only views.** (L — or WON'T-DO)
Decision to make at phase start: adding Helm = +~8MB binary (helm SDK) vs
NFR binary ≤30MB (current ~25MB stripped — measure first; if over budget,
implement via `helm` CLI shell-out like a builtin plugin: `:helm` requires
helm in PATH, table = `helm list -A -o json` parsed, drill: history, values
(`d`), rollback through guard). **Recommendation: CLI shell-out** — zero
binary cost, ships parity where users already have helm. Absent helm binary
→ view shows install hint. Tests: fixture JSON parsing + command-plan tests
(no live helm in CI).

**P7-2 — DECISION + benchmark.** (S — or WON'T-DO)
k9s embeds `hey`-style HTTP benchmarking. **Recommendation: WON'T-DO,
document** — niche, plugin-able (`plugins.yaml` example with `hey` provided
in docs instead: P9-3). Revisit on user demand. (If overruled: shell-out
design mirroring P7-1.)

**P7-3 — Pulses.** (M, deps P0-2, P1-1)
`:pulses` Nav page: grid of tiles (Deployments ok/total, Pods
running/total, restarts in last 5m (delta-tracked from informer updates),
Warning events last 5m, node ready count, CPU/MEM cluster %). Each tile =
sparkline (last 60 samples, 5s cadence, unicode block chars — no new deps)
+ count; Enter on tile jumps to filtered view (e.g. events tile → events
view filtered Warning). Data: existing informers + metrics poller; a
`pulses.Collector` aggregates in App. Tests: collector unit tests with
scripted informer events.

**P7-4 — XRay.** (L, deps P0-1)
`:xray deploy [ns]` → `tview.TreeView` page: Deployment → ReplicaSets →
Pods → Containers, each node suffixed with status glyph (ok/warn/error from
P3-3 semantics) and counts rolled up. Lazy-expand levels from informer
cache (Ensure rs/pods). Keys: Enter expand/collapse, `d` describe, `l` logs
on pod/container nodes, `/` filters tree. Also `:xray svc`, `:xray sts`,
`:xray rs`. Implementation: `internal/ui/xray.go` builder walking
ownerReferences index (build `ownerIndex map[types.UID][]*unstructured` per
refresh from informer store — O(n) each event batch, capped by debounce).

**P7-5 — Popeye.** (M — behind decision similar to helm)
Shell-out to `popeye` if present (`:popeye` → run with `--out json`,
parse into table: RESOURCE, GRADE, ISSUES, drill to issue list). No binary
embed. Absent → hint. (k9s embeds popeye; shell-out is our 90% answer,
document divergence.)

**P7-6 — RBAC views.** (M)
`:rbac u:<user>|g:<group>|s:<ns/sa>` → resolve bindings: list
ClusterRoleBindings + RoleBindings from cache, match subject, aggregate
rules from referenced (Cluster)Roles into table VERB × RESOURCE × SCOPE.
`:can <verb> <resource>` → SelfSubjectAccessReview for current user, Flash
result. Tests with kubefake RBAC fixtures.

### Phase 8 — Differentiator hardening (beyond-k9s)

**P8-1 — Palette v2.** (M, deps P0-4)
(a) Index live object names across **all synced informers** (not just
current view) — entries `pod default/api-7f9c` jump = switch view + select
row; cap 5k entries, rebuilt lazily on palette open from informer stores
(no background cost). (b) Recent-commands category with frecency (count ×
recency decay, persisted in session dir). (c) Palette preview pane: right
side shows selected entry detail (object status snippet / action help).

**P8-2 — AI v2.** (M)
(a) `:ai` chat page (Nav) with context injection: current view GVR,
selection YAML (truncated 8k), last 20 flash errors; streaming into
TextView (existing `streamAIResponse`). (b) NL action arguments: extend
`NaturalLanguageAction` prompt to return
`{"action":"scale","target":"deploy/api","args":{"replicas":"5"}}`; map
through the command registry with **guard flow unchanged** (AI can never
skip typed confirmation — assert in test). (c) `Ctrl+E` on a CrashLoop pod
auto-includes previous-container log tail (P6-1 plumbing) in
`FailureRequest`.

**P8-3 — Prod-guard polish.** (S)
Read-only mode surfaces disabled verbs in `?` overlay as struck-through;
`--readonly` flag (P4-9); guard denial flashes include which policy/source
matched (config context entry vs prod regex).

### Phase 9 — Hardening, docs, release

**P9-1 — Perf re-validation.** (M) Re-run `make perf`, `make nfr-live`,
1k-pod kind budgets with metrics poller on and nav stack active; add
`BenchmarkBuildRowsWithContext`, `BenchmarkMetricsSnapshotJoin` (target:
1k-pod join <5ms). Fix regressions before proceeding.

**P9-2 — Race/leak audit.** (M) `go test -race ./...` extended with: rapid
context-switch churn test (old poller/informer goroutines must stop —
assert via `runtime.NumGoroutine` plateau in test), Nav push/pop stress with
streaming logs, port-forward manager concurrent add/kill.

**P9-3 — Docs.** (S) Update README feature table; new
`docs/k9s-migration.md` (every k9s key/command → astrotui equivalent, all
intentional divergences incl. P7-2 WON'T-DO rationale + `hey` plugin
recipe); `docs/metrics.md`, `docs/navigation.md`; refresh `?` screenshot +
main SVG screenshot.

**P9-4 — Release.** (S) Version bump, changelog from task IDs, goreleaser
snapshot verify, tag rc, cosign verify, GHCR publish, then update
`docs/specs/completion-audit.md` with a *verified* (not aspirational)
matrix referencing this plan's IDs.

---

## 7. Sequencing, parallelism, and milestones

Dependency-honoring execution order (single implementer; parallel tracks
noted for multi-agent execution):

```
M0  P0-1 → P0-2 → P0-3 → P0-4                       [gate: zero-regression]
M1  P1-1 → P1-2 → P1-3 → P1-4 → P1-5                 [gate: metrics on kind e2e]
M2  P2-1..P2-6  (P2-3 is the long pole)              [gate: drill-down e2e]
M3  P3-1..P3-8  (3-1/3-2 first; 3-4→3-5 ordered)     [gate: filter+bulk e2e]
M4  P4-1..P4-9  (4-2 after 4-8 lands aliases)        
M5  P5-1..P5-9  (independent of M4; can run parallel track)
M6  P6-1..P6-3  (independent; parallel track)
M7  P7-1..P7-6  (after M1+M2; pulses needs metrics, xray needs nav)
M8  P8-1..P8-3  (after M4 registry)
M9  P9-1..P9-4  (last, always)
```

Parallel-track guidance for multi-agent implementation: after M0+M1, tracks
{M2,M3}, {M5}, {M6} touch mostly-disjoint files and can proceed
concurrently; M4/M7/M8 serialize behind their deps. Merge order on
conflict-prone files (`app.go`, `keymap.go`): M2 → M3 → M4.

Milestone gates are hard: each ends with full `make` + envtest + kind e2e
green and the Feature Inventory table in this file updated in the same PR.

Rough effort: M0 ≈ 5d · M1 ≈ 3d · M2 ≈ 4d · M3 ≈ 4d · M4 ≈ 3d · M5 ≈ 5d ·
M6 ≈ 2d · M7 ≈ 5d · M8 ≈ 2d · M9 ≈ 2d ⇒ ~35 focused dev-days single-track,
~20 calendar-equivalent with parallel tracks.

## 8. Testing strategy (applies to every task)

1. **Unit**: every new pure function table-driven; every action against
   kubefake/dynamicfake asserting the exact API verbs+patches issued.
2. **UI**: `tcell.SimulationScreen` interaction tests (existing pattern in
   `app_test.go`) for every new key/flow — minimum one per task that adds a
   key.
3. **envtest** (`test/envtest`): CRD printer columns (exists), add:
   metrics API absent behavior, RBAC-denied informer → flash path,
   eviction/drain against real API machinery.
4. **kind e2e** (`test/kind`): extend the golden path: metrics columns
   populate (kind + metrics-server addon in CI e2e job), drill-down chain,
   drain a tainted throwaway node, port-forward v2 status, bulk delete.
5. **Race**: all of the above under `-race` in CI (existing job).
6. **Golden snapshots**: renderer output with `ASTROTUI_UPDATE_GOLDEN=1`
   regen flow; any column change must show up as a reviewed golden diff.
7. **NFR harness**: `make perf` / `make nfr-live` at every milestone gate
   (§9.1 of spec budgets unchanged).

## 9. Risks & mitigations

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Nav refactor (P0-1) destabilizes every view | High | Do it first, alone, zero-feature PR; SimulationScreen regression suite before starting; convert call sites mechanically one file per commit |
| Metrics polling on huge clusters (10k pods) | Med | Single LIST per interval (not per-ns), interval floor 5s, parse-only-needed fields, benchmark P9-1; auto-degrade: if poll >2s, double interval and Flash(Warn) once |
| `resource.Quantity` parsing cost in hot render path | Med | Parse once per poll into int64 milli/bytes in the poller, renderers only format |
| Drain evictions vs PDBs hang | Med | Hard timeout + progress view + cancel; 429 backoff loop tested |
| Binary size with helm SDK | Med | Shell-out decision (P7-1) keeps 0 cost |
| Key conflicts vs k9s muscle memory | Low | §4 is the contract; migration doc P9-3; user `hotkeys.yaml` overrides (P4-8) |
| OSC52 clipboard not supported in some terminals | Low | Feature-detect env (`TERM`, tmux passthrough), Flash hint when likely unsupported |
| Golden churn noise in reviews | Low | One golden-regen commit per PR, separate from logic commits |
| Session schema evolution | Low | `Version` field + tolerant decode (P2-6) |

## 10. Task index (flat, for tracking)

P0-1 nav stack · P0-2 RowContext · P0-3 flash bus · P0-4 keymap/commands ·
P1-1 metrics poller · P1-2 wire poller · P1-3 metrics columns · P1-4 gauges ·
P1-5 quantity sort + semantic sort keys · P2-1 esc-back · P2-2 crumbs ·
P2-3 drill-down · P2-4 selection preservation · P2-5 header · P2-6 session v2 ·
P3-1 filter modes · P3-2 label filtering · P3-3 row colors · P3-4 marks ·
P3-5 bulk actions · P3-6 wide mode · P3-7 refresh · P3-8 connectivity ·
P4-1 help overlay · P4-2 autocomplete/history · P4-3 ns favorites ·
P4-4 contexts view · P4-5 aliases view · P4-6 screendump · P4-7 views.yaml ·
P4-8 hotkeys/aliases native · P4-9 config+flags · P5-1 delete v2/kill ·
P5-2 scale/restart coverage · P5-3 rollout undo/history · P5-4 cronjob ·
P5-5 cordon/drain · P5-6 node shell · P5-7 attach · P5-8 secret decode ·
P5-9 port-forward v2 · P6-1 log stream options · P6-2 log view behaviors ·
P6-3 structured log filter/pretty · P7-1 helm (decision) ·
P7-2 benchmark (decision) · P7-3 pulses · P7-4 xray · P7-5 popeye ·
P7-6 rbac · P8-1 palette v2 · P8-2 AI v2 · P8-3 guard polish ·
P9-1 perf · P9-2 race/leak · P9-3 docs · P9-4 release.

**54 tasks. When the Feature Inventory in §3 shows no ❌ without a WON'T-DO
rationale, astrotui is k9s-capable or beyond — with palette, AI, logs
deluxe, prod guard, and session/QoL as clear overtakes.**
