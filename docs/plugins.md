# Plugins

Astrotui loads `~/.config/k9s/plugins.yaml`, followed by `~/.config/astrotui/plugins.yaml`. Native entries override entries with the same name. The supported top-level format is `plugins: {name: definition}`. Both directories also support `plugins/*.yaml` files; a file may contain a single plugin definition named after the file.

The palette executes `command` with an argument array from `args`; `description`, `background`, and `confirm` are supported. Foreground commands inherit the terminal while the TUI is suspended. Background children are reaped. An optional confirmation is configured with `confirm: true`.

Variables include `$NAME`, `$NAMESPACE`, `$CONTEXT`, `$KUBECONFIG`, `$CLUSTER`, `$USER`, `$RESOURCE_GROUP`, `$RESOURCE_VERSION`, `$RESOURCE_NAME` (resource plural), and `$FILTER`. Brace syntax is also supported. This is argument substitution, not shell evaluation; use an explicit shell only when your own plugin requires it.

External plugins are disabled in read-only contexts because arbitrary commands cannot be proven read-only. The `confirm-destructive` context policy applies to built-in actions; configure plugin confirmation explicitly.

`scopes` filters palette entries and shortcut availability using resource aliases, qualified resources, `all` or `*`. `shortCut` dispatches the plugin from its matching resource table when it does not conflict with built-in keys. Definitions in the native configuration directory reload automatically. Unsupported plugin fields produce a load error. Per-context files, XDG data-directory overlays and all k9s variables are not implemented. See the [implementation report](reviews/implementation-follow-through.md).
