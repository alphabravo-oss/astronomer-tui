# k9s capability review

Date: 2026-07-06.
Scope: code-level audit of `astrotui` against the k9s feature set, answering
"is this as capable or more capable than k9s as a daily driver?"

**Verdict (after the implementation pass): yes for daily-driver use.** The
parity-plus plan ([`docs/plans/k9s-parity-plus-plan.md`](../plans/k9s-parity-plus-plan.md))
has been executed through the foundational refactors and the high-value
feature phases. astrotui now matches k9s on the core daily loop — browse,
filter, drill down, describe, logs, exec/attach, edit, scale, restart,
rollout verbs, port-forward, cordon/drain, bulk delete — with metrics
visible, in any namespace scope, on any context, plus differentiators k9s
lacks (command palette, AI, logs-deluxe, typed prod guard, session restore).
See "Implemented since the audit" for what landed; a few large auxiliary
views (pulses, xray, popeye, helm, RBAC) remain as tracked roadmap.

## Implemented since the audit (parity-plus plan)

Foundational refactors: navigation stack with Esc-back + breadcrumbs +
header/flash chrome (P0-1); RowContext-aware renderers + styled cells (P0-2);
flash/error bus surfacing watch/RBAC/port-forward failures (P0-3); keymap +
command registry driving the help overlay (P0-4).

Features:
- **Metrics** — metrics.k8s.io poller; pod CPU/MEM/%req and node
  CPU/%/MEM/%/PODS columns with `n/a` degradation; cluster header gauges;
  unit-aware sort + semantic sort keys (P1).
- **Drill-down** — Enter descends (workload→pods by selector, node→pods,
  pod→containers, service→pods, cronjob→jobs, namespace→switch); Esc walks
  back; selection pinned across refresh (P2).
- **Table power** — regex/inverse/fuzzy/label filter modes with match counts;
  row state coloring; multi-select marks + guard-aware bulk delete (P3).
- **Verbs** — scale (+ReplicaSet), restart (+DaemonSet), cronjob
  trigger/suspend, kill (grace 0), attach, secret decode, node
  cordon/uncordon/drain with eviction loop (P5).
- **Logs** — previous/timestamps toggles, since presets, tail, autoscroll,
  clear, pretty JSON/logfmt with severity coloring; structured filtering (P6).
- **Navigation/commands** — namespace favorites (1–9), first-class contexts
  view, `:q`/`:ctx`/`:flash`, discovery-based resource resolution (P4 subset).

Everything above ships with unit + fake-client tests; the suite is green
under `-race`.

## Original audit (pre-implementation)

**Verdict at audit time: not yet.** The foundation (informer cache, palette,
safety guard, logs, plugins, theming, AI hooks) was real and tested, but
several core day-to-day k9s behaviors were missing or broken. The first pass
closed the tractable gaps (see "Fixed in this pass"); the parity-plus plan
then closed the rest.

## Method

Every claim below was verified against the source, not the completion audit.
The audit (`docs/specs/completion-audit.md`) marks "k9s built-in renderers"
and "Dynamic CRD view with printer columns" as Done; both were only partially
true at the code level (empty columns; `RendererForCRD` never called by the
app).

## Fixed in this pass

| Gap | Before | After |
| --- | --- | --- |
| Pod columns mostly empty | `IP`, `SERVICE-ACCOUNT`, `QOS`, `LAST RESTART`, `NOMINATED NODE`, `READINESS GATES` had no extractors; `CPU`/`MEM`/`GPU`/`VS`/`PF`/`VALID` could never populate | Real extractors for all data-backed columns; metrics columns removed until a metrics source exists (`internal/ui/views/renderers.go`) |
| Pod STATUS wrong | Pods matched the node `Ready` condition branch, so a `CrashLoopBackOff` pod showed `Ready`/`Running` | kubectl-style derivation: `CrashLoopBackOff`, `ImagePullBackOff`, `Init:<reason>`, `Terminating`, `Completed` (`podStatus`) |
| Node columns empty | `ARCH`, `TAINTS`, `OS-IMAGE`, `KERNEL`, `INTERNAL-IP`, `EXTERNAL-IP` unimplemented | Extracted from `status.nodeInfo` / `status.addresses` / `spec.taints` |
| `CONTAINERS`/`IMAGES` empty | Declared for sts/rs/jobs/cronjobs but no extractor | Extracted from pod templates (incl. `jobTemplate`) |
| Lexicographic sort | `"10d" < "2m"`, `"10" < "2"` | Typed comparator: numbers, ages/durations, ready fractions; empty sorts last (`views.CompareCellValues`) |
| `d` deletes | Directly opposite k9s muscle memory (`d` = describe) — a safety hazard | `d`/Enter = describe (YAML + live EVENTS section), `y` = YAML, `Ctrl+D` = delete |
| No describe | Only raw YAML detail | Describe view appends the object's events (type/reason/age/message) |
| No `:q`, `:ctx` | Only `:ns`, `:pf`, and resource names | `:q`/`:quit`, `:ctx <name>` (bare `:ctx` opens the palette), `:all` |
| No all-namespaces view | `:ns` required a concrete namespace | `:ns all`, `:all`, or the `0` hotkey; title shows `[ctx/all]` |
| Static resource table only | Unknown plurals → "Unknown command"; CRDs unreachable except by full `group/version/resource` | Discovery-based resolution of plurals, singulars, shortnames, kinds, and `plural.group` (`cluster.ResolveResource`) |
| CRD printer columns unused | `views.RendererForCRD` existed but was dead code in the app | `App.rendererFor` fetches the CRD and renders `additionalPrinterColumns`; cluster-scoped CRDs list across namespaces |
| Nondeterministic row order on sort ties | Informer indexer map order leaked into the table | Rows pre-sorted by namespace/name before the column sort |

## Parity matrix (verified, post-fix)

### At or above k9s

- **Command palette** (Ctrl+P) across resources, actions, contexts, namespaces, plugins — k9s has no equivalent.
- **Merged multi-pod workload logs** with per-pod coloring, regex search with n/N navigation, save-to-file, and jump-to-events/owner from a log line.
- **AI assist** (opt-in): failure explain, log summarize/chat, manifest lint, NL-intent palette (`>`/`?`) — no k9s equivalent.
- **Prod-context typed-confirmation guard** with per-context policies (read-only / confirm-destructive / full) — stronger than k9s's `readOnly` flag.
- **k9s plugins.yaml drop-in compatibility** plus `config import-k9s` migration.
- **Session restore** (context, namespace, view, filter) — k9s persists less.
- **Crash-recovery**: panics in handlers are caught and logged instead of tearing the TUI down.

### At parity

- Resource browsing over live informer watches; ~30 builtin views + any CRD/aggregated API via discovery + printer columns.
- Filter (`/`), column sort with indicator, describe/YAML/edit/delete/scale/restart/exec/port-forward/logs on the usual kinds.
- Port-forward manager view (`:pf`) with kill; skins with live reload; `?` keybind overlay generated from the actual action catalog.

### Still behind k9s (ranked)

1. **No metrics** — k9s's CPU/MEM columns and header gauges need a
   metrics.k8s.io poller (informers can't watch metrics). The columns were
   removed rather than left permanently empty; re-add when the poller lands.
2. **No drill-down navigation stack** — k9s Enter descends
   (deployment→pods→containers) and Esc walks back with breadcrumbs. astrotui
   has one level (log-line→events/owner jumps).
3. **Filter is substring-only** — no regex, no fuzzy, no inverse (`!`), no
   label-selector (`-l app=x`) filtering.
4. **No multi-select** — k9s space-marks rows for bulk delete.
5. **No row coloring by state** (errors red, terminating purple, etc.).
6. **Missing verbs**: cordon/drain (nodes), kill (grace 0), attach, rollout
   undo, trigger job from cronjob, suspend/resume cronjob, base64 secret
   decode, node shell. Scale/restart cover Deployment/StatefulSet only
   (k9s also does RS/DS/rollouts).
7. **Log view options**: no since/tail selection, no previous-container
   toggle (`p`), no autoscroll toggle, no timestamp toggle (plumbing exists
   in `logs.RequestOptions`, not exposed in the UI).
8. **No dedicated views**: pulses, xray, popeye, helm/charts, RBAC
   policy/can-i, contexts view (contexts are palette-only), aliases view.
9. **Watch errors are silent** — an RBAC-denied informer shows an empty table
   with no error toast.
10. **No custom columns / wide mode / hotkeys.yaml runtime support** (hotkeys
    are only migrated by `config import-k9s`, not read natively).
11. **Port-forward failures are invisible** — `RunPortForward` errors are
    discarded and the manager still lists the forward as active.
12. **No namespace favorites / numbered hotkeys 1–9** (only `0` = all).

## Recommended next tranche

1. Metrics poller (metrics.k8s.io via dynamic client, 15s cadence) feeding
   pod/node CPU/MEM columns and a header gauge — restores the columns removed
   here and is the single most-missed k9s feature.
2. Navigation stack with Esc-back + Enter drill-down (workload→pods via
   selector, pod→containers), replacing the flat describe-on-Enter.
3. Filter upgrades: regex + `!` inverse + `-l` label selectors (server-side
   via the informer's label selector support, which `ResourceCache.List`
   already accepts).
4. Error surfacing: informer sync failures and port-forward errors to a toast
   plus the port-forward manager status column.
5. Row coloring by status + multi-select with bulk actions.

Items 1–3 are what stand between "usable" and "daily-driver replacement";
the spec's headline features (palette, logs deluxe, AI, safety guard) are
already ahead of k9s.
