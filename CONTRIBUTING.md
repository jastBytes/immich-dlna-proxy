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

## Project layout

See `CLAUDE.md` for the full architecture rundown (package responsibilities,
DLNA request flow, caching behavior). It's kept up to date and is the best
starting point for understanding how a change should fit in.

## Reporting bugs

Use the issue templates — the "Device / DLNA client compatibility" one in
particular helps a lot, since most bugs in this kind of project are
specific to one TV/client's UPnP quirks.
