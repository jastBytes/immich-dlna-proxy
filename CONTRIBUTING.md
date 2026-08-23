# Contributing

Thanks for considering a contribution. This is a small, single-maintainer
project, so please open an issue to discuss anything non-trivial (new
features, API changes) before writing code — bug fixes and doc
improvements can go straight to a PR.

## Getting set up

Requires Go 1.26.5+ (matches `go.mod` and CI).

```bash
go build ./...   # build
go vet ./...      # static analysis
gofmt -l .        # must print nothing
go test ./... -v -race   # exactly what CI runs
golangci-lint run # lint (CI pins version v2.12.2)
```

To run against a real Immich server:

```bash
export IMMICH_URL=http://192.168.1.10:2283
export IMMICH_API_KEY=your-api-key
go run .
```

Or copy `.env.example` to `.env`, fill in your values, and run `make run`
(`.env` is gitignored, so real credentials are safe to put there). `make ci`
runs the same vet/fmt/test checks CI does; `make help` lists all targets.

## Before opening a PR

- Run the commands above locally — CI runs the same checks and will fail
  on the same things.
- No external dependencies. This is a deliberate constraint (see
  `CLAUDE.md`) — don't add a `go.sum` entry without a strong reason;
  prefer a stdlib solution.
- Update `docs/architecture.md` and/or `docs/configuration.md` if you
  change protocol behavior or add/change an environment variable — they're
  the canonical reference, not just supplementary docs.
- The PR template's checklist mirrors CI; fill it in honestly rather than
  checking boxes you haven't actually verified.

## CI/CD

Several GitHub Actions workflows live under `.github/workflows/`:

- **`ci.yml`** - runs on every push/PR to `main`: `golangci-lint`,
  `gofmt` check, `go vet`, `go test -race`, cross-compile check for
  linux/darwin × amd64/arm64, and a multi-arch Docker build. On PRs
  opened from this repository (not forks - see below), the image is
  pushed to the separate `ghcr.io/jastbytes/immich-dlna-proxy-dev`
  package, tagged `pr-<number>` and `<commit-sha>`. On every push to
  `main`, the image is pushed to the separate
  `ghcr.io/jastbytes/immich-dlna-proxy-preview` package, tagged `latest`,
  `<commit-sha>`, and `<next-release-version>` (the next version is
  resolved from `.github/release-drafter.yml`'s `version-resolver`
  config, same as the draft release). PRs from forks only get a build
  check (not pushed) - the default `GITHUB_TOKEN` is read-only for fork
  PRs, so pushing would fail there regardless.
- **`release.yml`** - runs when you push a tag matching `v*.*.*`
  (e.g. `git tag v1.0.0 && git push --tags`):
  - re-runs the full CI suite first
  - builds `.tar.gz` archives for linux/darwin × amd64/arm64, generates a
    `checksums.txt`, and attaches both to a new GitHub Release
  - builds and pushes a multi-arch Docker image to
    `ghcr.io/jastbytes/immich-dlna-proxy:latest` and `:<version>`
- **`cleanup-images.yml`** - keeps the `-dev`/`-preview` packages from
  piling up in GHCR indefinitely:
  - deletes a PR's image from the `-dev` package as soon as the PR
    closes (merged or not)
  - weekly (and on manual `workflow_dispatch`), prunes the `-preview`
    package down to the 10 newest images, always keeping `latest`
  - `workflow_dispatch` takes a `dry_run` input to log what would be
    deleted without actually deleting anything

Most workflows need no extra setup beyond pushing the repo - they use the
automatically provided `GITHUB_TOKEN` (GHCR push is covered by the
`packages: write` permission declared on the relevant jobs, release
creation by `contents: write` in `release.yml`). **`cleanup-images.yml`
is the exception**: deleting package versions via the default
`GITHUB_TOKEN` additionally requires granting this repository the
**Admin** role under both the `immich-dlna-proxy-dev` and
`immich-dlna-proxy-preview` packages' own Settings -> "Manage Actions
access" on GitHub (one-time per package, done via the web UI -
`packages: write` in the workflow alone isn't sufficient for deletes;
each package only exists once its first image has been pushed, so this
can only be done after the first `-dev`/`-preview` build has run once).

## Project layout

See `CLAUDE.md` for the full architecture rundown (package responsibilities,
DLNA request flow, caching behavior). It's kept up to date and is the best
starting point for understanding how a change should fit in.

## Reporting bugs

Use the issue templates — the "Device / DLNA client compatibility" one in
particular helps a lot, since most bugs in this kind of project are
specific to one TV/client's UPnP quirks.
