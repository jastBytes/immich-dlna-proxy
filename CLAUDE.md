# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single Go binary, no external dependencies (standard library only),
that exposes an Immich server's albums and named people as a DLNA
MediaServer so Smart TVs can browse and view the photos and videos in
them. Assets of `type == "IMAGE"` or `type == "VIDEO"` are handled; any
other type (e.g. audio) is skipped.

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
lints and runs gofmt check / `go vet` / `go test -race` on every PR; the
cross-compile (linux/darwin × amd64/arm64) and Dockerfile build check only
run on pushes to `main` and during release verification, to keep PR runs
fast — reproduce all of it locally before pushing rather than relying on
CI to catch them.

Pushing a `v*.*.*` tag triggers `.github/workflows/release.yml`, which
re-runs CI, builds release archives, and publishes a multi-arch image to
`ghcr.io/jastbytes/immich-dlna-proxy`. Not something to do casually.

## Architecture

`main.version` (set via `-ldflags -X`, see `release.yml` and the
Dockerfile's `VERSION` build arg; falls back to Go's VCS stamp, then
`dev`) becomes `dlna.Version`, reported in SERVER headers and the device
description's `modelNumber`.

Data flow: `main.go` wires together `config` → `immich` client → `cache`
→ `dlna` server, then runs the HTTP server and the SSDP responder
concurrently until SIGINT/SIGTERM, which triggers a graceful shutdown
(SSDP `ssdp:byebye`, then `http.Server.Shutdown` with a 5s grace period). Each package has one job:

| Package | Responsibility |
|---|---|
| `config/` | Reads and validates env vars at startup (`config.Load()`); fails fast with a clear message rather than falling back to silent defaults for malformed values (e.g. non-numeric `CACHE_MAX_MB`). |
| `immich/` | Thin REST client for the Immich API (`client.go`) and the JSON shapes it expects (`types.go`) — `GET /api/albums`, `/api/albums/{id}`, `/api/people`, `/api/people/{id}`, `/api/people/{id}/thumbnail`, `/api/assets/{id}`, `/api/assets/{id}/original`, `/api/assets/{id}/thumbnail`, `/api/users/me`, `POST /api/search/metadata`. JSON calls share `doJSON`/`getJSON`; non-200 answers are `*StatusError` (`IsNotFound` for 404/400). |
| `cache/` | LRU disk cache for the bytes behind `/media/{id}`, `/media/person/{id}` (key `person:<id>`) and `/thumbnail/{id}` (key `thumb:<id>`). `Get`/`Put` return open `*os.File`s so a concurrent eviction can't break a hit. Not used for listings (see `dlna/listcache.go`). |
| `imageproc/` | Box-filter downscaling for `MAX_RESOLUTION`, JPEG/PNG only; a no-op passthrough for video and other formats. |
| `dlna/` | Everything protocol-facing: SSDP discovery, UPnP description XML, ContentDirectory/ConnectionManager/X_MS_MediaReceiverRegistrar SOAP actions, DIDL-Lite XML building, and the `/media/{id}` / `/thumbnail/{id}` HTTP handlers. |

A DLNA client talks to the proxy in three stages, each owned by a
different file in `dlna/`:

1. **Discovery (SSDP, UDP 1900)** — `ssdp.go`. Answers `M-SEARCH` and
   sends periodic `NOTIFY ssdp:alive`. Every advertised NT/ST (root
   device, UUID, device type, and each service type) is answered
   individually via `searchTargets` — a control point that searches for
   a specific service type rather than the device type must still find
   the server — and `ssdp:all` gets one reply per target. On shutdown
   (context cancelled) it sends `ssdp:byebye`. The announced `LOCATION`
   IP comes from `advertiseIP`: `ADVERTISE_IP`, else `SSDP_INTERFACE`'s
   IPv4 address, else whatever local IP the OS would pick to reach the
   internet (`detectLocalIP`), else the first non-loopback address.
   Docker needs `--network host` — SSDP multicast doesn't traverse the
   default bridge network.
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
| `0` (root) | — | containers `albums`, `people`, `timeline`, then the `EXTRA_FOLDERS` ones |
| `albums` | `GET /api/albums` | one container per album |
| `album:<id>` | `GET /api/albums/{id}` | one item per photo/video (filtered to `IMAGE`/`VIDEO`) |
| `people` | `GET /api/people` | one container per *named* person (unnamed face clusters skipped) |
| `person:<id>` | `GET /api/people/{id}/assets` | one item per photo/video (filtered to `IMAGE`/`VIDEO`) |
| `timeline` | `POST /api/search/metadata` (`order: desc`) | every photo/video, newest first — or, with `TIMELINE_GROUPING=year`/`month`, one container per year (`timeline:<YYYY>`, plus `timeline:unknown`) |
| `timeline:<YYYY>` / `timeline:<YYYY-MM>` | (same listing, grouped in memory — `timeline.go`) | that year's items, or (month mode) its month containers / that month's items |
| `favorites` (optional) | `POST /api/search/metadata` (`isFavorite`) | favorite items |
| `onthisday`, `random` (optional) | (timeline listing) | today's date in earlier years (`TZ`; tzdata embedded) / 100 random items, seeded per hour so paging is stable |
| `places`, `place:<country>[:<city>]` (optional) | (timeline listing, `exifInfo.country`/`city`) | countries → cities → items; names query-escaped in IDs |
| `asset:<id>` | `GET /api/assets/{id}` | `<res>` points at `/media/{assetID}` |

The optional folders live in `extrafolders.go` (`EXTRA_FOLDERS`, default
`favorites,onthisday`). All search requests set `withExif: true`
(`mergePage`) — without it Immich omits `exifInfo` (size, resolution,
place). `SystemUpdateID` (`updateid.go`) starts at 1 and is bumped when a
freshly fetched album/people listing's fingerprint changes;
`GetSystemUpdateID` re-lists (via the listing cache) so polls see
changes.

With multiple `IMMICH_API_KEYS`, the root instead lists one `user:<idx>`
container per account, and every ObjectID above is prefixed
`user:<idx>:` (`browseMultiUser`); `/media/`, `/thumbnail/` URLs gain an
`{idx}/` segment. Browse failures are SOAP faults (`dlna/soap.go`): `701
No such object` for unknown/invalid ObjectIDs or Immich 404s, `501 Action
Failed` otherwise — `browseUserScope` returns errors, `handleBrowse`
writes the fault.

`buildAssetItem` (`contentdirectory.go`, via `buildItem(itemSpec)` in
`didl.go`) picks each item's `<upnp:class>`
(`object.item.imageItem.photo` vs `object.item.videoItem.movie`) and
`<upnp:albumArtURI>` based on `Asset.IsVideo()`: a photo uses its own
`/media/{id}` URL as its own thumbnail, but a video's bytes can't double
as an image preview, so its albumArtURI instead points at
`/thumbnail/{id}` — without *some* albumArtURI, media browsers (e.g. Home
Assistant) list titles but show a placeholder icon instead of a
thumbnail.

Every `<res>` carries DLNA flags in `protocolInfo` (`dlnaFeatures`:
`DLNA.ORG_OP=01` for byte seek, `CI`, `FLAGS`, no `PN`), mirrored as the
`contentFeatures.dlna.org`/`transferMode.dlna.org` headers on media
responses (`setMediaHeaders`); video items also get `duration` and
`resolution`. `PHOTO_SOURCE` (`photosource.go`, default `auto`) decides
per photo whether `/media/` serves the original or Immich's JPEG preview
(`auto`: preview for anything not JPEG/PNG, e.g. HEIC); `VIDEO_SOURCE`
(`videosource.go`, default `original`) whether videos are the original
or Immich's `/api/assets/{id}/video/playback` (`transcoded`). Both are
decided in Browse (item advertised as `image/jpeg` / `video/mp4`,
`CI=1`, no size) and in `handleMedia` (`mediaVariantFor`, one `GetAsset`
lookup), which must agree. `size` is also omitted for JPEG/PNG when
`MAX_RESOLUTION` is set (served bytes may be smaller).

`album:<id>` and `person:<id>` **containers** carry `<upnp:albumArtURI>`
too, when a cover is available: albums reuse Immich's
`albumThumbnailAssetId` (a regular asset, so `/media/{id}` works as-is);
people don't have an asset-backed thumbnail, so their cover comes from
`GET /api/people/{id}/thumbnail` via `Client.GetPersonThumbnail`, fronted
at `GET /media/person/{personID}` and cached under `"person:<id>"` to
avoid colliding with the asset cache. Both are omitted (no
`albumArtURI` element) when Immich has no cover to offer.

Browse listings go through `cachedClient` (`listcache.go`), which reuses
Immich listing responses for `LISTING_CACHE_SECONDS` (default 30; `0` =
always live) and shares concurrent loads; errors are never cached. Cached
slices are shared — clone before sorting in place.

**Media streaming** (`server.go`, `Server.serveMedia`, parameterized by a
`mediaSource`): on cache hit, serves straight from `CACHE_DIR` via
`http.ServeContent` (handles `Range`/`ETag` for free), no Immich call.
Concurrent misses for the same key share one download (`joinFlight`).
On a miss, a photo is downloaded in full, buffered (needed for
orientation/resize), written to disk (temp file + `os.Rename`) and served
from there. A video (`isVideoMimeType`) is instead proxied through with
the client's `Range` forwarded (`proxyRange` → `Client.OpenOriginalRange`)
so playback starts immediately, while the full download already started
continues detached in `fillCacheInBackground` to fill the cache (so the
first playback downloads twice). With `DISABLE_CACHE=true`, photos are
served from memory and videos are proxied the same way, minus the fill.
Immich 404 → `404`, other failures → `502`.

`GET /media/person/{personID}` (key `person:<id>`, via
`Client.GetPersonThumbnail`) and `GET /thumbnail/{assetID}` (key
`thumb:<id>`, Immich's `/api/assets/{id}/thumbnail?size=preview`, used for
video items' `albumArtURI`) share `serveMedia`, without the
orientation/resize transform and without `openRange` (never video).

Binary downloads from Immich (`DownloadOriginal`, `OpenOriginalRange`,
`GetAssetThumbnail`, `GetPersonThumbnail`) use `immich.Client.Stream`,
which has no overall timeout (that would cut off large videos
mid-transfer) - only a 30s response-header timeout plus a 30s per-read
stall timeout - and follow the inbound request's context, so a DLNA
client hanging up cancels the Immich download (except a video's
background cache fill, which is deliberately detached). Every asset/album/person ID taken from a URL or Browse
ObjectID must pass `immich.ValidID` before reaching the Immich client or
the cache (otherwise `404`) - see "ID validation" in
`docs/architecture.md`.

On a cache miss, `serveMedia` acquires a slot from `Server.fetchSem` (a
buffered channel sized by `MEDIA_FETCH_CONCURRENCY`, default 4) before
calling Immich, so a TV rapidly scrolling through a large album can't
fire off unbounded concurrent downloads; a request that can't get a slot
within 30s (`fetchQueueTimeout`) gives up with `503` instead of queuing
indefinitely. Cache hits never touch this queue, and the slot is released
once the bytes are in (photos) or as soon as a video is identified.

**HTTP server**: `Server.NewHTTPServer` sets `ReadHeaderTimeout`/
`IdleTimeout` but deliberately no `WriteTimeout` (would cut off video
streams). SOAP bodies are capped at 64 KB (`readSOAPBody`). Request
logging (`dlna/log.go`) always logs protocol requests but `/media/`,
`/thumbnail/` and `/healthz` only with `DEBUG=true` (`debugf`).
`GET /healthz` is 200 when Immich answers `/api/server/ping`, else 503;
the Dockerfile's `HEALTHCHECK` runs `immich-dlna-proxy healthcheck`
(`main.go`), which requests it on `LISTEN_ADDR`'s port.

**Cache** (`cache/cache.go`): each entry is two files — `<key>`
(bytes) and `<key>.type` (MIME sidecar). "Last used" = file mtime,
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
- Unsupported inputs (asset types other than `IMAGE`/`VIDEO`, unnamed
  people, non-JPEG/PNG for downscaling) are deliberately
  skipped/passed-through rather than erroring — match that pattern
  rather than introducing hard failures for known-unsupported cases.
- Keep `docs/architecture.md` and `docs/configuration.md` in sync with
  behavior changes — they're the canonical detailed reference, not just
  supplementary.
