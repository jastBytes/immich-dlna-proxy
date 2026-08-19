# immich-dlna-proxy

[![CI](https://github.com/jastBytes/immich-dlna-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/jastBytes/immich-dlna-proxy/actions/workflows/ci.yml)

Exposes your Immich albums and named people (photos only, for now) as a
DLNA MediaServer so older Smart TVs / DLNA clients can browse and display
them without any extra app.

Written in Go, no external dependencies — just the standard library.

📖 Detailed docs: [Architecture](docs/architecture.md) · [Configuration](docs/configuration.md)

## How it works

- **SSDP** (UDP 1900): announces the server on the LAN and answers
  `M-SEARCH` requests (for the device itself and each individual service
  it advertises) so TVs find it automatically.
- **ContentDirectory (SOAP over HTTP)**: on `Browse`, calls the Immich API
  (`GET /api/albums`, `GET /api/albums/{id}`, `GET /api/people`,
  `GET /api/people/{id}/assets`) and maps the root to two folders,
  "Albums" and "People", each album/person to a DLNA *container*
  (folder), and each photo asset to a DLNA *item*.
- **X_MS_MediaReceiverRegistrar**: a Microsoft-defined UPnP extension some
  clients (Xbox, Windows Media Player, some Samsung firmwares) require to
  be present before they'll browse a server's content at all - the proxy
  advertises it and always answers "authorized".
- **Media streaming (HTTP)**: `/media/{assetID}` serves photo bytes to the
  TV. On a cache miss, it downloads the full original from Immich's
  `/api/assets/{id}/original`, writes it to a disk cache, then serves it
  from there (with proper `Range`/`ETag` support via `http.ServeContent`).
  On a cache hit, it's served straight from disk - no Immich call at all.

## Caching

Original photo bytes are cached on disk under `CACHE_DIR` (default
`/config/cache`) so repeated views of the same photo don't hit Immich
again. Album/asset/people *listings* (`Browse`) are **not** cached yet -
only the
image bytes behind `/media/{assetID}`.

- Cache key is the asset ID; stored as `<assetID>` (bytes) +
  `<assetID>.type` (MIME type sidecar).
- "Last used" is approximated by the file's mtime, touched on every hit.
- Once the cache exceeds `CACHE_MAX_MB`, a background sweep deletes the
  least-recently-used files first until back under budget.
- Set `DISABLE_CACHE=true` to fall back to the old behavior (always proxy
  live from Immich, nothing written to disk).

Only assets with `type == "IMAGE"` are shown; videos are skipped entirely
for this first version.

## Configuration (environment variables)

| Variable         | Required | Default          | Description                                   |
|-------------------|:--------:|------------------|------------------------------------------------|
| `IMMICH_URL`       | yes      | –                | Base URL of your Immich server, e.g. `http://192.168.1.10:2283` |
| `IMMICH_API_KEY`   | yes      | –                | Immich API key (needs album.read / asset.read / asset.download / person.read) |
| `LISTEN_ADDR`      | no       | `:8200`          | HTTP bind address/port                         |
| `FRIENDLY_NAME`    | no       | `Immich Photos`  | Name shown on TVs when browsing servers        |
| `DEVICE_UUID`      | no       | fixed default    | Set your own stable UUID if running >1 instance |
| `SSDP_INTERFACE`   | no       | all interfaces   | Restrict SSDP to one NIC name, e.g. `eth0`     |
| `CACHE_DIR`        | no       | `/config/cache`  | Where cached photo bytes are stored             |
| `CACHE_MAX_MB`     | no       | `2048`           | Soft size budget in MB before LRU eviction kicks in |
| `DISABLE_CACHE`    | no       | `false`          | Set to `true` to disable caching entirely       |
| `MAX_RESOLUTION`   | no       | (unset = disabled) | Downscale photos larger than this to fit, e.g. `1920x1080`. Aspect ratio is preserved; smaller images are left untouched. |

## Run locally

```bash
export IMMICH_URL=http://192.168.1.10:2283
export IMMICH_API_KEY=your-api-key
go run .
```

## Run in Docker (e.g. on your Unraid server, alongside Immich)

### Option A: pull the published image (after you've pushed a release tag)

Once you push a `v*.*.*` tag, the release workflow builds and publishes a
multi-arch (amd64 + arm64) image to GitHub Container Registry:

```bash
docker run -d \
  --name immich-dlna-proxy \
  --network host \
  -v /mnt/user/appdata/immich-dlna-proxy/cache:/config/cache \
  -e IMMICH_URL=http://192.168.1.10:2283 \
  -e IMMICH_API_KEY=your-api-key \
  -e FRIENDLY_NAME="Wohnzimmer Fotos" \
  ghcr.io/jastBytes/immich-dlna-proxy:latest
```

By default, GHCR packages are private to your account/org. Make the
package public (or set up a pull secret) so `docker run` on your Unraid
box doesn't need authentication - see the package's Settings page on
GitHub after the first successful release.

### Option B: build locally

```bash
docker build -t immich-dlna-proxy .
docker run -d \
  --name immich-dlna-proxy \
  --network host \
  -v /mnt/user/appdata/immich-dlna-proxy/cache:/config/cache \
  -e IMMICH_URL=http://192.168.1.10:2283 \
  -e IMMICH_API_KEY=your-api-key \
  -e FRIENDLY_NAME="Wohnzimmer Fotos" \
  immich-dlna-proxy
```

`--network host` is strongly recommended (and on Unraid, easy to set via
the "Host" network type in the container settings): SSDP relies on UDP
multicast, which generally does not traverse Docker's default bridge
network cleanly. Without host networking, TVs likely won't auto-discover
the server.

## CI/CD

Two GitHub Actions workflows live under `.github/workflows/`:

- **`ci.yml`** - runs on every push/PR to `main`: `golangci-lint`,
  `gofmt` check, `go vet`, `go test -race`, cross-compile check for
  linux/darwin × amd64/arm64, and a Dockerfile build check (not pushed).
- **`release.yml`** - runs when you push a tag matching `v*.*.*`
  (e.g. `git tag v1.0.0 && git push --tags`):
  - re-runs the full CI suite first
  - builds `.tar.gz` archives for linux/darwin × amd64/arm64, generates a
    `checksums.txt`, and attaches both to a new GitHub Release
  - builds and pushes a multi-arch Docker image to
    `ghcr.io/jastBytes/immich-dlna-proxy:latest` and `:<version>`

No extra setup needed beyond pushing the repo - both workflows use the
automatically provided `GITHUB_TOKEN` (release creation + GHCR push are
covered by the `contents: write` / `packages: write` permissions declared
in `release.yml`).

## Known limitations / things to verify against your Immich version

- If folders/albums browse fine but clicking a photo shows nothing (the
  proxy logs `DownloadOriginal(...): unexpected status 403`), your API key
  is missing the `asset.download` permission - `asset.read` is enough to
  list albums/people but not to fetch the actual file bytes. Edit the key
  under Account Settings -> API Keys in Immich and grant it.
- Immich's REST API has changed across major versions. The JSON field
  names used here (`albumName`, `assetCount`, `originalFileName`,
  `originalMimeType`, `type`) match the commonly deployed v1 API as of
  2026. If album/asset listing returns errors, check your own server's
  live OpenAPI docs at `{IMMICH_URL}/api/doc` and adjust
  `immich/types.go` / `immich/client.go` accordingly.
- No transcoding: photos are streamed/cached as their original file. Most
  TVs handle JPEG fine; very large originals (e.g. 48MP RAW-derived JPEGs)
  might be slow to load or unsupported by some TVs. A follow-up version
  could cache Immich's `/thumbnail?size=preview` instead of the original
  for faster loading.
- Album/asset *listings* aren't cached, only the image bytes - a TV
  re-browsing a huge album will still hit the Immich API for the listing
  every time, just not re-download photos it already viewed.
- The disk cache has no encryption/access control beyond normal
  filesystem permissions; anyone with access to `CACHE_DIR` can read
  cached photos directly.
- `MAX_RESOLUTION` downscaling only supports JPEG and PNG (the two
  formats the Go standard library can decode *and* re-encode). HEIC,
  WebP, TIFF, and similar are served at their original resolution even
  if `MAX_RESOLUTION` is set - unsupported formats are passed through
  untouched rather than dropped. The downscaler uses a simple box filter,
  not a high-quality resampling algorithm; it's fine for "smaller file
  for an old TV", not for archival-quality thumbnails.
- Tested by compiling only (`go build`, `go vet`) in this environment —
  not yet verified against a real TV or Immich instance. DLNA
  compatibility varies a lot between TV brands (Samsung/LG/Sony each
  have their own quirks); expect to need some debugging with your
  specific TV. Tools like `python3 -m ssdp` or the "BubbleUPnP" Android
  app are useful for poking at the server independently of a TV.
- Album covers/thumbnails for the folder view itself aren't implemented
  (some DLNA clients show a folder icon; this is cosmetic).
