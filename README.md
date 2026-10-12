# astrotui

A Kubernetes terminal UI in the spirit of [k9s](https://k9scli.io), built in Go, intended as a daily-driver replacement.

> **Status:** active development. The [quality audit and implementation plan](docs/reviews/k9s-quality-audit.md) records verified behavior, corrective work, and remaining k9s parity gaps. Full parity has not been established.

## Quickstart

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/alphabravo-oss/astronomer-tui/main/install.sh | sh
```

Or build locally:

```bash
make
./bin/astrotui --context kind-kind
```

For non-interactive connectivity checks:

```bash
./bin/astrotui --headless --context kind-kind
```

Container usage:

```bash
docker run --rm -it -v "$HOME/.kube:/home/nonroot/.kube:ro" ghcr.io/alphabravo-oss/astrotui:<version>
```

## Screenshot

![astrotui main resource view](docs/screenshots/astrotui-main.svg)

The screenshot is exported from the actual tcell renderer with fixture resources.

## Everyday workflow

The resource view opens immediately with context, namespace, policy, counts, and shortcuts. Compact tables show operational columns; `w` exposes the full column set. Selection follows the resource when watch updates or sorting change row order.

| Key / command | Behavior |
|---|---|
| `Ctrl+P` | Search resources, actions, contexts, namespaces, and plugins; arrows/Tab navigate while typing |
| `:pods`, `:deploy`, `:svc` | Change resource family; explicit `:group/version/resources` accesses custom resources |
| `:ns`, `:ns payments`, `0` | Pick a namespace, switch directly, or show all namespaces |
| `:ctx`, `:ctx staging` | Pick or switch context |
| `:refresh` | Reload server discovery |
| `R`, `E`, `b` | Relationships, UID-scoped events, previous related view |
| `:access`, `:diagnose` | Effective namespace permissions, selected volume/claim diagnostics |
| `/` | Live regex filter; `!pattern` excludes; `-l app=api` selects labels; `-f apisrv` fuzzy-matches; `-t [literal]` searches literal text |
| `Esc` | Cancel filter editing, close an overlay, or clear the active resource filter |
| `j` / `k`, arrows, PgUp/PgDn | Navigate rows |
| `Enter`, `d`, `y` | Open YAML details; Enter on a namespace selects it |
| `Ctrl+D` | Confirm deletion (Cancel is the default; protected contexts require their name) |
| `e`, `l`, `x`, `f` | Edit, logs, pod shell, port forward where supported |
| `Shift+1..9` | Sort the corresponding visible column; repeat to reverse |
| `?`, `q`, `:q` | Help or quit |

**Keyboard change:** plain `d` now opens details. Deletion uses `Ctrl+D` and confirmation.

Logs retain the newest 10,000 lines, request an initial tail of 1,000 lines per source, and batch screen updates. `Space` pauses display; `F` toggles following; scrolling up turns following off; `w` wraps lines. Lines larger than 256 KiB are visibly truncated. `p` switches previous-container logs; `t` reconnects. Sources include namespace/pod/container and use server timestamps. Esc closes the stream. Pausing the display does not stop ingestion, so older lines can leave the bounded buffer.

YAML edits preserve field deletions and include the original resource version to reject concurrent changes. Sessions are saved atomically with the selected GVR, namespace, and filter.

## Headline ideas

What we want to do better than k9s:

1. **Global command palette + fuzzy search** — search current-view objects, built-in resource commands, actions, contexts, namespaces, and plugins together.
2. **Optional, configurable AI assistant** — explain failures (Crash/OOM/ImagePull), summarize logs, lint manifests, and translate natural-language intent into actions. Multi-provider (Anthropic / OpenAI / Ollama). Off by default.
3. **Logs deluxe** — multi-pod merged stream, JSON/logfmt parsing, regex search, jump-to-source from a log line to its events or owning workload.
4. **Prod-context safety guard** — typed-confirmation modal for destructive verbs against contexts matching a configurable regex.
5. **Quality-of-life fixes** — session restore (last context+ns+filter), port-forward manager view, live `?` keybind overlay, partial support for k9s `plugins.yaml`.

## Foundational decisions

| Area | Choice |
|---|---|
| Language | Go |
| TUI library | tview / tcell |
| Resource coverage | Built-in resource families + explicit GVR access; server-discovered aliases, scope, CRD printer columns and Table fallback |
| Auth (MVP) | Static kubeconfig + exec plugins (`aws`, `gcloud`, `kubelogin`) |
| Multi-cluster | Single active context + fast switcher |
| Editing | Shell out to `$EDITOR` |
| Config | `~/.config/astrotui/` (active aliases/hotkeys/views; supported k9s import with explicit compatibility limits) |
| Distribution | Signed GitHub Releases binaries + `ghcr.io/alphabravo-oss/astrotui` container image |
| Telemetry | Zero |
| License | Apache-2.0 |

## Core explorers

Use `:pulses` for health, `:xray deployments` for ownership trees, `:rbac` / `:who-can` for visible grants, and `:workloads` for a combined inventory. `:containers`, `:ref`, `:decode`, `:save` and `:sd` cover container details, references, Secret decoding and saved manifests. HTTP load requires explicit review through `:bench`; `:be` opens saved reports.

`:helm` provides release/history/values/manifest and confirmed rollback/uninstall through the native Helm executable. `:scan` runs an explicitly confirmed Grype image scan. These optional native tools must be installed separately. Node shells use an explicitly confirmed, temporary privileged helper; cluster policy may prohibit it.

Start with `--readonly`, `--command pulses` (or `-c`), `--context`, `--namespace` (or `-n`) and `--config-dir` as needed. Press `?` for commands and shortcuts.

## Current limitations

- Customization includes context overlays, aliases/hotkeys, namespace-pattern views and numeric/time column sorting. Arbitrary k9s settings and component-specific skins are not drop-in compatible. Plugin expansion is deferred.
- Metrics require metrics-server and permission to read its API. Missing metrics are shown as unavailable; ordinary resource browsing continues.
- AI is off by default. Every request has a redacted outgoing-data preview and requires Send; arbitrary text still needs review. Secret manifests are blocked. AI action suggestions never execute automatically.
- The distroless image has no editor or cloud authentication executables. Use the native binary for those workflows.
- The installer requires cosign and authenticates signed checksums before verifying/extracting the archive.
- Native macOS/ARM sessions and release publication require CI/external qualification. See the [implementation and evidence report](docs/reviews/implementation-follow-through.md).

See the [pinned k9s core parity matrix](docs/reviews/k9s-v0.50.18-parity.md) for current support, commands, validation and remaining qualification tasks. The [original audit](docs/reviews/k9s-quality-audit.md) records the initial findings.

## Performance targets (NFRs)

- Cold start ≤ 300ms on a 1k-pod cluster
- List re-render ≤ 100ms after watch sync
- Idle CPU ≤ 2% on 1k-pod cluster
- RSS ≤ 150MB after 5 min of normal use
- Stripped binary ≤ 30 MiB per arch

These are targets, not blanket performance guarantees. The [measured comparison](docs/reviews/implementation-follow-through.md#measured-results) records actual terminal timings and remaining gaps, including the 300 ms cold-start target.

## Out of scope (v1)

ArgoCD/Flux integration, cost accounting, web UI, direct OIDC-provider integration, in-cluster service-account auth, Homebrew/Krew distribution. Exec credential refresh is supported through client-go. Some are roadmap; some are intentional non-goals.

## Roadmap

Eight phases, 43 user stories. See the [spec](docs/specs/i-want-to-make-a-clone-of-k9s-but-call-it-astronomer-tui-and.md#9-implementation-phases) for acceptance criteria.

1. Foundation (kubeconfig + auth, ResourceCache, generic table view)
2. Resource breadth (full k9s-parity renderers + CRDs)
3. Write actions + safety guard
4. Logs deluxe
5. Command palette + `?` overlay
6. Plugins, session restore, theming
7. AI module
8. Config, distribution, performance hardening

## Current implementation

- Go module and CLI at `github.com/alphabravo-oss/astrotui`.
- `make`, `make test`, `make lint`, and `make build` scaffolding.
- Kubeconfig loading through `clientcmd.NewNonInteractiveDeferredLoadingClientConfig`, including exec auth plugin registration.
- Dynamic informer-backed `ResourceCache` for arbitrary GVRs.
- tview/tcell Pods table entrypoint with help and palette overlays.
- Built-in and generic renderers for the k9s resource families listed in the spec.
- Fuzzy command palette index plus resource/action/context/namespace/plugin entry builders.
- Config loader, k9s import command, session persistence, prod-context safety guard.
- Log parsing/filtering helpers, AI provider interface/mock/Ollama HTTP path, plugin loader, theme defaults, and port-forward manager model.
- GoReleaser, install script, container image, and CI scaffolding.

## License

[Apache-2.0](LICENSE)
