# astrotui configuration

`astrotui` reads `~/.config/astrotui/config.yaml`. Missing files are accepted and defaults are used.

Use `--config-dir /path/to/directory` to isolate configuration, themes, customizations and session state. A nondefault directory also isolates plugin loading from the default k9s configuration. `--no-restore` skips reading saved state; it does not disable saving state on exit.

```yaml
safety:
  prodPattern: "(?i)prod|production"
ai:
  enabled: false
  provider: openai
  model: gpt-4o-mini
  apiKeyEnv: OPENAI_API_KEY
  endpoint: ""
contexts:
  prod-east:
    policy: confirm-destructive
theme: dark
```

Context policies are `read-only`, `confirm-destructive`, or `full`. Destructive verbs require typed confirmation when the policy is `confirm-destructive`.


Deletion always requires confirmation. Ordinary contexts default to Cancel; protected contexts require the exact context name. Read-only contexts block built-in destructive actions and external plugins. Use Kubernetes RBAC for authorization beyond the client interface.

Session state is written atomically to `~/.config/astrotui/state.json` with private file permissions. `all` records an all-namespace selection; custom resource views preserve group/version/resource. CLI `--context` and `--namespace` take precedence over restored state, and `--no-restore` skips restoration. `--namespace all` selects every namespace.

The main table uses compact columns by default; `w` toggles wide columns for the current run. `/` supports case-insensitive Go regular expressions, `!` inverse matching, `-l` Kubernetes label selectors, `-f` fuzzy subsequences, and `-t` literal substring matching. The pinned k9s v0.50.18 source also uses Go `regexp`; lookarounds and backreferences are unavailable in both.

`config import-k9s` preserves conflicts, copies aliases/hotkeys/views/plugins and translates shared skin colors. It archives k9s global settings as `config.k9s.yaml` and reports that those settings are not active. Core context preferences and context aliases/hotkeys/views/benchmarks are imported. Component-specific skin details and arbitrary k9s extension fields are not translated.

The native configuration directory is watched. Valid edits to `config.yaml`, `aliases.yaml`, `hotkeys.yaml`, `views.yaml` and plugin definitions replace the previous configuration; invalid files leave the last valid settings active and show a status error. Aliases may expand to a resource, namespace and label selector; cycles are rejected. Custom shortcuts cannot override reserved built-in controls.

```yaml
# aliases.yaml
aliases:
  pay: pods payments app=api
```

```yaml
# hotkeys.yaml
hotKeys:
  payments:
    shortCut: Ctrl-Y
    description: Payments pods
    command: pay
```

```yaml
# views.yaml
views:
  v1/pods:
    sortColumn: NAME:asc
    columns:
      - NAME
      - 'APP:.metadata.labels.app'
      - STATUS
      - CPU
      - MEM
      - 'AGE|W'
```

`W` means wide-only, `S` always shown, `H` hidden, `R` right aligned and `L` left aligned; `N` and `T` select numeric/quantity and timestamp sorting. JSONPath supports array access and escaped label keys. Read [implementation details and limits](reviews/implementation-follow-through.md).

Set `NO_COLOR=1` to suppress terminal colors while retaining inverse-video focus and text status indicators. Dark, light and dim themes are also available.

The default `dark` theme uses a true black background with cyan accents, readable gray text and distinct warning/error colors. The main dashboard includes the Astro-TUI ASCII logo with `by AlphaBravo` directly underneath. At widths below 80 columns or heights below 24 rows, it uses a compact wordmark to preserve table space. Branding does not delay startup.

## Context overlays and startup precedence

Core settings accept `readOnly: true` globally. `--readonly` remains enforced after context switches and config reloads. `--command` / `-c` opens a resource or explorer at launch. Explicit context/namespace flags win over saved session state; context namespace preferences apply when neither supplies a namespace. `--no-restore` makes a context's preferred namespace useful for a fresh session.

Context files live under `clusters/<cluster>/<context>/` in the config directory. Each kubeconfig name is URL path-escaped (a slash becomes `%2F`). Context aliases/hotkeys/views override matching global entries; unmatched global entries remain available. Cross-layer alias cycles reject the update. Invalid edits retain the previous valid settings.

```yaml
# clusters/my-cluster/my-context/config.yaml
policy: confirm-destructive
theme: dark
namespace: payments
view: deployments
```

The core k9s context fields `k9s.readOnly`, `k9s.skin`, `k9s.namespace.active` and `k9s.view.active` are also read. Built-in theme names are `dark`, `dim`, `light`. Custom skin files use Astro-TUI's shared-color schema; arbitrary named k9s skins are not automatically resolved. Import preserves source paths; exotic kubeconfig names may require moving the imported directory to its escaped path.

```yaml
# views.yaml, globally or in a context directory
views:
  'v1/pods@payments':
    columns: [NAME, READY, STATUS, 'CPU|N|R', 'MEM|N|R', 'AGE|W']
  'v1/pods@^team-.*$':
    sortColumn: NAME:asc
    columns: [NAME, NAMESPACE, STATUS]
  pay:
    columns: [NAME, STATUS]
```

An invoked alias view wins over its underlying resource view. Within either, an exact namespace wins, followed by matching Go regex patterns in lexical order, then the unqualified view. Omitted base columns remain available in wide mode. Regex matching is unanchored unless `^`/`$` are supplied.

Resource commands accept inline scope/filter arguments, for example `:pods payments @staging /api` or `:pods payments app=api`. A context switch completes before the remaining command is executed.

## HTTP benchmarks

`:bench http://127.0.0.1:8080/health` opens an editable form. Reviewing the budget and confirming the run are separate steps; opening the view sends no traffic. `b` on a ready port-forward opens a benchmark for its loopback endpoint. Read-only mode blocks benchmark traffic.

```yaml
# benchmarks.yaml: global file, or a complete per-context replacement
benchmarks:
  defaults:
    requests: 200
    concurrency: 1
    http:
      method: GET
      path: /health
      header:
        Accept: application/json
  services:
    payments/api:
      requests: 500
      concurrency: 4
  containers:
    payments/api-pod:app:
      http:
        method: POST
        path: /test
        body: '{"fixture":true}'
```

Context `benchmarks.yaml` replaces the global benchmark file. Matching service settings override defaults; a Pod uses its first regular container's key. Form fields can override endpoint/method/budget. `auth.user` and `auth.password` are supported; protect configuration files as credentials. Host defaults apply only when no explicit endpoint is supplied.

Limits: 10,000 requests, 64 concurrent workers, 60 seconds overall, 10-second default request timeout, 1 MiB request/response bodies. TLS verification stays enabled and redirects are not followed. Oversized responses count as errors. Results include counts, status codes, throughput, p50/p90/p95/p99 and error summaries. Reports in `benchmarks/` use private atomic files, omit payloads/headers, and strip endpoint userinfo/query/fragment. Cancellation stops pending work and saves completed measurements; `:be` opens reports.

## Other core workflows

- `:rbac user alice group-a,group-b`, `:rbac sa runner payments`, and `:who-can get pods/log payments api` show visible bindings, rules and provenance. Unknown groups, unreadable bindings, webhooks and other authorizers prevent treating this as a complete authorization decision.
- `:helm` requires `helm` on PATH (validated with 3.21.0). Enter opens history; `v` values, `m` manifest, `r` selected history rollback, Ctrl-D uninstall. The captured kubeconfig/context is used. Secret/ConfigMap Helm storage is supported; SQL storage is not. Lists/history cap at 1,000 entries. Refresh before acting if a release changes.
- `:scan` requires `grype` on PATH. Selection and confirmation precede downloads/scan. Local registry credentials are used; Kubernetes imagePullSecrets are not exported. The scanner's database/network and registry access determine availability. Output caps at 16 MiB and runtime at five minutes.
- `:node-shell` (or `x` on a Node) requires Linux and permission for a privileged host-PID/network helper. Identity is checked before and after preparation; helper deletion uses its UID. Preparation supports Ctrl-C. The helper has a one-hour active deadline and is deleted on exit; interrupted processes or API outages may still require manual cleanup of the named helper.
- `Space` marks up to 100 resources. Ctrl-D deletes marked resources with confirmation; Ctrl-K / `:kill` requests grace-zero deletion with explicit warning. Each target retains its UID. Bulk operations can partially succeed; cancellation cannot undo completed deletions.
- `:save` / Ctrl-S saves a manifest to `screendumps/`; Secret exports require confirmation. `:sd` lists/open/deletes app captures. This is manifest capture, not a raster terminal screenshot. Saved Secrets remain sensitive. `:decode` displays a selected Secret locally.
