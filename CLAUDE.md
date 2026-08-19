# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single Go binary, no external dependencies (standard library only),
that exposes an Immich server's albums and named people as a DLNA
MediaServer so Smart TVs can browse and view the photos. Only
`type == "IMAGE"` assets are handled — video is out of scope for now.

## Commands

```bash
go build ./...                       # build
go vet ./...                         # static analysis (part of CI)
gofmt -l .                           # must print nothing (CI fails otherwise)
gofmt -w .                           # fix formatting
go test ./...                        # run all tests
go test ./... -v -race               # exactly what CI runs
go test ./dlna/... -run TestBrowse   # run a single test / package
golangci-lint run                    # lint (CI pins version v2.12.2)

# Run locally against a real Immich server
export IMMICH_URL=http://192.168.1.10:2283
export IMMICH_API_KEY=your-api-key
go run .
```

Requires Go 1.26.5+ (matches `go.mod` and CI). CI (`.github/workflows/ci.yml`)
runs gofmt check, `go vet`, `go test -race`, cross-compiles for
linux/darwin × amd64/arm64, and does a Dockerfile build check — reproduce
these locally before pushing rather than relying on CI to catch them.

Pushing a `v*.*.*` tag triggers `.github/workflows/release.yml`, which
re-runs CI, builds release archives, and publishes a multi-arch image to
`ghcr.io/jastBytes/immich-dlna-proxy`. Not something to do casually.

## Architecture

Data flow: `main.go` wires together `config` → `immich` client → `cache`
→ `dlna` server, then runs the HTTP server and the SSDP responder
concurrently. Each package has one job:

| Package | Responsibility |
|---|---|
| `config/` | Reads and validates env vars at startup (`config.Load()`); fails fast with a clear message rather than falling back to silent defaults for malformed values (e.g. non-numeric `CACHE_MAX_MB`). |
| `immich/` | Thin REST client for the Immich API (`client.go`) and the JSON shapes it expects (`types.go`) — `GET /api/albums`, `/api/albums/{id}`, `/api/people`, `/api/people/{id}/assets`, `/api/assets/{id}/original`. |
| `cache/` | LRU disk cache for original photo bytes, keyed by asset ID. Not used for album/asset/people *listings* — only for `/media/{id}` bytes. |
| `imageproc/` | Box-filter downscaling for `MAX_RESOLUTION`, JPEG/PNG only. |
| `dlna/` | Everything protocol-facing: SSDP discovery, UPnP description XML, ContentDirectory/ConnectionManager/X_MS_MediaReceiverRegistrar SOAP actions, DIDL-Lite XML building, and the `/media/{id}` HTTP handler. |

A DLNA client talks to the proxy in three stages, each owned by a
different file in `dlna/`:

1. **Discovery (SSDP, UDP 1900)** — `ssdp.go`. Answers `M-SEARCH` and
   sends periodic `NOTIFY ssdp:alive`. Every advertised NT/ST (root
   device, UUID, device type, and each service type) is answered
   individually via `searchTargets` — a control point that searches for
   a specific service type rather than the device type must still find
   the server — and `ssdp:all` gets one reply per target. The announced
   `LOCATION` uses whatever local IP the OS would pick to reach the
   internet (`detectLocalIP`), which is why Docker needs
   `--network host` — SSDP multicast doesn't traverse the default bridge
   network.
2. **Description (HTTP GET)** — `description.go` serves
   `/description.xml`, `/ContentDirectory.xml`, `/ConnectionManager.xml`,
   `/X_MS_MediaReceiverRegistrar.xml`.
3. **Control (SOAP-over-HTTP)** — `contentdirectory.go` handles `Browse`
   actions at `/ctl/ContentDirectory`; `connectionmanager.go` is a
   required-by-spec no-op-ish service some TVs (Samsung) insist on
   seeing; `mediareceiverregistrar.go` implements the Microsoft
   X_MS_MediaReceiverRegistrar extension (`IsAuthorized`/`IsValidated`/
   `RegisterDevice`, always answering "authorized") that Xbox, Windows
   Media Player, and some Samsung firmwares require to be present or
   they silently treat the server as having no content. `didl.go` builds
   the DIDL-Lite XML returned by `Browse`.

`Browse` maps DLNA `ObjectID`s to Immich concepts:

| ObjectID | Immich call | Returns |
|---|---|---|
| `0` (root) | — | containers `albums`, `people` |
| `albums` | `GET /api/albums` | one container per album |
| `album:<id>` | `GET /api/albums/{id}` | one item per photo (filtered to `IMAGE`) |
| `people` | `GET /api/people` | one container per *named* person (unnamed face clusters skipped) |
| `person:<id>` | `GET /api/people/{id}/assets` | one item per photo (filtered to `IMAGE`) |
| `asset:<id>` | — | `<res>` points at `/media/{assetID}` |

Every photo `<item>` (from `album:<id>`, `person:<id>`, or `asset:<id>`)
also carries an `<upnp:albumArtURI>` pointing at the same `/media/{id}`
URL — without it, some media browsers (e.g. Home Assistant) list titles
but show a placeholder icon instead of a thumbnail.

Every `Browse` call hits Immich live; listings are never cached, only
the image bytes behind `/media/{id}`.

**Media streaming** (`server.go`, `GET /media/{assetID}`): on cache hit,
serves straight from `CACHE_DIR` via `http.ServeContent` (handles
`Range`/`ETag` for free), no Immich call. On cache miss, downloads the
*full* original from Immich regardless of any inbound `Range` header,
writes it to disk (temp file + `os.Rename` for atomicity), then serves
it. With `DISABLE_CACHE=true`, every request proxies live from Immich
instead, forwarding `Range` as-is.

**Cache** (`cache/cache.go`): each asset is two files — `<assetID>`
(bytes) and `<assetID>.type` (MIME sidecar). "Last used" = file mtime,
touched on every hit via `os.Chtimes`. After each write, a background
sweep evicts oldest-mtime files first once total size exceeds
`CACHE_MAX_MB`.

**Downscaling** (`imageproc/resize.go`): only triggers when
`MAX_RESOLUTION` is set and the format is JPEG or PNG (the only formats
the stdlib can decode *and* re-encode); other formats pass through
untouched. `image.DecodeConfig` is used first to cheaply check
dimensions before doing a full decode. Downscaling + `DISABLE_CACHE`
together means every request buffers the whole image in memory (no
Range-passthrough fast path in that combination).

For more detail than needed for a typical change, see `docs/architecture.md`
(protocol internals) and `docs/configuration.md` (env vars, Docker/Unraid
deployment).

## Conventions specific to this repo

- No external dependencies — this is a deliberate constraint, not an
  oversight. Don't add a `go.sum` entry without a strong reason; prefer
  stdlib solutions (this is why downscaling is a hand-rolled box filter
  and the Immich client isn't built on a generated SDK).
- Config validation belongs in `config.Load()` — fail startup with a
  clear error rather than defaulting silently on malformed input.
- Env-var-driven configuration only; there is no config file format to
  maintain.
- Unsupported inputs (video assets, unnamed people, non-JPEG/PNG for
  downscaling) are deliberately skipped/passed-through rather than
  erroring — match that pattern rather than introducing hard failures
  for known-unsupported cases.
- Keep `docs/architecture.md` and `docs/configuration.md` in sync with
  behavior changes — they're the canonical detailed reference, not just
  supplementary.
