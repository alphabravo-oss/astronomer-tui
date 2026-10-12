# Astro-TUI / k9s v0.50.18 core parity acceptance matrix

Baseline: upstream tag `v0.50.18`, commit `6dbf571c59fd48dc5b384aa46ee7f3e5decfae2b`. Source inventory: `internal/view/registrar.go`, the view implementations, command interpreter and configuration schemas. Plugin expansion is explicitly excluded at the user's request. Existing plugin behavior is not removed. UI/key naming may differ; equivalent workflows must be reachable and documented. Destructive actions keep Astro-TUI confirmation and identity protections.

This is a work ledger, not a claim that unchecked rows work. A passing compile or generic resource viewer does not establish specialized workflow parity. Prior tests and benchmarks are in [the implementation report](implementation-follow-through.md).

| ID | Workflow | Initial state | Acceptance |
|---|---|---|---|
| C01 | Core/CRD discovery, namespace/context changes, watch updates | Implemented | Restricted namespace RBAC, CRD install/remove, empty/reconnecting caches, no stale context objects |
| C02 | Resource detail/YAML, describe, sorting, filtering, aliases | Partial | Pinned upstream Go regexp semantics (source corrects website regex2 wording); inline resource filter/context; alias list |
| C03 | Pulses health dashboard | Missing | Scoped totals/health, metrics unavailable state, bounded history, periodic refresh, drill-down, cancellation |
| C04 | XRay relationship tree | Missing | Workload/service/pod graphs, UID ownership, selectors, container leaves, navigation, missing/forbidden dependencies |
| C05 | HTTP endpoint benchmarks and reports | Missing | Explicit bounded run, configured requests/concurrency/method/path/body/headers, errors/status counts/percentiles, cancellation, local forwards, saved reports |
| C06 | User/group/service-account RBAC exploration | Partial | Binding and rule provenance, namespace/cluster scope, named resources/subresources/wildcards, reverse lookup, partial access warning |
| C07 | Custom columns and scoped configuration | Partial | Context overlays, exact/regex namespace views, alias views, S/W/H/L/R/N/T attributes, stable precedence, reload fallback |
| C08 | Saved resource views | Missing | Private atomic files, list/open/delete only app-owned captures, explicit Secret export handling |
| C09 | Secrets, ConfigMaps, containers and references | Partial | Decode selected Secret explicitly, container identity/status/env/images, related references and UID-safe navigation |
| C10 | Logs | Implemented core | Previous/init/ephemeral/multi-source, search/filter/follow/wrap/time, bounded reconnect and export |
| C11 | Exec, node shell, forwarding | Partial | Container picker, terminal resize, node identity/policy/cleanup; ready/error/collision/stop forward lifecycle |
| C12 | Workload mutations | Implemented core | Scale, restart, trigger, suspend, delete including force with explicit confirmation; conflict/replacement/read-only cases |
| C13 | Helm releases/history/values/manifest/rollback/uninstall | Missing | Explicit context/namespace, failure visibility, mutation confirmation, history and identity checks |
| C14 | Metrics/resource health, storage and events | Implemented core | Quantities, init/sidecar requests, stale/unavailable metrics, exact-UID events, PVC diagnostics |
| C15 | CLI/session/read-only controls | Partial | Explicit launch resource/context/ns/read-only and config paths, state precedence, CLI help |
| C16 | Terminal usability | Partly qualified | Black/light/dim, 40/80/120/200 widths, Unicode, resize, no-color, tmux; native OS qualification separate |
| C17 | Release/auth/long-running reliability | Partly qualified | Race/API/live regression, exec credential refresh fixture, reconnect and context churn, four-target builds; actual cloud and release CI qualification separate |
| C18 | Image vulnerability reports | Inventory pending | Inspect upstream integration, identify required operator/report API, show findings/errors without executing external scanners implicitly |
| EX1 | Plugins and plugin compatibility | Excluded | No plugin expansion in this pass |

## Evidence rules

- Record source and test names for implemented rows; distinguish unit/fake, real API-server, live process and human visual evidence.
- Do not infer authorization from RBAC rule inspection: webhooks/other authorizers and unknown group membership may change effective permission.
- No benchmark load runs automatically. The user must see the endpoint and request budget and start it explicitly.
- Native macOS/ARM terminals, actual cloud accounts, release signing/publication and human operator trials require their real environments. Cross-compiles and fixtures cannot replace them.

## Primary references

- [Pinned k9s source](https://github.com/derailed/k9s/tree/v0.50.18/internal/view)
- [Commands](https://k9scli.io/topics/commands/), [features](https://k9scli.io/)
- [HTTP benchmarks](https://k9scli.io/topics/bench/), [RBAC](https://k9scli.io/topics/rbac/)
- [Custom views](https://k9scli.io/topics/columns/), [configuration](https://k9scli.io/topics/config/)

Source correction: `internal/model1/table_data.go:166` in k9s v0.50.18 imports Go `regexp`. The earlier claim that replacing our engine was necessary for regex2 parity was based on website wording and is withdrawn. Both engines reject lookarounds/backreferences; this is not a missing Astro-TUI feature against the pinned baseline.

## Implementation result (2026-10-12)

The missing core workflows in this inventory are now implemented. This establishes a usable core feature set against the pinned baseline, with the explicit differences below. It does **not** establish universal feature equivalence, every k9s configuration/keybinding, production reliability in every cluster, or superiority in operator trials. Plugins remain excluded.

The initial-state column above is historical. Current acceptance and evidence:

| ID | Result and implementation | Evidence / practical boundary |
|---|---|---|
| C01 | Retained discovered resources, scoped caches and partial discovery recovery | Real API-server tests `TestNamespaceRestrictedInformerAgainstAPIServer`, `TestDiscoveryInvalidationAndServerPrinterColumns`; live TUI watch/navigation suite |
| C02 | Describe includes snapshot metadata, owners, YAML and exact-UID events; alias browser; inline resource namespace/context/filter commands | `resource_extras.go`, `custom.go`, existing selection/filter tests. Resource palette clears stale alias-specific columns. Describe is a structured local view, not byte-identical kubectl output |
| C03 | `:pulses` / `:pu`: eight resource families, readiness/generation-aware health, metrics stale/unavailable, 40 samples, 5s refresh and drill-down | `health.go`, `explorer.go`; live explorer test and real tmux navigation. Partial API failures show `?`, not empty success. A scoped health table/history is the functional equivalent; it does not reproduce k9s' chart layout |
| C04 | `:xray`: owner UID chains, service selectors/EndpointSlices, nodes, Pods/PVCs and container leaves | `TestGraphUsesOwnerUIDAndNamespace`, live Deployment→ReplicaSet→Pod→container traversal. Manual refresh, 10k displayed nodes, depth 12. Supported roots are workloads, services, Pods and nodes; arbitrary CRD relationship semantics are not inferred |
| C05 | `:bench`, ready-forward `b`, `:be`: bounded HTTP load with two-stage review, config, results and saved reports | Local HTTP tests for exact budget/status/cancellation/no redirects/private reports; live keyboard flow confirms zero requests before confirmation and exactly 200 afterward. Limits and per-context config semantics in [configuration](../config.md#http-benchmarks) |
| C06 | `:rbac`, users/groups/service accounts, `:who-can`: scope, resource names, subresources, wildcards, bindings and partial access | `TestRBACScopeIdentityAndReverseLookup`, `TestRBACPartialVisibility`, live namespace RoleBinding/reverse lookup. These are visible RBAC grants, not a complete authorization engine |
| C07 | Context aliases/hotkeys/views, namespace regex/exact views, alias views, S/W/H/L/R/N/T and reload fallback | `TestScopedCustomPrecedenceAndPatterns`, `TestContextPreferencesIsolationAndReadOnly`; tests reject cycles and invalid policy. Core k9s context preferences imported; arbitrary global fields, named component skins and exotic import directory names have documented limits |
| C08 | `:save` / Ctrl-S, `:sd`: private atomic manifest captures, open/delete and explicit Secret export review | `archives.go`, archive/palette test, private-file/report tests. Captures are manifests; raster screenshot export is a test utility |
| C09 | Secret decode, containers (regular/init/ephemeral), env/status, references and merged workloads | `resource_extras.go`, `references.go`, `TestPodReferenceSources`; existing related views use UID snapshots. Secret decoding stays local |
| C10 | Retained logs: multiple sources, previous, time windows, server timestamps, reconnect, wrapping, search/filter/follow, export | Busy-log/timestamp/wrapped-source tests plus live suite; `TestStreamRejectsReplacedPod` now checks selected UID before opening a stream. Timestamps are displayed; controls/layout need not match k9s |
| C11 | Container exec with raw terminal/resize; Linux node shell with helper identity/cleanup; existing ready/error forward lifecycle | `TestLiveNodeShellCleanup` actually executes nsenter in a disposable kind node and verifies deletion; cancellation/replacement/exited-helper tests, container-selection and resize cancellation tests. Node shell requires privilege and is not supported on Windows nodes |
| C12 | Mark up to 100 objects; bulk and grace-zero deletion added to existing administrative mutations | `TestForceDeleteRetainsUIDAndZeroGrace`, `TestMarkedSelectionPersistsSortByUID`, `TestReadOnlyBlocksNewMutationEntrypoints`, live administration and previous conflict/drain tests. Bulk deletion is not atomic; canceled/failed runs can partially succeed |
| C13 | Native Helm release/history/values/manifest/rollback/uninstall | `TestLiveHelmLifecycle`: real Helm 3.21.0 install→upgrade→stale revision rejection→rollback→manifest→uninstall of a disposable ConfigMap chart. UI captures context/kubeconfig, checks revision/storage UID before confirmed mutations. Helm installation and Secret/ConfigMap storage required |
| C14 | Retained metrics, resource quantities, PVC diagnostics, exact-UID events; shared health for Pulses/workloads | Existing metric/quantity/events tests, prior live metrics-server qualification. This pass verified missing-metrics behavior without installing metrics-server |
| C15 | `--readonly`, `--command`/`-c`, `-n`, scoped preferences and config paths | CLI builds/help, context-overlay tests, read-only entrypoint tests and actual read-only tmux launch. CLI read-only remains set during reloads |
| C16 | Black branding retained; new views use theme colors; no-color and terminal matrix retained | Actual black tcell frames exported below, visual review, real Linux tmux 120×36→80×24 resize and navigation; automated dark/light/dim/Unicode/no-color matrix. Actual macOS/ARM and comprehensive assistive-technology trials remain external |
| C17 | Credential refresh fixture, cancellation hardening, race/API/live suite, four-target release snapshot and CI gates | Actual HTTPS ExecCredential expiry→refresh test; API-server 1.36 envtest; kind 1.32.2 live suite; build checks. Actual cloud auth and published signing chain remain external qualification |
| C18 | Explicit native Grype image selection/confirmation/report | `TestImageScanRequiresConfirmationAndRendersReport` checks the CLI contract and table with a controlled fixture; real Grype 0.120.1 scanned public `busybox:1.37.0` successfully (3 matches at this database version). Native executable required; database/network/registry availability remain dependencies |
| EX1 | Deferred by request | No new plugin compatibility work in this pass; earlier plugin implementation is retained |

## Safety and lifecycle decisions

1. New cluster mutations use existing read-only/protected-context policies. Confirmation defaults to Cancel; production policy requires the exact context name. Selected identity is preserved across sorting/marking and passed to Kubernetes mutation preconditions.
2. Exec and logs recheck Pod UID before opening their respective subresources. Kubernetes exec/log APIs do not accept UID preconditions, so a replacement between the check and request remains a small API-level race. Helm likewise has a revision/UID preflight, not an atomic lock around its CLI command.
3. Node-shell preparation verifies the node before and after helper readiness. Failure/cancellation attempts independent UID-preconditioned cleanup. The helper has a one-hour active deadline. A killed application or inaccessible API can leave a helper requiring cleanup; the error identifies it.
4. Auxiliary polling ends when the view closes. Async operations have timeouts/cancellation and suppress late navigation. Terminal resize reads the captured file descriptor to avoid racing the Go file object on closure. Native tools use context cancellation, bounded output and a bounded pipe wait.
5. HTTP benchmarks require an explicit endpoint/budget review and a separate confirmation. Redirects are disabled; normal TLS verification remains. Reports omit request body/headers and redact URL credentials/query/fragment.
6. RBAC analysis never presents absence of visible bindings as denial. Partial access is explicit; unknown group membership and other authorizers are outside the binding model.
7. Secret exports are explicitly confirmed and private; local Secret decoding does not initiate AI or network transmission. Helm values/manifests may also contain credentials and are displayed locally.

## Validation run and reproducibility

Run from the repository root; use a disposable cluster and isolated configuration. No user cluster workloads were changed. The test cluster was `astrotui-parity` with context `kind-astrotui-parity`; it and its fixture workloads were deleted after validation.

```sh
go test -race ./...
make lint
make coverage-nfr
make envtest
KUBECONFIG=/tmp/astrotui-parity.kubeconfig \
  ASTROTUI_E2E=1 ASTROTUI_E2E_CONTEXT=kind-astrotui-parity \
  ASTROTUI_NODE_SHELL_E2E=1 \
  go test -race -tags=e2e ./test/kind/... ./internal/ui/... \
  -skip 'TestHeadlessRenderProcessGoldenPath|TestLiveMetrics' -count=1 -timeout=6m
goreleaser release --snapshot --clean --skip=publish,sign,docker,sbom,validate
```

Results for this pass:

- Full race suite passed. New tests cover HTTP budgets, RBAC scope, partial snapshots, config precedence, helper cleanup/cancellation, force-delete identity, native-tool errors/cancellation, credential refresh and scan confirmation.
- `make lint` passed through its `go vet ./...` fallback; golangci-lint is not installed here. This is not a claim that golangci-lint ran.
- Required coverage gate passed: cluster **78.4%**, actions **72.5%**, logs **74.1%**. The gate now also fails immediately on a failed test process instead of accepting a printed coverage percentage from failing tests.
- Real API-server envtest passed in **37.076s**. Full kind/TUI live run passed (kind **30.054s**, UI **5.792s**); after the cancellation fix, node-shell and explorer live checks passed again (**2.743s / 1.975s**); the final report-formatting live rerun passed in **1.882s**.
- Four GoReleaser snapshot targets passed: Linux/macOS × amd64/arm64. Publishing, signatures, Docker and SBOM generation were explicitly skipped in this local snapshot. No release was published.
- Actual isolated tmux process passed brand, Pulses, resize and XRay checks. A real Grype database/image scan completed; its temporary database was removed afterward. The first race rerun overlapped that multi-gigabyte download and hit temporary-disk exhaustion while linking; it passed after removing that task-owned cache. This was an environment failure, not a suppressed test failure.
- CI now pins and verifies Helm 3.21.0 for the live suite, uses an isolated `astrotui-ci` kind name, and enables the guarded node-shell fixture. The platform workflow still needs to run on GitHub to qualify its actual runners.

Regenerate live screenshots by adding `ASTROTUI_PARITY_SCREENSHOTS=/absolute/path/to/docs/screenshots` to the live explorer test and unsetting `NO_COLOR`. The SVGs contain actual tcell frames from the disposable cluster; they are not hand-drawn mockups or production screenshots:

- [Main branding fixture](../screenshots/astrotui-main.svg)
- [Pulses](../screenshots/astrotui-pulses.svg)
- [XRay](../screenshots/astrotui-xray.svg)
- [RBAC](../screenshots/astrotui-rbac.svg)
- [HTTP benchmark report](../screenshots/astrotui-benchmark.svg)

## Current terminal smoke comparison

The final native build and unchanged k9s v0.50.18 binary ran sequentially against the same 1,000 scheduling-gated Pods at 120×40. Each had an isolated configuration directory. This is a **30-second idle smoke run**, not a new five-minute soak. Each filter p95 is the maximum of ten samples. Shared-host scheduling and warm caches affect the values; no statistical superiority claim follows.

| Measurement | Astro-TUI | k9s v0.50.18 |
|---|---:|---:|
| First useful resource frame | 397.70 ms | 1746.77 ms |
| Filter p95, ten samples | 198.97 ms | 186.66 ms |
| Idle CPU, one-core percentage over 30s | 1.17% | 5.87% |
| Peak RSS during that window | 69.26 MiB | 142.98 MiB |

Astro-TUI opened faster and used less idle CPU/RSS in this run; its filter p95 was slightly slower. Startup remains above the repository's 300 ms target. Filter timing includes entering and applying the query and is not the watch-to-frame metric. Historical five-minute and 10k-Pod runs belong to the older binary and remain in the prior report.

Raw evidence: [Astro-TUI](measurements/astrotui-parity-1000.json), [k9s](measurements/k9s-parity-1000.json), [validation commands/output and binary identities](measurements/parity-validation.json).

Snapshot sizes:

| Target | MiB |
|---|---:|
| linux_amd64_v1 | 30.000 |
| linux_arm64_v8.0 | 27.875 |
| darwin_amd64_v1 | 30.661 |
| darwin_arm64_v8.0 | 28.727 |

## Completion checklist and remaining qualification

- [x] Implement Pulses and cancel its periodic work on exit.
- [x] Implement UID-aware XRay navigation and partial-access display.
- [x] Implement explicit HTTP load, configuration and private reports.
- [x] Implement user/group/service-account and reverse RBAC exploration.
- [x] Implement context configuration, namespace-pattern/alias views and column attributes.
- [x] Implement saved manifests, Secret decode, containers, references and workload inventory.
- [x] Implement terminal resize, confirmed node shell, bulk/force deletion and read-only controls.
- [x] Implement core Helm and explicit image-scanner integrations.
- [x] Exercise new workflows against real Kubernetes, real terminal processes and real native tools where noted above.
- [x] Add regression tests and CI dependencies; preserve black Astro-TUI / by AlphaBravo branding.
- [x] Document supported commands, limits, optional tools and non-equivalent behavior.
- [ ] Run native Linux ARM/macOS ARM/macOS Intel interactive sessions, SSH variations and screen-reader trials on those actual environments. Cross-builds and simulation are supporting evidence only.
- [ ] Run AWS/GKE/Azure exec credential tools with real expiring credentials and account permissions. The local HTTPS refresh fixture verifies client-go integration, not those accounts.
- [ ] Run tag-triggered signed release publication and verify the downloaded artifacts/container with the intended identity; no publishing authorization was implied by local validation.
- [ ] Repeat statistically controlled startup/filter/soak and operator-task trials across representative clusters. Earlier one-host benchmarks and the current smoke comparison do not prove blanket superiority.
- [ ] Meet the original ≤300ms startup and ≤30MiB-per-target release goals consistently. The macOS Intel snapshot is about **30.66 MiB**, and Linux Intel is just over **30 MiB**; these misses have not been hidden by changing the threshold.
- [ ] Plugins: intentionally deferred by the user, including further compatibility expansion.

Core workflow implementation is substantially broader and is backed by concrete tests. The remaining checklist is the boundary on any statement that it is “k9s quality everywhere.”
