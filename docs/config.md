# astrotui configuration

`astrotui` reads `~/.config/astrotui/config.yaml`. Missing files are accepted and defaults are used.

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
