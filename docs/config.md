# astrotui configuration

`astrotui` reads `~/.config/astrotui/config.yaml`. Missing files are accepted and defaults are used.

```yaml
safety:
  prodPattern: "(?i)prod|production"
  defaultPolicy: confirm-destructive
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

Context policies are `read-only`, `confirm-destructive`, or `full`. A context-specific policy takes precedence over `safety.defaultPolicy`; otherwise contexts matching `prodPattern` use `confirm-destructive`. Destructive verbs require typed confirmation when that policy is active.

`astrotui config import-k9s` copies k9s `config.yaml`, `skin.yaml`, `hotkeys.yaml`, and `aliases.yaml` into the private `~/.config/astrotui` directory without overwriting conflicts. Imported files are limited to 1 MiB each. At runtime, `k9s.readOnly: true` becomes the default read-only safety policy unless native configuration explicitly selects another default.

The aliases and hotkeys loaders are strict and bounded. Invalid files stop startup with a configuration error. See [k9s compatibility](k9s-compatibility.md) for the supported schema and current limits.
