# astrotui — Specification

*Lisa interview output — 2026-05-08. Project codename `astrotui`. Repo: `github.com/alphabravo-oss/astrotui`.*

> Note on naming: the original prompt called the project "astronomer-tui". During the interview the user renamed the user-facing tool to **`astrotui`**. The original Lisa slug (`i-want-to-make-a-clone-of-k9s-but-call-it-astronomer-tui-and`) is retained for filename continuity only.

## 1. Overview

`astrotui` is a Kubernetes terminal UI in the spirit of [k9s](https://k9scli.io), built in Go, intended as a **daily-driver replacement** for k9s. It keeps k9s's speed and table-centric model while shipping two headline differentiators:

1. **Global command palette + fuzzy search** — a VSCode-style palette is the primary navigation model. The user types to find any resource, action, context, namespace, or plugin entry without memorizing keybindings.
2. **Optional, configurable AI assistant** — explain failures, summarize logs, lint manifests, and translate natural-language intent into actions. Off by default; provider-pluggable.

## 2. Problem statement

k9s is the de-facto k8s TUI but has known frustrations:
- **Discoverability is gated by memorized keybindings** (`:po`, `:dp`, `Shift+Y`, `Ctrl+D`, `t` for trace, …). New and occasional users hit a wall.
- **Multi-step navigation across namespaces / contexts / resource types** — every cross-axis move is several keystrokes.
- **No built-in intelligence** for triaging failures (CrashLoopBackOff, OOMKilled, ImagePullBackOff). Users alt-tab to a chat window and paste YAML.
- **Logs UX is single-pod-centric**; merging logs across replicas, parsing JSON/logfmt, and pivoting from a log line to its owning workload requires external tools.
- **Lost session state**: reopening k9s drops the user back at the default view, not where they were.

`astrotui` keeps everything good about k9s (speed, table model, plugins) and addresses these gaps.

## 3. Foundational decisions

| Area | Decision |
|---|---|
| Language | **Go** — k9s ecosystem parity, `client-go`. |
| TUI library | **tview / tcell** — k9s parity, mature, fast tables. |
| Module path | `github.com/alphabravo-oss/astrotui` |
| Binary name | `astrotui` |
| Config dir | `~/.config/astrotui/` |
| License | **Apache 2.0** (k9s parity) |
| Multi-cluster | Single active context + fast switcher (palette-driven). Data layer architected so future "warm background contexts" mode is purely additive. |
| Safety | Read+write enabled by default; **prod-context guard** — destructive verbs require typed confirmation when current context matches a configurable regex (default `(?i)prod|production`). |
| AI | **Opt-in, multi-provider** (Anthropic / OpenAI / Ollama) behind a single `LLMProvider` interface. Tool fully usable without it. |
| Resource coverage | **Full k9s built-in parity** + dynamic CRDs. |
| Plugins | **Drop-in compatible** with `~/.config/k9s/plugins.yaml`. |
| Editing | Shell out to `$EDITOR` (k9s parity). |
| Telemetry | **Zero**. No analytics, no auto update-check, no error reporting. |
| Distribution | GitHub Releases (signed per-arch binaries + install script) and container image at `ghcr.io/alphabravo-oss/astrotui`. No Homebrew/Krew in v1. |
| Tests | `envtest` (sigs.k8s.io/controller-runtime) for unit; **kind cluster in CI** for end-to-end. AI tests use a `MockLLMProvider`; a real-call integration suite is gated by env vars (opt-in). |
| MVP cut-line | **Big bang** — full vision in v1. |

## 4. Scope

### 4.1 In scope

- Read/list/watch/describe of all resources k9s ships built-in views for, plus dynamic CRDs.
- Edit (`$EDITOR`), delete (with prod guard), scale, restart, port-forward, exec.
- Multi-pod merged log stream, structured-log parsing (JSON/logfmt), regex search, jump-to-source (events/owner), AI-summarize button.
- Command palette indexing resources, actions, contexts, namespaces, and plugin entries.
- AI module: explain failure, summarize logs, explain/lint manifest, NL-to-action in palette.
- k9s `plugins.yaml` drop-in compatibility.
- Session restore (last context + namespace + filter).
- Port-forward manager view.
- Live keybind discoverability overlay (`?` → contextual cheat sheet).
- Theme/skin engine.
- Authentication: static kubeconfig + exec auth plugins (`aws eks get-token`, `gke-gcloud-auth-plugin`, `kubelogin`).
- One-shot `astrotui config import-k9s` to migrate skin/hotkeys/aliases.
- Performance NFRs as acceptance criteria (see §7).

### 4.2 Out of scope (v1)

- Helm release management (no `helm list/install/upgrade` views).
- ArgoCD / Flux GitOps integration (no Application CR views or sync buttons).
- Cost/metrics overlay (no Prometheus/OpenCost integration; no `$/hr` columns).
- Web UI / hybrid mode (pure terminal only).
- Homebrew tap and Krew distribution (defer to v1.x).
- OIDC token refresh and in-cluster service-account auth (defer; static + exec auth covers ~95% of users).
- Concurrent multi-cluster split-pane (data layer is ready; UI deferred).
- In-TUI YAML editor with schema validation (use `$EDITOR`).
- Telemetry of any kind.

### 4.3 Things Ralph (or any implementer) should ignore

- Anything in `k9s` source about `xray`, `pulse`, `popeye` integrations — not in v1 scope.
- Discussion of mouse support beyond what tcell gives for free.
- Cross-platform shells beyond bash/zsh/fish for the install script.

## 5. User stories

> Each story is sized for **one focused coding session**. Acceptance criteria are specific and verifiable.

### Foundation (Phase 1)

#### US-1: Project scaffolding & module bootstrap
**Description:** As a maintainer, I want a Go module, layout, Makefile, lint/test scaffolding so I can iterate.
**Acceptance criteria:**
- [ ] `go mod init github.com/alphabravo-oss/astrotui` succeeds.
- [ ] Layout: `cmd/astrotui/`, `internal/{cluster,ui,config,palette,ai,plugins,logs,actions}`, `pkg/...`, `test/`, `docs/`.
- [ ] `make build` produces `bin/astrotui` ≤ 30 MB stripped.
- [ ] `make test` runs `go test ./...` with race detector.
- [ ] `make lint` runs `golangci-lint` with `errcheck`, `staticcheck`, `gosec`, `revive`.
- [ ] `make` (default) runs lint + test + build.
- [ ] Apache-2.0 LICENSE and minimal README at repo root.

#### US-2: Kubeconfig loading + auth providers
**Description:** As a user, I want my existing kubeconfig (including EKS/GKE/AKS exec plugins) to just work.
**Acceptance criteria:**
- [ ] `clientcmd.NewNonInteractiveDeferredLoadingClientConfig` loads `KUBECONFIG` env or `~/.kube/config`.
- [ ] Exec-auth plugins (`aws`, `gcloud`, `kubelogin`) resolve via `client-go/plugin/pkg/client/auth/exec` (registered).
- [ ] `astrotui --context <name>` overrides current-context.
- [ ] Errors when kubeconfig missing/malformed are printed in plain English with the offending path; exit code 64 (EX_USAGE).
- [ ] Integration test: kind cluster's kubeconfig loads and a `clientset.CoreV1().Namespaces().List()` returns ≥1 namespace.

#### US-3: Resource discovery + watch infrastructure
**Description:** As a developer, I want a generic informer-backed cache so every resource view shares one watch primitive.
**Acceptance criteria:**
- [ ] Uses `dynamic.SharedInformerFactory` for arbitrary GVRs.
- [ ] `discovery.CachedDiscoveryClient` lists all GVRs (built-in + CRDs) in <500ms on first call.
- [ ] A `ResourceCache` type exposes `List(gvr, ns, selector) []*unstructured.Unstructured` and `Subscribe(gvr) <-chan Event`.
- [ ] Unit tests use `envtest` to verify watch events propagate within 200ms of object creation.
- [ ] Memory ceiling: ≤30MB for cache holding 5,000 pods (measured with `pprof`).

#### US-4: Generic resource list view (table widget)
**Description:** Render any GVR as a sortable, filterable table with watch updates.
**Acceptance criteria:**
- [ ] tview Table renders columns supplied by a per-resource `Renderer` (default renderer for unknown GVRs).
- [ ] `/<text>` filters rows in <16ms for ≤5k rows.
- [ ] Sort by any column with `Shift+<n>`; visible sort indicator.
- [ ] Live updates from `ResourceCache.Subscribe` apply incrementally without flicker.
- [ ] Acceptance: open Pods view on a 1k-pod kind cluster — first frame <100ms after watch synced.

### Resource breadth (Phase 2)

#### US-5: Core renderers — pods, deployments, services, endpoints, configmaps, secrets, namespaces, nodes, events
**Acceptance criteria:** Each has a `Renderer` returning k9s-equivalent columns; verified by snapshot test against a fixture cluster.

#### US-6: Workload renderers — statefulsets, daemonsets, jobs, cronjobs, replicasets, hpas, pdbs

#### US-7: Networking renderers — ingresses, endpointslices, networkpolicies, services (already in US-5, refine)

#### US-8: Storage renderers — pvs, pvcs, storageclasses, volumeattachments

#### US-9: RBAC renderers — roles, rolebindings, clusterroles, clusterrolebindings, serviceaccounts

#### US-10: Cluster renderers — nodes (extended), events (extended), apiservices, leases

#### US-11: Dynamic CRD view
**Description:** Any CRD discovered at runtime gets a generic auto-rendered view (additionalPrinterColumns honored).
**Acceptance criteria:**
- [ ] CRD with `additionalPrinterColumns` renders those columns in the listed order.
- [ ] CRD without printer columns falls back to `[NAME, AGE]`.
- [ ] Test: install a CRD via envtest, create instances, verify they appear in the dynamic view.

### Write actions + safety guard (Phase 3)

#### US-12: Prod-context guard primitive
**Description:** A reusable confirmation modal that requires typing the context name.
**Acceptance criteria:**
- [ ] Triggered when `currentContext` matches `safety.prodPattern` (default `(?i)prod|production`) AND verb ∈ {delete, scale, restart, exec-into-prod-pod, edit, patch}.
- [ ] User must type the exact context name to proceed; ESC cancels.
- [ ] Configurable per-context override in `config.yaml` (`contexts.<name>.policy: read-only|confirm-destructive|full`).

#### US-13: Edit via `$EDITOR`
**Acceptance criteria:** writes object YAML to a tempfile, opens `$EDITOR`, applies on save with strategic-merge-patch; rejects with diff modal on conflict.

#### US-14: Delete with guard

#### US-15: Scale workload (`s` on a Deployment/STS) — replica modal, prod guard.

#### US-16: Restart workload (`r`) — `kubectl rollout restart` equivalent (patch `spec.template.metadata.annotations.kubectl.kubernetes.io/restartedAt`).

#### US-17: Exec into pod
**Acceptance criteria:** `e` opens an exec PTY using SPDY; container picker if pod has >1 container; works on `bash`/`sh`/`/bin/ash` fallback chain.

#### US-18: Port-forward (`f` on Pod or Service)
**Acceptance criteria:** Modal asks local + remote port (defaults from container/service ports); spawns goroutine; shown in port-forward manager.

#### US-19: Port-forward manager view (`:pf`)
**Acceptance criteria:** Lists active forwards with `LOCAL`, `RESOURCE`, `REMOTE`, `SINCE`; `Ctrl+K` kills.

### Logs deluxe (Phase 4)

#### US-20: Single-pod log view
**Acceptance criteria:** Tail+follow, container picker, line-wrap toggle, save-to-file. <50ms keystroke→render.

#### US-21: Multi-pod merged stream
**Description:** Selecting a workload (Deploy/STS/DS/Job) streams logs from ALL its pods into one merged view, color-coded by pod.
**Acceptance criteria:**
- [ ] Each pod gets a stable color from a 16-color palette (deterministic hash of pod name).
- [ ] Lines prefixed `[<pod>] ` (or column-aligned by config).
- [ ] Pod additions/removals during stream handled without losing buffer.

#### US-22: Structured-log parsing + regex search
**Acceptance criteria:**
- [ ] Auto-detects JSON or logfmt per line; offers `:level=error` / `:msg~="timeout"` filters.
- [ ] `Ctrl+R` enters regex mode; matches highlighted; `n`/`N` jumps.
- [ ] `:save` writes filtered result to file.

#### US-23: Jump-to-source + AI summarize button
**Acceptance criteria:**
- [ ] On a log line, `g e` jumps to the events view filtered to the originating pod; `g o` jumps to the owning workload; `g a` opens AI-summarize panel preloaded with the surrounding 100 lines and pod metadata.
- [ ] AI button is disabled when AI is not configured (greyed with hint).

### Command palette + discoverability (Phase 5)

#### US-24: Palette infrastructure
**Acceptance criteria:**
- [ ] `Ctrl+P` opens palette; fuzzy match (e.g. fzf-style scoring).
- [ ] <50ms response to keystroke on an index of 10k entries.
- [ ] Result categories visually grouped: Resources / Actions / Contexts / Namespaces / Plugins.

#### US-25: Resource indexer
**Acceptance criteria:** Every cached object → palette entry `<kind>/<ns>/<name>`. Selection navigates to its detail view.

#### US-26: Action indexer
**Acceptance criteria:** Verbs scoped to current selection. E.g. on a Deployment: `restart`, `scale`, `logs`, `edit`, `delete`, `pf`, `describe`. Each maps to the same handler the keybind would invoke.

#### US-27: Context / namespace indexer
**Acceptance criteria:** All contexts from kubeconfig + all namespaces in current context. Selection switches.

#### US-28: Plugin indexer
**Acceptance criteria:** Every entry from `plugins.yaml` (k9s drop-in) appears with its description; selection executes per the plugin's `command`/`args` semantics.

#### US-29: Live keybind overlay (`?`)
**Acceptance criteria:** `?` shows a panel of every action available on the current view + selection, with the keybind, the verb, and a one-line description. Updates as selection changes.

### Plugins, sessions, theming (Phase 6)

#### US-30: k9s plugins.yaml drop-in loader
**Acceptance criteria:** Reads `~/.config/k9s/plugins.yaml`; supports `description`, `scopes`, `shortCut`, `command`, `args`, `background`, `confirm`. Plugin executions run in a tcell-suspended subshell.

#### US-31: Native astrotui plugin schema (additive, optional) at `~/.config/astrotui/plugins.yaml`.

#### US-32: Session restore
**Acceptance criteria:** On exit, persist `{context, namespace, lastView, lastFilter}` to `~/.config/astrotui/state.json`. On launch with no flags, restore. `--no-restore` skips.

#### US-33: Theme/skin engine
**Acceptance criteria:** YAML at `~/.config/astrotui/skin.yaml`; live-reload on save; ships 3 default skins (`dark`, `light`, `dim`).

### AI module (Phase 7)

#### US-34: `LLMProvider` interface + multi-provider config
**Acceptance criteria:**
- [ ] Interface methods: `Complete(ctx, req) (Response, error)` with streaming variant `Stream(ctx, req) (<-chan Chunk, error)`.
- [ ] Implementations: `Anthropic` (uses prompt caching for system + tool defs), `OpenAI`, `Ollama` (HTTP).
- [ ] Config: `ai.provider: anthropic|openai|ollama`, `ai.model`, `ai.apiKeyEnv`, `ai.endpoint` (Ollama).
- [ ] `ai.enabled: false` by default. When false, all AI UI is hidden/greyed.
- [ ] Tests use `MockLLMProvider`; opt-in `make test-llm-real` exercises real providers when `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OLLAMA_HOST` are set.

#### US-35: AI feature — Explain failure
**Acceptance criteria:** On a failing pod (Crash/OOM/ImagePull/Pending), `Ctrl+E` opens an AI panel pre-loaded with `pod.spec`, `pod.status`, recent events; user gets a streaming explanation + likely fixes.

#### US-36: AI feature — Summarize / Q&A logs
**Acceptance criteria:** From the logs view, `Ctrl+E` opens a panel with the visible (filtered) buffer as context; user can ask follow-ups in a chat box.

#### US-37: AI feature — Explain / lint manifest
**Acceptance criteria:** From any resource YAML view, `Ctrl+E` lets the user ask "what does this do?" or "what's wrong here?". Streaming response.

#### US-38: AI feature — NL-to-action in palette
**Acceptance criteria:** When palette query starts with `> ` (or `?`), text after is sent to LLM with the action catalog as context; LLM responds with one of: a structured action call (executed after confirmation modal) or a clarifying question.

### Config, distribution, hardening (Phase 8)

#### US-39: Config loader + schema validation
**Acceptance criteria:** `~/.config/astrotui/config.yaml` parsed with strict schema; validation errors exit 78 (EX_CONFIG) with line/column. Documented schema in `docs/config.md`.

#### US-40: `astrotui config import-k9s`
**Acceptance criteria:** Reads `~/.config/k9s/{config.yaml, skin.yaml, hotkeys.yaml, aliases.yaml}` and writes equivalents under `~/.config/astrotui/`. Idempotent. Reports a per-file `imported|skipped|conflict` summary.

#### US-41: Build & release pipeline
**Acceptance criteria:** GoReleaser config produces signed binaries (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`) on tag push. Install script `curl -fsSL https://raw.githubusercontent.com/alphabravo-oss/astrotui/main/install.sh | sh` works on macOS and Linux.

#### US-42: Container image
**Acceptance criteria:** `ghcr.io/alphabravo-oss/astrotui:<version>` is multi-arch, ≤80 MB, runs `astrotui` as entrypoint. Documented `docker run` invocation that mounts `~/.kube`.

#### US-43: Performance harness + NFR enforcement
**Acceptance criteria:** see §7. CI fails if any NFR target regresses. Harness runs against a kind cluster pre-loaded with 1,000 pods.

## 6. Functional requirements (FR)

- **FR-1** Loads kubeconfig from `KUBECONFIG` env or `~/.kube/config`; supports static (cert/token) and exec-plugin auth.
- **FR-2** Lists, watches, describes every resource k9s ships built-in views for, plus dynamic CRDs.
- **FR-3** Edits (via `$EDITOR`), deletes, scales, restarts, port-forwards, and execs into resources where applicable.
- **FR-4** Streams logs from a single pod or all pods of a workload, with structured-log parsing and regex search.
- **FR-5** Provides a global command palette indexing resources, actions, contexts, namespaces, and plugins; fuzzy-matched.
- **FR-6** Drop-in compatible with k9s `plugins.yaml`.
- **FR-7** Optional AI assistant with multi-provider support (Anthropic/OpenAI/Ollama) for: explain failure, summarize logs, explain manifest, NL-to-action.
- **FR-8** Prod-context guard requires typed confirmation for destructive verbs against contexts matching a configurable regex.
- **FR-9** Persists and restores last `{context, namespace, view, filter}` across sessions.
- **FR-10** Live `?` keybind overlay reflects the actions available on current view + selection.
- **FR-11** Theme/skin engine via YAML with live reload; ships 3 default skins.
- **FR-12** `astrotui config import-k9s` migrates k9s skin/hotkeys/aliases.
- **FR-13** Port-forward manager view tracks all session forwards.
- **FR-14** Distributed via signed GitHub Releases binaries and a multi-arch container image.

## 7. Non-functional requirements (NFR)

- **NFR-1 — Cold start.** ≤300ms from launch to first usable frame on a 1k-pod kind cluster. Measured by a CI benchmark.
- **NFR-2 — Render latency.** Resource list re-renders ≤100ms after watch sync; keystroke→frame ≤16ms (60fps target) under typical load.
- **NFR-3 — Idle CPU.** ≤2% on a 1k-pod cluster, averaged over 60s while user is idle on a list view.
- **NFR-4 — Memory.** RSS ≤150MB after 5 min of normal use on a 1k-pod cluster.
- **NFR-5 — Binary size.** Stripped binary ≤30MB per arch.
- **NFR-6 — Auth coverage.** Static kubeconfig + exec plugins (`aws`, `gcloud`/`gke-gcloud-auth-plugin`, `kubelogin`) work without manual setup beyond having the helper on PATH.
- **NFR-7 — Telemetry.** Zero. No outbound network calls except the user's apiserver and (when AI enabled) the configured LLM provider.
- **NFR-8 — Test coverage.** Unit tests with envtest cover ≥70% of `internal/cluster`, `internal/actions`, `internal/logs`. Kind-based e2e covers the golden path of every user story.
- **NFR-9 — Security.** Binaries signed (cosign keyless via Sigstore). No CGO. Container image runs as non-root.
- **NFR-10 — Crash-free.** Panics in handlers must not take down the TUI; recovered with an in-app error modal that links to a saved crash log.

## 8. Technical design

### 8.1 Module layout

```
github.com/alphabravo-oss/astrotui
├── cmd/astrotui/             # main, flags, version
├── internal/
│   ├── cluster/              # kubeconfig, clients, informers, ResourceCache
│   ├── ui/                   # tview app, layout, theme, components
│   │   ├── views/            # per-resource Renderers + detail views
│   │   ├── palette/          # command palette
│   │   ├── logs/             # logs widget (single, multi, structured)
│   │   └── overlay/          # ? keybind overlay, modals
│   ├── actions/              # edit, delete, scale, restart, exec, pf, pf-manager
│   ├── ai/                   # LLMProvider interface + anthropic/openai/ollama
│   ├── plugins/              # k9s plugins.yaml loader
│   ├── config/               # schema, loader, import-k9s
│   ├── safety/               # prod-context guard
│   └── session/              # state.json persistence
├── pkg/                      # (reserved; nothing exported initially)
├── test/
│   ├── envtest/              # envtest fixtures + harness
│   └── kind/                 # kind-based e2e
├── docs/
│   ├── config.md
│   ├── plugins.md
│   └── ai.md
├── install.sh
├── Dockerfile
├── .goreleaser.yml
├── Makefile
├── LICENSE                   # Apache 2.0
└── README.md
```

### 8.2 Data layer

- `internal/cluster/cache.go` wraps a `dynamic.DynamicSharedInformerFactory` keyed by GVR.
- `discovery.CachedDiscoveryClient` populates the GVR catalog at startup; refreshed on context switch.
- A single `ResourceCache` is shared by all views; each view subscribes to the GVRs it needs.
- Context switch tears down the factory and rebuilds; cancellation via `context.Context`.

### 8.3 UI layer

- tview pages stack: `main` (focused list), `detail` (YAML/describe), `logs`, `palette` (modal), `overlay` (`?`).
- Theme tokens (`fg`, `bg`, `accent`, `warn`, `error`, `subtle`, `selection`) flow from `internal/ui/theme.go`; all components read from theme, never hard-code colors.
- `Renderer` interface per resource:
  ```go
  type Renderer interface {
      GVR() schema.GroupVersionResource
      Header() []Column
      Row(*unstructured.Unstructured) []Cell
      Detail(*unstructured.Unstructured) DetailContent // describe-style
  }
  ```

### 8.4 AI layer

- `LLMProvider` interface (see US-34).
- Prompt templates live under `internal/ai/prompts/` with golden tests.
- `MockLLMProvider` returns canned responses keyed by prompt hash; used in all unit/integration tests.
- Real-provider tests gated behind `ASTROTUI_AI_REAL=1`.

### 8.5 Safety layer

- `internal/safety/guard.go` exposes `RequireConfirmation(ctx, verb, resource) error` consulted before every destructive action.
- Policy resolution: `config.contexts[currentContext].policy` (if set) wins, else `config.safety.prodPattern` regex match → `confirm-destructive`, else `full`.

### 8.6 Plugin loader

- Reads `~/.config/k9s/plugins.yaml` (k9s schema) and optionally `~/.config/astrotui/plugins.yaml`.
- Each plugin registers (a) a keybind in the configured `scopes`, (b) a palette entry.
- Execution runs in a tcell-suspended subshell so terminal raw-mode is restored cleanly.

## 9. Implementation phases

### Phase 1 — Foundation
US-1, US-2, US-3, US-4.
**Verification:** `make build && make test && make lint` green; `astrotui --context kind-kind` opens to a Pods view that updates live.

### Phase 2 — Resource breadth
US-5 through US-11.
**Verification:** every k9s built-in resource has a renderer covered by snapshot tests; CRDs auto-render.

### Phase 3 — Write actions + safety guard
US-12 through US-19.
**Verification:** kind-based e2e exercises edit/delete/scale/restart/exec/pf with a contrived `kind-prod` context to prove the guard fires.

### Phase 4 — Logs deluxe
US-20 through US-23.
**Verification:** envtest + kind e2e for single/multi/structured/jump; AI summarize is mocked.

### Phase 5 — Command palette + discoverability
US-24 through US-29.
**Verification:** palette benchmark passes (<50ms on 10k entries); overlay snapshot tests pass.

### Phase 6 — Plugins, sessions, theming
US-30 through US-33.
**Verification:** drop-in test reads a real-world `plugins.yaml` and registers entries; session restore round-trip test.

### Phase 7 — AI module
US-34 through US-38.
**Verification:** `make test` green with `MockLLMProvider`; `make test-llm-real` green when keys set.

### Phase 8 — Config, distribution, hardening
US-39 through US-43.
**Verification:** GoReleaser dry-run produces all artifacts; perf harness enforces every NFR target.

## 10. Definition of done

- [ ] All user stories' acceptance criteria pass.
- [ ] All eight phases verified.
- [ ] `make test` (incl. race) green.
- [ ] `make lint` green.
- [ ] `make e2e` (kind-based) green.
- [ ] Performance harness reports all NFRs within budget.
- [ ] GoReleaser produces signed binaries + multi-arch container image.
- [ ] `docs/config.md`, `docs/plugins.md`, `docs/ai.md` written.
- [ ] README has install/quickstart/screenshots.

## 11. Ralph loop command

```bash
/ralph-loop "Implement astrotui per spec at docs/specs/i-want-to-make-a-clone-of-k9s-but-call-it-astronomer-tui-and.md

PHASES:
1. Foundation (US-1..4): scaffold, kubeconfig+exec auth, ResourceCache, generic table view — verify with 'make build && make test && make lint && go run ./cmd/astrotui --context kind-kind'
2. Resource breadth (US-5..11): all k9s-parity renderers + dynamic CRD view — verify with 'make test ./internal/ui/views/...'
3. Write actions + safety (US-12..19): edit/delete/scale/restart/exec/pf with prod-context guard — verify with 'make e2e-actions'
4. Logs deluxe (US-20..23): single/multi/structured/jump+AI button — verify with 'make e2e-logs'
5. Command palette (US-24..29): all four indices + ? overlay — verify with 'make bench-palette && make test ./internal/ui/palette/...'
6. Plugins, sessions, theming (US-30..33): k9s drop-in, state.json, skin engine — verify with 'make test ./internal/plugins/... ./internal/session/... ./internal/ui/theme/...'
7. AI module (US-34..38): provider abstraction + 4 features — verify with 'make test ./internal/ai/...' (and 'make test-llm-real' when keys set)
8. Config, distribution, hardening (US-39..43): config schema, import-k9s, goreleaser, container, perf harness — verify with 'make perf && goreleaser release --snapshot --clean'

VERIFICATION (run after each phase):
- make test
- make lint
- make build

ESCAPE HATCH: After 20 iterations without progress on a phase:
- Document what's blocking under 'Implementation Notes' in the spec
- List approaches attempted and why each failed
- Stop and ask for human guidance

Output <promise>COMPLETE</promise> when all phases pass verification." --max-iterations 60 --completion-promise "COMPLETE"
```

## 12. Open questions

- **Module path placeholder.** `alphabravo-oss` was chosen; confirm at repo creation.
- **Default AI model per provider.** Anthropic: Claude Sonnet 4.6 (fast, cheap, good for this); OpenAI: gpt-4o-mini class; Ollama: `llama3.1:8b` default. Confirm before AI phase.
- **Keybinding scheme — k9s parity vs reimagined.** Default to k9s parity to lower switching cost; the palette is the escape hatch for users who never learned them. Revisit during Phase 5.
- **Snapshot-test format for tcell screens.** `teatest`-style ANSI golden files vs structured cell-grid JSON. Decide in Phase 1.
- **Crash-log location.** Default `~/.local/state/astrotui/crashes/<timestamp>.log`. Confirm.
- **Sigstore keyless signing requires GitHub OIDC.** If the release pipeline runs outside GitHub Actions later, switch to a managed key.

## 13. Implementation notes

*(reserved for Ralph to record blockers, attempted approaches, and decisions taken during implementation)*
