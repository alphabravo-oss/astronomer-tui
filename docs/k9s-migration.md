# Migrating from k9s

astrotui adopts k9s muscle memory wherever it doesn't collide with our own
differentiators. This page maps k9s keys and commands to astrotui and lists
the intentional divergences.

## Keys (resource table)

| Action | k9s | astrotui |
| --- | --- | --- |
| Drill down | `Enter` | `Enter` (contextual: workload→pods, node→pods, pod→containers, ns→switch, else describe) |
| Describe | `d` | `d` (YAML + events) |
| View YAML | `y` | `y` |
| Edit | `e` | `e` |
| Delete | `Ctrl-D` | `Ctrl-D` (bulk-aware) |
| Kill (grace 0) | `Ctrl-K` | `Ctrl-K` (bulk-aware) |
| Logs | `l` | `l` |
| Shell / exec | `s` (k9s) | `x` (astrotui — `s` is scale/decode) |
| Attach | `a` | `a` |
| Port-forward | `f` (shift-f) | `f` |
| Scale | `s` | `s` (also decodes Secrets) |
| Restart | — | `r` (deploy/sts/ds) |
| Trigger cronjob | `t` | `t` |
| Suspend cronjob | — | `z` |
| Cordon / Uncordon | `c` / `u` | `c` / `u` |
| Drain | `r` (k9s) | `Shift-D` |
| Mark row | `Space` | `Space` |
| Mark all | `Ctrl-A` | `Ctrl-A` |
| Clear marks | `Ctrl-\` | `Ctrl-\` |
| Filter | `/` | `/` (see filter syntax) |
| Command | `:` | `:` |
| All namespaces | `0` | `0` |
| Namespace favorites | `1`–`9` | `1`–`9` |
| Sort by column | `Shift-<col>` | `Shift-N/A/S/R/C/M/P` or `Shift-1..9` |
| Help | `?` | `?` |
| AI explain | — | `Ctrl-E` |
| Command palette | — | `Ctrl-P` |

## Keys (logs view)

| Action | astrotui |
| --- | --- |
| Filter (raw / `key=val` / `key~=re`) | `/` |
| Regex search + next/prev | `Ctrl-R`, `n`, `N` |
| Previous container | `p` |
| Timestamps | `t` |
| Since 1m/5m/15m/1h/all | `1` `2` `3` `4` `0` |
| Autoscroll toggle | `Ctrl-S` |
| Wrap | `w` |
| Pretty JSON/logfmt | `j` |
| Clear buffer | `C` |
| Save to file | `s` |
| Jump to events / owner / AI | `g e` / `g o` / `g a` |
| AI chat about logs | `Ctrl-E` |

## Commands

| k9s | astrotui |
| --- | --- |
| `:<resource>` | `:<resource>` (aliases, shortnames, CRDs via discovery, autocomplete + history) |
| `:ns <name>` | `:ns <name>`, `:ns all`, `:all` |
| `:ctx` | `:ctx` (contexts view), `:ctx <name>` (switch) |
| `:xray <kind>` | `:xray deploy\|sts\|ds` (dependency tree) |
| `:pulses` | `:pulses` (health tiles) |
| `:rbac` | `:rbac u:\|g:\|s:ns/sa`; `:can <verb> <res>` |
| `:alias` | `:alias` (aliases + commands) |
| `:q` | `:q`, `:quit` |
| port-forwards | `:pf` |
| — | `:flash` (recent status messages) |

Custom aliases: drop an `aliases.yaml` in `~/.config/astrotui/` (or reuse your
k9s one) mapping `alias: group/version/resource`.

## Filter syntax (`/`)

- `text` — case-insensitive substring (default)
- `re:PATTERN` — regular expression
- `!EXPR` — invert any text match
- `-f TERM` — fuzzy subsequence
- `-l SELECTOR` — label selector (e.g. `-l app=api,tier!=db`)

## Intentional divergences

- **`x` for exec, not `s`.** `s` is scale (workloads) and Secret decode; exec
  lives on `x`. Attach is `a`.
- **`Shift-D` for drain.** k9s overloads `r`; astrotui keeps `r` for rollout
  restart and puts drain on `Shift-D` with a mandatory typed confirmation.
- **Pretty logs on `j`, autoscroll on `Ctrl-S`.** k9s uses different bindings;
  `w` wrap is unchanged.

## Not yet implemented (roadmap)

Popeye scan, Helm releases, native `hotkeys.yaml` key binding, `views.yaml`
custom columns, port-forward manager status column, AI chat view (`:ai`).
Benchmarking (`hey`) is intentionally out of scope — use a `plugins.yaml`
entry instead. (Pulses, XRay, RBAC/can-i, `aliases.yaml`, wide-mode toggle,
screendump, and command autocomplete/history are implemented.)
