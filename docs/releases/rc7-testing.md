# Astro-TUI v0.1.0-rc.7 testing guide

This is a release candidate for user testing, not the stable 0.1.0 release. Plugin expansion remains deferred. The [feature/evidence matrix](../reviews/k9s-v0.50.18-parity.md) records implemented workflows and qualification limits.

## Native installables

| Machine | Release archive |
|---|---|
| Linux Intel/AMD 64-bit | `astrotui_linux_amd64.tar.gz` |
| Linux ARM 64-bit | `astrotui_linux_arm64.tar.gz` |
| macOS Intel | `astrotui_darwin_amd64.tar.gz` |
| macOS Apple Silicon | `astrotui_darwin_arm64.tar.gz` |

The release includes `install.sh`, signed `checksums.txt` (`.sig` and `.pem`), an SPDX JSON SBOM, and this guide. Windows, DEB/RPM, Homebrew and Krew packages are not supported distribution targets.

Install cosign first, then use a user-owned test directory:

```sh
mkdir -p "$HOME/.local/bin"
curl -fsSLo /tmp/astrotui-rc7-install.sh \
  https://github.com/alphabravo-oss/astronomer-tui/releases/download/v0.1.0-rc.7/install.sh
ASTROTUI_VERSION=v0.1.0-rc.7 PREFIX="$HOME/.local/bin" sh /tmp/astrotui-rc7-install.sh
"$HOME/.local/bin/astrotui" --version
"$HOME/.local/bin/astrotui" --readonly --no-restore \
  --config-dir "$HOME/.config/astrotui-rc7-test" --context YOUR_CONTEXT
```

The version must report `0.1.0-rc.7`, with a real commit hash/date. The default `latest` installer does not select this prerelease. The native app requires kubeconfig and any configured exec authentication tools. Helm and Grype are optional separately installed native integrations.

## Containers, both architectures

Published tags:

- `ghcr.io/alphabravo-oss/astrotui:0.1.0-rc.7` (multi-architecture)
- `ghcr.io/alphabravo-oss/astrotui:0.1.0-rc.7-amd64`
- `ghcr.io/alphabravo-oss/astrotui:0.1.0-rc.7-arm64`

The existing GHCR package may require authentication. Public release assets also include `astrotui_container_linux_amd64.tar.gz` and `astrotui_container_linux_arm64.tar.gz`, so container installation does not depend on registry access. These have their own signed `container-checksums.txt` (`.sig` and `.pem`).

Download your architecture's container archive and the three container checksum files from the release, then verify before loading:

```sh
cosign verify-blob \
  --signature container-checksums.txt.sig \
  --certificate container-checksums.txt.pem \
  --certificate-identity https://github.com/alphabravo-oss/astronomer-tui/.github/workflows/release.yml@refs/tags/v0.1.0-rc.7 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  container-checksums.txt
# Linux amd64 example; use arm64 when appropriate.
# On macOS, replace sha256sum with shasum -a 256.
awk '$2 == "astrotui_container_linux_amd64.tar.gz"' container-checksums.txt | sha256sum -c -
gzip -dc astrotui_container_linux_amd64.tar.gz | docker load
docker run --rm ghcr.io/alphabravo-oss/astrotui:0.1.0-rc.7-amd64 --version
```

Images run as nonroot. For an interactive test, mount a readable kubeconfig and writable configuration directory; kubeconfig paths/server addresses must resolve inside the container. The distroless image does not bundle an editor, cloud exec credential commands, Helm or Grype. Use native binaries for those workflows.

## Suggested acceptance pass

- [ ] Confirm version, black background, Astro-TUI ASCII logo and `by AlphaBravo`; resize to 80×24 and back.
- [ ] Start in read-only mode; navigate contexts/namespaces/resources, sort, search, inspect YAML/describe and CRDs.
- [ ] Open Pulses, XRay, containers, references and RBAC; verify missing permissions and unavailable metrics are clear.
- [ ] Test logs, previous/container selection, reconnect, wrapping and export on a disposable workload.
- [ ] Test port-forward readiness/stop and an explicitly confirmed bounded HTTP benchmark against your own endpoint.
- [ ] In a disposable nonproduction context, test confirmed edit/scale/restart/delete, Helm and node-shell cleanup if your policy permits them.
- [ ] Exercise config reload and your real authentication method; note OS, terminal, Kubernetes version and exact steps for any failure.

Known qualification gaps remain: no blanket k9s equivalence claim, startup above the 300 ms target in the recorded run, Intel binary-size misses, and broader native terminal/cloud/operator testing still needed. Stable release promotion waits for user acceptance.
