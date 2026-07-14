# k9s configuration compatibility

Run this once to copy supported k9s configuration without overwriting existing astrotui files:

```sh
astrotui config import-k9s
```

The command reads `~/.config/k9s/{config.yaml,skin.yaml,hotkeys.yaml,aliases.yaml}` and writes equivalents under `~/.config/astrotui/`. It reports `imported`, `skipped`, or `conflict` for every file. Sources are limited to 1 MiB, destinations use private permissions, destination symlinks are rejected, and conflicts are never overwritten.

## Supported runtime behavior

- `aliases.yaml`: resource aliases and alias-to-alias chains. Targets must resolve to one resource token such as `pods`, `v1/pods`, or `apps/v1/deployments`. Cycles and chains deeper than eight entries are rejected at resolution time.
- `hotkeys.yaml`: one printable ASCII key or `Ctrl-`, `Alt-`, or `Shift-` plus one letter or digit. Commands may open resources, change namespace, or open `:pf`, `:bench`, `:auth`, and `:health`. `override: true` runs before built-in bindings; other hotkeys run afterward.
- `skin.yaml`: k9s body foreground/background, table foreground/background/cursor/header, frame border/focus, and pending/error status colors are translated into the native live theme. Unmapped k9s skin fields are ignored.
- `config.k9s.yaml`: `k9s.readOnly: true` sets the default policy to `read-only`. A native `safety.defaultPolicy` or per-context policy takes precedence.
- `plugins.yaml`: k9s and astrotui plugin files are merged, with the native file taking precedence by plugin name. Scopes, shortcuts, override behavior, argument substitution, confirmation, and background execution are supported. Native plugins can additionally declare a server-preview contract for mutations.

Alias and hotkey files are strict, capped at 500 aliases and 200 hotkeys, and limited to 1 MiB. Invalid input fails startup instead of partially applying configuration. Descriptions shown in the TUI are escaped before reaching markup-aware widgets.

## Current differences

- k9s aliases that include a resource name, filter, or command arguments are not executed; use a single resource target instead.
- k9s custom `views.yaml` table-column expressions are not imported yet. Kubernetes CRD printer columns and astrotui's built-in renderers still work normally.
- Only the skin fields listed above are translated. Native astrotui skins remain available through the compact `name`, `fg`, `bg`, `accent`, `warn`, `error`, `subtle`, and `selection` schema.

These differences keep compatibility explicit and prevent imported configuration from turning cluster-derived values into shell commands. The parity plan remains open until the compatibility corpus covers the current k9s configuration surface.
