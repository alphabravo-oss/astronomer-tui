# Plugins

`astrotui` reads k9s-compatible plugins from `~/.config/k9s/plugins.yaml` and native plugins from `~/.config/astrotui/plugins.yaml`.

Supported fields: `description`, `scopes`, `shortCut`, `command`, `args`, `background`, and `confirm`.

Plugins are indexed by the command palette and can also register shortcuts for matching scopes.
