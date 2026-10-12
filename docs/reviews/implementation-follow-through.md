# Astronomer TUI: parity backlog implementation and evidence

> Subsequent core parity work is recorded in [the pinned k9s v0.50.18 matrix](k9s-v0.50.18-parity.md). That report supersedes this historical report’s statements about missing Pulses/XRay/benchmark/RBAC/Helm/scoped customization and tmux/credential-refresh qualification. The measurements below retain their original binary identity and are not relabeled as results for the newer implementation.

This continues the [initial quality audit](k9s-quality-audit.md). The original P1–P13 task identifiers are retained. Implementation, automated verification, and external release qualification are separate statuses. Having these features does not establish universal superiority over k9s.

## Black theme and branding revalidation

The subsequent user-requested visual pass changed the default dark background to true black (`#000000`) and added the Astro-TUI ASCII logo to the live dashboard, with `by AlphaBravo` immediately underneath. An adaptive compact wordmark preserves resource rows in smaller terminals; there is no blocking startup banner. Context, namespace, access policy, selection, warning and failure colors remain visible.

Revalidated after the change on 2026-10-12:

- Fresh `go test -race ./... -count=1`: passed.
- `make envtest` against Kubernetes 1.36: passed.
- Fresh race-enabled live Kubernetes suite in a new disposable kind 1.32.2 cluster: passed, including actions, logs, exec, forwarding, administrative operations, live resource navigation and watch updates. The metrics-server-specific opt-in check was not repeated in this visual pass; its earlier result is recorded below.
- `make lint`: passed with the `go vet` fallback; `make build`: passed.
- Linux/macOS amd64/arm64 GoReleaser snapshot archives: passed; signing/publication were not invoked.
- Existing terminal size/theme/resize and `NO_COLOR` checks: passed. Actual tcell fixture frames were exported and visually inspected at 120×24, 80×24 and 40×12. The [main screenshot](../screenshots/astrotui-main.svg) now shows the black theme and logo.

Reproduce a visual fixture with `env -u NO_COLOR ASTROTUI_SCREENSHOT=/tmp/astro.svg ASTROTUI_SCREENSHOT_SIZE=80x24 go test ./internal/ui -run TestResourceScreenFixture -count=1`. The benchmark records below are the earlier measured build, before this branding-only follow-up; they have not been relabeled as fresh performance measurements.

## Measured results

Measured on 2026-10-12 using Linux x86_64, an AMD EPYC-Rome virtual CPU (16 logical CPUs), Go 1.26.2, kind Kubernetes 1.32.2 and metrics-server 0.8.0. K9s is the fixed upstream 0.50.18 Linux amd64 release; its downloaded archive was checked against the upstream checksum. Both applications used the same API, fixture namespaces, terminal dimensions and isolated default configurations. Astronomer used its ordinary browsing mode; k9s used `--readonly`. No mutation is part of this benchmark.

These are single-run observations on a shared host, with other verification activity during parts of the runs. They demonstrate behavior in this environment, not guaranteed latency or a statistically controlled universal ranking. First frame means the first visible fixture resource, not completion of every background discovery/metrics request. Filter p95 is the nearest-rank percentile of ten samples (therefore the maximum sample).

| Pods, 120×40 terminal | Astronomer first frame | k9s first frame | Astronomer filter p95 | k9s filter p95 | Astronomer RSS | k9s RSS |
|---|---:|---:|---:|---:|---:|---:|
| 1,000 | 387.89 ms | 1770.19 ms | 206.60 ms | 221.15 ms | 69.12 MiB | 139.82 MiB |
| 5,000 | 1700.70 ms | 3103.76 ms | 170.79 ms | 592.16 ms | 212.54 MiB | 286.53 MiB |
| 10,000 | 3292.80 ms | 5260.45 ms | 225.12 ms | 1303.67 ms | 365.37 MiB | 467.91 MiB |

RSS for 1,000 Pods is after the five-minute idle period; 5,000/10,000-Pod RSS is sampled after filtering without a five-minute soak. The scheduling-gated fixture has no running container log/metrics workload. The real metrics and busy-log tests below exercise those separately.

At 1,000 Pods over 300 seconds, Astronomer used **3.47 CPU seconds (1.16% of one CPU)** and peaked at **70.38 MiB** RSS. K9s used **18.21 CPU seconds (6.07%)** and peaked at **146.93 MiB**. These satisfy Astronomer’s local idle CPU and memory targets for this fixture; they do not prove the same bounds under arbitrary load.

A separate live API mutation → informer → tcell simulation test measured **56.17 ms p95 across 20 samples** ([raw watch measurements](measurements/watch-to-frame.json)). This meets the 100 ms watch-to-frame target in that test; it is distinct from whole-process terminal filtering. The actual 80×24 `NO_COLOR` process run had **zero nondefault-color cells**, a 424.23 ms first frame and 66.99 ms filter p95 ([raw monochrome measurements](measurements/astrotui-1000-80x24-nocolor.json)). Its color mode differs from the auxiliary k9s 80×24 run, so that pair is not used as a like-for-like performance ranking.

Raw comparison records: [Astronomer 1k](measurements/astrotui-1000-120x40.json), [5k](measurements/astrotui-5000-120x40.json), [10k](measurements/astrotui-10000-120x40.json); [k9s 1k](measurements/k9s-1000-120x40.json), [5k](measurements/k9s-5000-120x40.json), [10k](measurements/k9s-10000-120x40.json). Each includes the initial terminal screen, individual filter samples and binary SHA-256.

Final measured Astronomer binary SHA-256: `0be087626e75e114a59c01a10b7d0b0f76a43a311015aea7e2d7e1a1eacc8d78`. All published Astronomer process measurements use that binary.

### Open engineering targets

- [ ] Reduce the measured 1,000-Pod first frame from **387.89 ms to ≤300 ms**. Profile initial client/discovery/cache setup and rendering separately; rerun the same PTY fixture and preserve full identity/scope/error behavior. It is faster than this k9s sample, but the repository target is still missed.
- [ ] Reduce the stripped **darwin/amd64 binary from 30.16 MiB to ≤30 MiB** without compromising resource accounting or safety. Other snapshot binaries are linux/amd64 29.50 MiB, linux/arm64 27.44 MiB and darwin/arm64 28.29 MiB. Cross-compilation succeeded for all four; the per-architecture size target is not fully met.

## Verification results

| Check | Result and scope |
|---|---|
| `go test -race ./...` | Passed across all packages; live tests are opt-in and listed separately. |
| `make lint` | Passed using the documented `go vet ./...` fallback; golangci-lint was unavailable. |
| `make envtest` | Passed against Kubernetes 1.36: namespace-limited RBAC/cache scope, CRD discovery and Table behavior, edit/conflict/precondition workflows. |
| `make e2e` with metrics enabled | Passed against the disposable kind cluster: resource/action/log/exec/forward workflows, administrative operations, live UI and metrics extension. |
| Live administrative race test | Passed: scale/restart/job operations and synthetic-node cordon/uncordon/drain. No real worker node was drained. |
| Live watch-to-frame test | Passed; 20 mutation samples, p95 56.17 ms. |
| `make coverage-nfr` | Passed: cluster 84.8%, actions 73.3%, logs 73.8%. These are package statement coverage, not full behavioral coverage. |
| `make perf` | Passed the existing synthetic harness; actual process results are reported above. |
| Terminal/auth/config/privacy tests | Passed: size/theme/resize/no-color simulation, HTTPS ExecCredential fixture, config import/reload, outgoing AI preview and limits. |
| GoReleaser 2.12.7 check + snapshot | Passed for Linux/macOS amd64/arm64 archives and checksums. Snapshot skipped signing, publishing, Docker and SBOM; real release gates remain external. Legacy Docker configuration emits a nonblocking deprecation notice. |
| Source/script checks | `gofmt -l` clean, Python scripts compile, `sh -n install.sh` passes, `git diff --check` passes. |

The [terminal screenshot](../screenshots/astrotui-main.svg) is generated from the actual tcell simulation screen with synthetic resource/metrics fixtures, and was visually inspected. It is not a screenshot of a production cluster.

## Implemented capabilities

| Task | Implementation | Verification and limits |
|---|---|---|
| P1: discovery | Immutable preferred-version catalog; kind, singular, plural, short-name and qualified commands; list/watch verb filtering; ambiguity errors; `:refresh` invalidates discovery. Background discovery populates palette resources and namespace choices. | Alias/collision/partial-discovery tests; real API CRD installation/removal. Explicit GVRs remain available for nonpreferred versions. Discovery failures retain the last useful catalog. |
| P2: scope/lifecycle | API LIST/WATCH calls are namespace-scoped. Discovered cluster resources use cluster scope. Only the active resource/scope owns an informer cache; switching releases old watches and stores. Successful empty relists clear stale errors. | Real RBAC principal can list/watch its namespace while cluster-wide listing is forbidden. Repeated namespace switching verifies one remaining watch and zero after shutdown. No unbounded inactive-view cache. |
| P3: columns | CRD printer names, types, priorities, and client-go JSONPath expressions; priority columns appear with `w`. Background server Table fallback for resources without accessible CRD definitions, with metadata identity/version checks. Persistent views in `views.yaml`. | Server printer output verified against the API; JSONPath arrays, escaped label keys, missing paths. Fallback polls every five seconds; cells display `…` while the server Table snapshot is behind a watch object. |
| P4: metrics | Background metrics.k8s.io pods/nodes polling, CPU millicores, memory MiB, requests/limits/percentages and node allocatable. Kubernetes resource helpers account for init/sidecar, overhead and pod-level resources. `n/a`, `stale`, and unavailable status are explicit. | Quantity, missing-extension and live metrics-server tests. No metrics error prevents resource browsing. Thirty-second polling, five-second timeout, two-minute timestamp staleness threshold. Declared resource totals, not a scheduler simulator. |
| P5: relationships | `R` opens workload→pods, pod→containers, Service→pods/EndpointSlices, CronJob→Jobs, Node→pods, owners and events. `E` opens exact-UID events; `b` returns to the prior scope/filter. Owner opening fetches and verifies UID. | Same-name replacement event tests; owner UID checks. Workload membership follows its label selector; overlapping workload selectors can therefore share pod results, as in Kubernetes. |
| P6: logs | Regular-container default and annotation override; init/ephemeral/all-container picker; merged workload containers; namespace/pod/container source identity; server timestamps; `p` previous logs and `t` reconnect; bounded history and line size. Search uses filtered displayed rows, including explicit wrapping. AI output uses a separate view. | Timestamp/source/wrapped-row regressions, busy ingestion responsiveness, prior live log/exec flows. Reconnection is explicit; it restarts the bounded tail rather than promising exactly-once delivery across pod restarts. |
| P7: responsiveness | Cancellable background operations for context creation, actions, workload log target lookup, log source navigation, failure-analysis preparation, permission review and storage diagnostics. API operations capture source client/target. Late completion cannot retarget a new view. | A five-second operation can be cancelled without freezing input. UI update queue is bounded and resource paints are coalesced. Editors/shells deliberately own the terminal during interactive use. Most API operations time out in 30 seconds; drain allows ten minutes and can be cancelled. |
| P8: configuration | Runtime aliases, hotkeys, JSONPath views, sort columns, wide/hidden/alignment attributes; custom file reload keeps last valid settings. Plugin scope matching, shortcut dispatch, directory snippets, and native override ordering. Import translates shared skin colors and copies supported files without overwriting conflicts. | Fixture import/config tests, alias-cycle and unsupported-field rejection. K9s global config is archived explicitly, not claimed as active. Namespace-pattern views, per-context overlays, and arbitrary k9s plugin extensions remain outside the supported compatibility subset. |
| P9: AI privacy/control | Every UI request previews the exact redacted payload and destination before Send. Secret manifests are denied. Common credential keys, literal environment values, annotations, bearer tokens and private keys are redacted. Post-redaction request cap 64 KiB; input rejection above 4 MiB; bounded responses and cancellation. AI action proposals are suggestions only. | Local provider error/redaction/size tests; simulation verifies preview. Arbitrary text cannot be perfectly classified: the preview is mandatory. No external provider was invoked by this work. |
| P10: distribution | Installer requires cosign, verifies the checksum signature's GitHub OIDC issuer and repository release workflow identity, then checks archive SHA-256 before extracting only the binary. Explicit versions require an exact signing identity. Pinned action SHAs/Go/cosign/GoReleaser, release tests, SBOM/provenance and anonymous container-pull gate. | Installer valid/tampered/rejected-signature cases; local four-target GoReleaser snapshot. Real GitHub OIDC signing, SBOM upload, attestations and anonymous registry publication must run in release CI. Nothing was published. |
| P11: performance | Actual PTY process harness with terminal parsing, first useful frame, ten filter samples, nearest-rank p95, five-minute RSS/CPU and binary identity. Reproducible inert 1k/5k/10k pod fixtures and fixed k9s comparison. Added formatted-cell caching, hoisted column metadata, input-filter debounce, and periodic age refresh. | Raw measurements are in [measurements](measurements/). Single-machine observations, not a statistically controlled operator usability study. Busy-log and repeated-switch lifecycle tests are separate from the PTY measurements. |
| P12: terminals/auth | Simulated 40×12, 80×24, 120×40 and 200×60 layouts with resize and dark/light/dim themes. `NO_COLOR` strips colors at the terminal boundary while preserving focus emphasis. ExecCredential fixture authenticates an actual HTTPS request. CI includes Linux/macOS on amd64/arm64; release snapshots cross-compile all four. | Native macOS/ARM, real cloud credential executables, SSH/tmux and a comprehensive screen-reader/no-color assessment still require those environments. Cross-compilation is not a substitute for those sessions. |
| P13: operations | DaemonSet restart, ReplicaSet scale, CronJob trigger, Job/CronJob suspend/resume, Node cordon/uncordon/drain, `:access` effective RBAC review and `:diagnose` storage status/events. Mutations honor read-only/protected-context policy and identity/version checks. | Fake and API-server identity tests, policy tests, drain preflight/PDB/error/cancellation tests. Drain refuses unmanaged pods and emptyDir data, skips mirror/DaemonSet pods, uses eviction UID preconditions and respects PDBs. Cancellation/failure can leave the node cordoned; that state is explicitly reported. |

## Completed engineering tasks

- [x] Replace static-only resource discovery with preferred-version metadata and resolvers.
- [x] Scope informer API requests; cancel replaced view/context caches.
- [x] Integrate CRD columns and server Table negotiation.
- [x] Connect live metrics and Kubernetes quantity/resource accounting.
- [x] Add relationship navigation, UID event selection and verified owner opening.
- [x] Add previous/all-container logs, full source identity and server-time ordering.
- [x] Map wrapped visible lines to their source; keep regex matches in the current filtered document.
- [x] Move slow lookup/mutation/AI preparation work off the event loop.
- [x] Implement active aliases/hotkeys/views and scoped plugin dispatch.
- [x] Translate supported skin settings; report unsupported compatibility instead of promising a drop-in import.
- [x] Add outgoing AI preview, redaction, size limits and suggestion-only actions.
- [x] Authenticate release checksums and pin release tools/actions.
- [x] Add missing workload/node operations, permission and storage diagnostics.
- [x] Add port-forward collision checks, readiness/error state and context/shutdown cleanup.
- [x] Add targeted race, API-server, live metrics, terminal, lifecycle and distribution tests.
- [x] Add real terminal benchmark and reproducible fixture tools.

## Verification commands

```sh
go test -race ./...
make lint                 # go vet fallback if golangci-lint is unavailable
make envtest              # managed Kubernetes 1.36 control-plane binaries
make coverage-nfr
make build
make perf
KUBECONFIG=/tmp/astrotui-quality.kubeconfig \
  ASTROTUI_E2E=1 ASTROTUI_E2E_CONTEXT=kind-astrotui-quality make e2e
KUBECONFIG=/tmp/astrotui-quality.kubeconfig \
  ASTROTUI_E2E_CONTEXT=kind-astrotui-quality ASTROTUI_METRICS_E2E=1 \
  go test -race -tags=e2e ./internal/ui -run TestLiveMetricsExtension -count=1
goreleaser check
goreleaser release --snapshot --clean --skip=publish,sign,docker,sbom,validate
git diff --check
```

The isolated kind cluster uses Kubernetes 1.32.2. Metrics-server 0.8.0 is installed solely in that disposable cluster, with its insecure-kubelet-TLS option required by the kind fixture. This does not change application TLS settings. The environment's other Docker workloads are untouched.

## Reproduce terminal measurements

Install `pyte==0.8.2` in a disposable Python environment. Set KUBECONFIG to a disposable cluster. The fixture script refuses namespaces it does not own and uses a scheduling gate, so benchmark Pods never launch containers.

```sh
python3 scripts/benchmark_fixture.py --namespace astrotui-bench-1000 --count 1000
python3 scripts/terminal_benchmark.py \
  --binary ./bin/astrotui --product astrotui \
  --context kind-astrotui-quality --namespace astrotui-bench-1000 \
  --count 1000 --width 120 --height 40 --idle-seconds 300 \
  --output /tmp/astrotui-1000.json
# Repeat with the same fixture, terminal dimensions and fixed k9s binary:
python3 scripts/terminal_benchmark.py \
  --binary /path/to/k9s --product k9s \
  --context kind-astrotui-quality --namespace astrotui-bench-1000 \
  --count 1000 --width 120 --height 40 --idle-seconds 300 \
  --output /tmp/k9s-1000.json
```

The timer begins before spawning the binary and ends at the first parsed terminal frame containing a fixture Pod. Filter timings include typing the query and applying it; they are not watch-event latency. CPU is process user+system time from `/proc`, and RSS is sampled during the idle period. This harness currently measures processes on Linux. Each process gets a temporary configuration directory (`--config-dir` for Astronomer and `K9S_CONFIG_DIR` for k9s), so user settings and saved sessions do not affect the comparison. The `count` argument records fixture size; ensure the namespace actually contains that count. Raw outputs include errors rather than treating an incomplete run as a result.

Initial measurements exposed expensive repeated row formatting while typing. The implementation now reuses formatted cells until the source snapshot changes, coalesces filter input, and avoids reconstructing column metadata for every table cell. The published samples identify their binary hash; interim build measurements are excluded from the final comparison.

## External qualification still required

These are acceptance gates that cannot be replaced with local implementation claims:

- [ ] Run the configured native Linux/macOS amd64/arm64 CI matrix on the final revision.
- [ ] Exercise representative SSH/tmux terminals, screen readers and no-color operation; record focus, resize, clipboard and Unicode behavior.
- [ ] Run real AWS/GCP/Azure exec-credential workflows, including credential expiry and interactive renewal.
- [ ] Publish a release candidate through GitHub CI; verify its actual Sigstore identity, provenance and SBOM, then install it and pull its public container anonymously.
- [ ] Conduct timed human operator trials on a fixed k9s version: CrashLoop diagnosis, previous multi-container logs, namespace-limited RBAC, CRDs, concurrent edits, API interruption, context changes and drain/PDB interruption. Record completion rate and mistakes as well as speed.

The remaining compatibility boundaries are explicit: k9s per-context config overlays and namespace-pattern custom views are rejected/not imported as functional equivalents; all k9s extension variables and schema fields are not supported; Helm/GitOps management remains outside the original project scope. No claim of complete k9s feature equivalence is made.

## Design references

- [Kubernetes API discovery and Table content negotiation](https://kubernetes.io/docs/reference/using-api/api-concepts/): use server scope/type metadata and negotiate Table responses while retaining resource identity.
- [CRD printer columns](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/): served versions, JSONPath/type/priority semantics.
- [kubectl logs](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_logs/): previous container, timestamps and all-container behavior.
- [K9s commands](https://k9scli.io/topics/commands/), [aliases](https://k9scli.io/topics/aliases/), [hotkeys](https://k9scli.io/topics/hotkeys/), [custom columns](https://k9scli.io/topics/columns/) and [plugins](https://k9scli.io/topics/plugins/): benchmark interaction and compatibility schema references, not an assertion that every extension is implemented.
- [Sigstore verification](https://docs.sigstore.dev/cosign/verifying/verify/), [GitHub provenance action](https://github.com/actions/attest-build-provenance), [Anchore SBOM action](https://github.com/anchore/sbom-action): release authenticity and build evidence.
