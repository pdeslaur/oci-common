# oci-common

Shared GitHub Actions workflows and tooling for the melange + apko image
repositories:

- [oci-caddy](https://github.com/pdeslaur/oci-caddy)
- [oci-anki-sync](https://github.com/pdeslaur/oci-anki-sync)
- [oci-dns01proxy](https://github.com/pdeslaur/oci-dns01proxy)

## Workflows

### `build.yaml`

Builds `melange.yaml` for `x86_64` and `aarch64`, runs the package's `test:`
pipeline, and, when `publish: true`, publishes `apko.yaml` to
`ghcr.io/<repository>` tagged `latest` and `<package version>` and signs it
with [cosign](https://github.com/sigstore/cosign) keyless signing. Scheduled
runs bypass the package cache so that Wolfi toolchain fixes are picked up.

```yaml
jobs:
    build:
        uses: pdeslaur/oci-common/.github/workflows/build.yaml@main
        with:
            publish: ${{ github.event_name != 'pull_request' }}
```

The caller must grant `contents: read`, `id-token: write` and `packages: write`.

Images are signed by this reusable workflow, so verify them with:

```sh
cosign verify ghcr.io/pdeslaur/oci-caddy:latest \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-identity https://github.com/pdeslaur/oci-common/.github/workflows/build.yaml@refs/heads/main \
    --certificate-github-workflow-repository pdeslaur/oci-caddy
```

### `update.yaml`

Runs [`melange-update`](./cmd/melange-update) to bump `melange.yaml` and the Go
modules it pins to their latest releases, then opens a pull request on
`update/<package>` with auto-merge enabled. While that pull request is open,
later runs update it in place.

```yaml
jobs:
    update:
        uses: pdeslaur/oci-common/.github/workflows/update.yaml@main
```

The caller must grant `contents: read` and `id-token: write`, and the repository
needs:

- The [octo-sts](https://github.com/apps/octo-sts) app installed.
- A `.github/chainguard/update.sts.yaml` trust policy granting
  `contents: write` and `pull_requests: write` to this workflow.
- Auto-merge enabled and a ruleset requiring the build checks, so that
  auto-merge waits for them.

## `melange-update`

Reads the upstream repository and tag format from the `git-checkout` step of a
melange config, and if the latest GitHub release is newer, updates
`package.version` and `expected-commit`.

It also bumps Go modules pinned in pipeline steps with `go get MODULE@VERSION`,
such as Caddy plugins, to the latest release reported by the Go module proxy.
This stays within the module's major version and skips prereleases.

`package.epoch` is reset to 0 when the package version changes, and incremented
when only Go modules change.

```sh
GH_TOKEN=$(gh auth token) MELANGE_CONFIG=path/to/melange.yaml go run ./cmd/melange-update
```
