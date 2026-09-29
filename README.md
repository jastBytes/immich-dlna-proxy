<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
  <img alt="immich-dlna-proxy" src="assets/logo-light.svg" height="80">
</picture>

[![CI](https://github.com/jastbytes/immich-dlna-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/jastbytes/immich-dlna-proxy/actions/workflows/ci.yml)

Exposes your Immich albums, named people, and full photo/video timeline
(photos and videos) as a DLNA MediaServer so older Smart TVs / DLNA
clients can browse and display them without any extra app.

Written in Go, no external dependencies — just the standard library.

📖 Detailed docs: [Architecture](docs/architecture.md) · [Configuration](docs/configuration.md) · [Contributing](CONTRIBUTING.md)

## How it works

- **SSDP** (UDP 1900): announces the server on the LAN and answers
  `M-SEARCH` requests (for the device itself and each individual service
  it advertises) so TVs find it automatically.
- **ContentDirectory (SOAP over HTTP)**: on `Browse`, calls the Immich API
  (`GET /api/albums`, `GET /api/albums/{id}`, `GET /api/people`,
  `GET /api/people/{id}/assets`, `POST /api/search/metadata`) and maps the
  root to three folders, "Albums", "People", and "Timeline" (every
  photo/video, newest first - optionally grouped into year or month
  folders via `TIMELINE_GROUPING`), plus optional "Favorites", "On this
  day", "Places" and "Random" folders (`EXTRA_FOLDERS`), each
  album/person to a DLNA *container* (folder), and each photo/video
  asset to a DLNA *item*.
- **X_MS_MediaReceiverRegistrar**: a Microsoft-defined UPnP extension some
  clients (Xbox, Windows Media Player, some Samsung firmwares) require to
  be present before they'll browse a server's content at all - the proxy
  advertises it and always answers "authorized".
- **Media streaming (HTTP)**: `/media/{assetID}` serves photo/video bytes
  to the TV. On a cache hit, it's served straight from disk (with proper
  `Range`/`ETag` support via `http.ServeContent`) - no Immich call at
  all. On a cache miss, a photo is downloaded in full from Immich's
  `/api/assets/{id}/original`, written to the disk cache, then served
  from there. An uncached video is instead proxied straight through to
  Immich with the TV's `Range` header, so playback and seeking start
  immediately, while the full file downloads into the cache in the
  background for next time. Concurrent requests for the same uncached
  asset share one download. `/media/person/{personID}` works the same
  way for person cover thumbnails, backed by Immich's
  `/api/people/{id}/thumbnail`.
- **Thumbnails (HTTP)**: `/thumbnail/{assetID}` serves Immich's
  generated preview thumbnail (cached like `/media/`). Used as the
  `albumArtURI` for video items, since a video file can't double as its
  own preview image the way a photo can.

## Caching

Original photo/video bytes are cached on disk under `CACHE_DIR` (default
`/config/cache`) so repeated views of the same asset don't hit Immich
again - that covers `/media/{assetID}`, `/media/person/{personID}` and
`/thumbnail/{assetID}`. Album/asset/people *listings* (`Browse`) are only
kept in memory for `LISTING_CACHE_SECONDS` (default 30), so a TV paging
through a large album doesn't re-fetch the whole listing for every page,
while changes in Immich still show up within seconds. See
[Architecture → Caching](docs/architecture.md#caching) for cache keys and
eviction, and [→ Media streaming](docs/architecture.md#media-streaming)
for how `DISABLE_CACHE` changes this.

- Cache key is the asset ID (or `person:<id>` for person thumbnails,
  `thumb:<id>` for video preview thumbnails);
  stored as `<key>` (bytes) + `<key>.type` (MIME type sidecar).
- "Last used" is approximated by the file's mtime, touched on every hit.
- Once the cache exceeds `CACHE_MAX_MB`, a background sweep deletes the
  least-recently-used files first until back under budget.
- Set `DISABLE_CACHE=true` to fall back to the old behavior (always proxy
  live from Immich, nothing written to disk).

Assets with `type == "IMAGE"` or `type == "VIDEO"` are shown; any other
asset type is skipped.

## Configuration (environment variables)

| Variable         | Required | Default          | Description                                   |
|-------------------|:--------:|------------------|------------------------------------------------|
| `IMMICH_URL`       | yes      | –                | Base URL of your Immich server, e.g. `http://192.168.1.10:2283` |
| `IMMICH_API_KEY`   | yes*     | –                | Immich API key (needs album.read / asset.read / asset.download / asset.view / person.read - see [permissions](docs/configuration.md#api-key-permissions)). *Not required if `IMMICH_API_KEYS` is set. |
| `IMMICH_API_KEYS`  | no       | –                | Comma-separated API keys, for exposing more than one Immich user's library (e.g. one household sharing a server) - each gets its own top-level folder named after its account. See [Configuration](docs/configuration.md#multiple-immich-accounts). |
| `LISTEN_ADDR`      | no       | `:8200`          | HTTP bind address/port                         |
| `FRIENDLY_NAME`    | no       | `Immich Photos`  | Name shown on TVs when browsing servers        |
| `DEVICE_UUID`      | no       | fixed default    | Set your own stable UUID if running >1 instance |
| `SSDP_INTERFACE`   | no       | all interfaces   | Restrict SSDP to one NIC name, e.g. `eth0`     |
| `CACHE_DIR`        | no       | `/config/cache`  | Where cached photo bytes are stored             |
| `CACHE_MAX_MB`     | no       | `2048`           | Soft size budget in MB before LRU eviction kicks in |
| `DISABLE_CACHE`    | no       | `false`          | Set to `true` to disable caching entirely       |
| `PHOTO_SOURCE`     | no       | `auto`           | `auto`: JPEG/PNG as original, HEIC & other formats TVs can't show as Immich's JPEG preview. `original` / `preview` force one or the other. |
| `VIDEO_SOURCE`     | no       | `original`       | `transcoded` serves the version Immich transcoded (H.264 MP4 by default) for TVs that can't play HEVC/MKV etc. |
| `EXTRA_FOLDERS`    | no       | `favorites,onthisday` | Optional root folders, in order: `favorites`, `onthisday`, `places`, `random` (or `none`) |
| `TZ`               | no       | `UTC`            | Time zone for the "On this day" folder, e.g. `Europe/Berlin` |
| `MAX_RESOLUTION`   | no       | (unset = disabled) | Downscale photos larger than this to fit, e.g. `1920x1080`. Aspect ratio is preserved; smaller images are left untouched. |
| `MEDIA_FETCH_CONCURRENCY` | no | `4`         | Max photos/thumbnails allowed to download from Immich at once; extra requests queue for a free slot (giving up after 30s) instead of piling onto Immich |
| `LISTING_CACHE_SECONDS` | no | `30`         | How long album/people/timeline listings are reused across `Browse` calls; `0` always asks Immich live |
| `TIMELINE_GROUPING` | no     | `none`           | `year` or `month` to split the Timeline folder into year (and month) folders |
| `ADVERTISE_IP`     | no       | auto-detected    | IP announced to TVs via SSDP, if auto-detection picks the wrong one |
| `DEBUG`            | no       | `false`          | Set to `true` to log every media request and other per-photo details |

## Run locally

```bash
export IMMICH_URL=http://192.168.1.10:2283
export IMMICH_API_KEY=your-api-key
go run .
```

Or copy `.env.example` to `.env`, fill in your values, and run `make run`
(`.env` is gitignored). See `make help` for other targets (`build`, `test`,
`ci`, ...).

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
  ghcr.io/jastbytes/immich-dlna-proxy:latest
```

The `immich-dlna-proxy` package is public, so `docker run` works without
authentication.

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

## Known limitations / things to verify against your Immich version

- If folders/albums browse fine but photos, thumbnails or videos don't
  load, the API key is probably missing a permission: the proxy log then
  says which one (e.g. `... unexpected status 403 Forbidden - the API key
  probably lacks the asset.download permission`). `asset.download` is
  needed for original files, `asset.view` for HEIC photos (served as JPEG
  previews), video thumbnails and transcoded video. Edit the key under
  Account Settings -> API Keys in Immich and grant it - see
  [API key permissions](docs/configuration.md#api-key-permissions).
- Immich's REST API has changed across major versions. The JSON field
  names used here (`albumName`, `assetCount`, `originalFileName`,
  `originalMimeType`, `type`) match the commonly deployed v1 API as of
  2026. If album/asset listing returns errors, check your own server's
  live OpenAPI docs at `{IMMICH_URL}/api/doc` and adjust
  `immich/types.go` / `immich/client.go` accordingly.
- No transcoding: photos and videos are streamed/cached as their original
  file/codec. Most TVs handle JPEG and common video codecs (H.264/AAC in
  MP4) fine; very large photo originals (e.g. 48MP RAW-derived JPEGs) or
  video codecs/containers your TV doesn't support natively might be slow
  to load or fail outright, with no server-side fallback. A follow-up
  version could cache Immich's `/thumbnail?size=preview` for photos
  instead of the original for faster loading.
- Album/asset *listings* are only cached in memory for
  `LISTING_CACHE_SECONDS` - a TV re-browsing a huge album after that
  fetches the listing from Immich again (but not photos it already
  viewed).
- The first playback of an uncached video downloads it from Immich twice
  at the same time: once proxied to the TV, once into the cache.
- The disk cache has no encryption/access control beyond normal
  filesystem permissions; anyone with access to `CACHE_DIR` can read
  cached photos directly.
- `MAX_RESOLUTION` downscaling only supports JPEG and PNG (the two
  formats the Go standard library can decode *and* re-encode). HEIC,
  WebP, TIFF, and similar are served as Immich's JPEG preview by default
  (`PHOTO_SOURCE=auto`), which Immich already limits in size; with
  `PHOTO_SOURCE=original` they're served untouched. The downscaler uses a simple box filter,
  not a high-quality resampling algorithm; it's fine for "smaller file
  for an old TV", not for archival-quality thumbnails.
- DLNA compatibility varies a lot between TV brands (Samsung/LG/Sony each
  have their own quirks); expect to need some debugging with your
  specific TV. Tools like `python3 -m ssdp` or the "BubbleUPnP" Android
  app are useful for poking at the server independently of a TV.
- Album and person folders advertise a cover thumbnail (`albumArtURI`) -
  Immich's chosen album cover asset for albums, and the generated
  face-crop thumbnail for people - but not every DLNA client renders
  container-level cover art; some will still show a generic folder icon
  even though the data is present. This is cosmetic.

## Security

The server has no authentication and isn't meant to be exposed beyond a
trusted LAN — see [SECURITY.md](SECURITY.md) for the reasoning and how to
report a vulnerability.

## License

[MIT](LICENSE)
