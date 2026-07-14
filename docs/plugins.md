# Plugins

`astrotui` reads k9s-compatible plugins from `~/.config/k9s/plugins.yaml` and native plugins from `~/.config/astrotui/plugins.yaml`.

Supported k9s-compatible fields: `description`, `scopes`, `shortCut`, `override`, `command`, `args`, `background`, and `confirm`.

Native plugins may additionally declare a mutation preview contract:

```yaml
plugins:
  delete-pod:
    description: Delete the selected Pod
    scopes: [po]
    command: kubectl
    args: [delete, pod, $NAME, --namespace, $NAMESPACE]
    mutates: true
    previewCommand: kubectl
    previewArgs: [delete, pod, $NAME, --namespace, $NAMESPACE, --dry-run=server, -o, yaml]
```

When `mutates: true`, `astrotui` refuses to run the apply command unless the preview command succeeds, shows at most 64 KiB of preview output, and requires a separate Apply action. Executable names are trusted configuration and are never expanded from cluster-derived values; placeholders are expanded only in argument arrays.

Plugins are indexed by the command palette and can also register shortcuts for matching scopes. `override: true` runs before built-in bindings; other shortcuts run only after built-ins. If multiple in-scope plugins claim the same shortcut, `astrotui` refuses to execute either and displays the conflicting names. Plugin files are limited to 1 MiB and configuration errors stop normal startup.
