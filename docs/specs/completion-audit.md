# astrotui Completion Audit

Objective: read `docs/specs` and build the `astrotui` product out fully as the Kubernetes TUI described in `docs/specs/i-want-to-make-a-clone-of-k9s-but-call-it-astronomer-tui-and.md`.

Audit date: 2026-05-09.

## Success Criteria

- Implement the Go module, CLI, layout, docs, license, lint/test/build targets, release files, and CI described by the spec.
- Implement US-1 through US-43 with the functional requirements FR-1 through FR-14.
- Enforce or verify NFR-1 through NFR-10.
- Satisfy the Definition of Done: race tests, lint, kind e2e, perf harness, signed/multi-arch release pipeline, docs, and README.

## Prompt-To-Artifact Checklist

| Requirement | Evidence | Status |
| --- | --- | --- |
| Spec files read and tracked | `docs/specs/i-want-to-make-a-clone-of-k9s-but-call-it-astronomer-tui-and.md`, `.json`, progress tracker | Done |
| Module path `github.com/alphabravo-oss/astrotui` | `go.mod`; `go list -m` returns the module path | Done |
| Binary name `astrotui` | `cmd/astrotui`, `Makefile`, `.goreleaser.yml`, `install.sh` | Done |
| Required layout including reserved `pkg/` | `cmd/`, `internal/`, `pkg/.gitkeep`, `test/`, `docs/` | Done |
| Apache-2.0 license | `LICENSE` contains Apache License 2.0 | Done |
| README install, quickstart, screenshot | `README.md`, `docs/screenshots/astrotui-main.svg`, distribution tests | Done |
| Lint config with errcheck/staticcheck/gosec/revive | `.golangci.yml`; `make` reports 0 issues | Done |
| Build/test/lint default target | `Makefile`; `make` passed | Done |
| Kubeconfig loading and exec auth registration | `internal/cluster/client.go`; cluster tests; kind e2e kubeconfig test | Done |
| Resource discovery/cache/watch | `internal/cluster/cache.go`; cache tests; envtest watch/discovery tests | Done |
| Generic list view with filter/sort/live updates | `internal/ui/app.go`; UI tests; perf tests | Done |
| Sort indicator | `internal/ui/app.go` appends `▲`/`▼` to the active header | Done |
| k9s built-in renderers | `internal/ui/views/renderers.go`; golden snapshots; envtest fixture coverage guard | Done |
| Dynamic CRD view with printer columns | `views.RendererForCRD`; envtest CRD instance test | Done |
| Prod guard | `internal/safety/guard.go`; safety/action/UI tests; kind prod-guard e2e | Done |
| Edit/delete/scale/restart | `internal/actions`; UI tests; kind e2e action path | Done |
| Exec | `internal/actions/exec.go`; unit tests; kind e2e SPDY command path | Done |
| Port-forward and manager | `internal/actions/portforward.go`; UI/unit tests; kind e2e port-forward start/cancel path | Done |
| Logs single/multi/structured/regex/save | `internal/logs`, `internal/ui/app.go`; log/UI tests; kind logs e2e | Done |
| Log jumps and AI summary hook | UI tests for event/owner/AI log actions | Done |
| Command palette categories and fuzzy search | `internal/palette`; UI/palette tests; benchmark | Done |
| Resource/action/context/namespace/plugin indexers | `internal/palette/indexers.go`; UI/palette/plugin tests | Done |
| Live `?` keybind overlay | `internal/ui/app.go`; UI help tests | Done |
| k9s and native plugin support | `internal/plugins`; `docs/plugins.md`; tests | Done |
| Session restore | `internal/session`; UI/session tests | Done |
| Theme/skin engine and live reload | `internal/ui/theme`; UI/theme tests | Done |
| AI provider interface and providers | `internal/ai/provider.go`; provider tests; `docs/ai.md` | Done |
| AI explain failure/logs/manifest/NL action | `internal/ai/features.go`, `internal/ui/app.go`; UI/AI tests | Done |
| Config strict schema and import-k9s | `internal/config`; `docs/config.md`; config tests | Done |
| GoReleaser binary targets and checksums | `.goreleaser.yml`; `make release-snapshot` produced four archives and checksums | Done |
| Cosign signing config and release workflow | `.goreleaser.yml`, `.github/workflows/release.yml`, distribution tests | Pipeline present; real tag push still external |
| Install script artifact contract | `install.sh`; README; distribution tests | Done |
| Multi-arch container config | `.goreleaser.yml`, `Dockerfile`, release workflow; local GoReleaser Docker snapshot built amd64/arm64 images; distribution tests | Build done; real GHCR publish still external |
| Non-root container | `Dockerfile`; local Docker evidence in progress tracker; distribution tests | Done |
| Perf harness | `test/kind/perf_test.go`; `make perf` passed | Done |
| Live 1,000-pod kind render budget | `test/kind/e2e_test.go`; live kind tests enforce informer sync plus first render <300ms, process-to-render <300ms, and cache-to-render <100ms | Done |
| Process-level idle CPU/RSS | `make nfr-live`; 5-minute live kind run passed with idle CPU <=2% and RSS <=150MiB | Done |
| Kind e2e harness | `test/kind/e2e_test.go`; `ASTROTUI_E2E=1 ASTROTUI_E2E_CONTEXT=kind-kind make e2e` passed against a live kind cluster | Done |
| Coverage NFR | `make coverage-nfr` passed: cluster/actions/logs all above 70% | Done |
| Zero telemetry dependency scan | distribution test scans common telemetry dependency markers | Done |
| Panic recovery | `internal/ui/recovery.go`; recovery tests | Done |

## Verified Commands

- `make`
- `make envtest`
- `make perf`
- `make coverage-nfr`
- `go test -tags=e2e ./test/kind/... -count=1` (compiled locally; skipped when `ASTROTUI_E2E` was unset)
- `ASTROTUI_E2E=1 ASTROTUI_E2E_CONTEXT=kind-kind make e2e` (passed against a live kind cluster)
- `ASTROTUI_E2E=1 ASTROTUI_E2E_CONTEXT=kind-kind go test -tags=e2e ./test/kind/... -run TestKindPerformanceGoldenPath -count=1 -timeout=3m`
- `ASTROTUI_E2E=1 ASTROTUI_E2E_CONTEXT=kind-kind ASTROTUI_E2E_PROCESS_IDLE=5m go test -tags=e2e ./test/kind/... -run TestHeadlessRenderProcessGoldenPath -count=1 -timeout=8m`
- `ASTROTUI_E2E_CONTEXT=kind-kind make nfr-live` (passed against a live kind cluster)
- `PATH="$HOME/go/bin:$PATH" make release-snapshot`
- `PATH="$HOME/go/bin:$PATH" goreleaser release --snapshot --clean --skip=sign,publish`
- `PATH="$HOME/go/bin:$PATH" goreleaser check`

## Remaining External Verification

These items cannot be proven from the current local workspace alone:

- Push a `v*` tag to GitHub and verify the release workflow produces cosign-signed checksums/binaries through GitHub OIDC.
- Verify the release workflow publishes `ghcr.io/alphabravo-oss/astronomer-tui:<version>` as a multi-arch image.

Conclusion: the implementation and local/live-kind harness are complete to the extent this workspace can prove. The project should not be declared fully complete until the external release verification passes.
