# Pod I/O, port-forwards, and benchmarks

## Exec, attach, and copy

Select a Pod and use:

- `x` to exec into a selected container with shell fallback.
- `A` to attach to a selected container.
- `c` to copy one file or directory to or from a selected container.

All three operations run the `pods/exec` or `pods/attach` RBAC preflight before opening a stream and honor the active context policy. A `read-only` context blocks them; a `confirm-destructive` context requires the exact context name.

Copy does not invoke a shell. It sends argument-separated `tar` commands over a non-TTY SPDY exec stream. Uploads and downloads allow at most 512 MiB of uncompressed content and 10,000 entries; tar metadata is independently stream-bounded. Downloads must target an existing local directory and are fully extracted into a private staging directory before no-overwrite installation. Files use an atomic hard-link install and directories reserve the final name exclusively before staged children are moved. Existing destinations are never overwritten. Absolute/traversal paths, symlinks, hard links, devices, sockets, and other special files are rejected on both sides of the local trust boundary.

The selected container must provide `tar`; a missing binary is reported as a copy failure.

## Port-forward lifecycle

Use `f` on a Pod or Service, then enter `local:remote`. Named Service target ports are resolved through a matching Pod. Use `:pf` to list active forwards and `d`, `k`, or `Ctrl+K` to stop one.

Forwards register before their runner starts, remove themselves when the runner exits, and are canceled when the Kubernetes context changes or astrotui exits. A failed context switch leaves existing forwards running because the old context remains active.

## HTTP benchmarks

Open `:pf`, select an active forward, and press `Ctrl+B` or `b`. Configure an HTTP path, request count, concurrency, and timeout. The defaults are 100 requests, concurrency 10, and 30 seconds.

Benchmarks are implemented in-process and are restricted to the literal loopback IP address of the active port-forward. They cannot target hostnames or external hosts, use environment proxies, follow redirects, include URL credentials, exceed 10,000 requests, exceed concurrency 100, or run longer than five minutes. Response bodies are drained only up to a small bound.

Use `:bench` (or `:be`) to view results. Press Enter for status-code and latency details or `k`/`Ctrl+K` to cancel a running benchmark. Runs are also canceled during context changes and application exit.
