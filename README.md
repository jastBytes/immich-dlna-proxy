<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
  <img alt="immich-dlna-proxy" src="assets/logo-light.svg" height="80">
</picture>

[![CI](https://github.com/jastbytes/immich-dlna-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/jastbytes/immich-dlna-proxy/actions/workflows/ci.yml)

Watch your [Immich](https://immich.app/) photos and videos on your TV -
no app needed. immich-dlna-proxy makes your Immich library show up as a
media server (DLNA) on your home network, so Smart TVs, consoles and
media players can browse and play it with their built-in media browser.

## What it does

- **Browse your library on the TV:** Albums, People (everyone you've
  named in Immich) and a Timeline of everything, newest first - plus
  optional Favorites, On this day, Places and Random folders.
- **Photos and videos:** iPhone HEIC photos and other formats TVs can't
  show are served as Immich's JPEG previews automatically; videos your
  TV can't play can optionally be served as Immich's transcoded version.
- **Fast:** viewed photos are cached on disk, videos start playing right
  away and support seeking.
- **Zero setup on the TV:** it's found automatically on your network.
- **Several Immich users** can share one instance, each with their own
  folder.
- One small program, no database - runs anywhere Docker runs, or as a
  single binary.

## Quickstart

You need a running Immich server and a machine on the same network as
your TV (e.g. the server Immich runs on).

### 1. Create an Immich API key

In Immich, go to **Account Settings → API Keys → New API Key**. Either
give it all permissions, or at least `album.read`, `asset.read`,
`asset.download`, `asset.view` and `person.read`
([what each is for](docs/configuration.md#api-key-permissions)).

### 2. Start the proxy

With Docker:

```bash
docker run -d \
  --name immich-dlna-proxy \
  --restart unless-stopped \
  --network host \
  -v immich-dlna-proxy-cache:/config/cache \
  -e IMMICH_URL=http://192.168.1.10:2283 \
  -e IMMICH_API_KEY=your-api-key \
  -e FRIENDLY_NAME="Immich Photos" \
  -e TZ=Europe/Berlin \
  ghcr.io/jastbytes/immich-dlna-proxy:latest
```

Replace `IMMICH_URL` with your Immich server's address and
`IMMICH_API_KEY` with the key from step 1. Keep `--network host`: TVs
find media servers via network broadcasts that don't reach a container
on Docker's default network.

Other ways to run it:

- **Unraid:** add the template
  [`unraid-template.xml`](unraid-template.xml) (Docker → Add Container),
  see [Running on Unraid](docs/configuration.md#running-on-unraid-via-the-provided-template).
- **Proxmox:** an LXC container with the binary as a systemd service, see
  [Running on Proxmox](docs/proxmox.md).
- **Without Docker:** download the binary for your system from the
  [releases page](https://github.com/jastbytes/immich-dlna-proxy/releases),
  set the same environment variables plus `CACHE_DIR` (a writable
  folder), and run `./immich-dlna-proxy`.

### 3. Open it on your TV

Open your TV's media player / media browser (often called "Media",
"Source" or "Home network") and pick **Immich Photos** (or whatever you
set as `FRIENDLY_NAME`). It usually appears within a minute.

### Basic settings

| Variable | Default | What it does |
|---|---|---|
| `IMMICH_URL` | – (required) | Address of your Immich server, e.g. `http://192.168.1.10:2283` |
| `IMMICH_API_KEY` | – (required) | The API key from step 1 |
| `FRIENDLY_NAME` | `Immich Photos` | Name shown on the TV |
| `TZ` | `UTC` | Your time zone, e.g. `Europe/Berlin` (for "On this day") |
| `EXTRA_FOLDERS` | `favorites,onthisday` | Extra folders next to Albums/People/Timeline: any of `favorites`, `onthisday`, `places`, `random`, or `none` |
| `TIMELINE_GROUPING` | `none` | `year` or `month` to split the Timeline into folders |

Everything else is optional - see [all settings](#all-settings) below.

### Something doesn't work?

- **The TV doesn't find the server:** make sure the container uses host
  networking and the TV is on the same network (not a guest Wi-Fi or a
  different VLAN). If the TV finds it but can't open it, set
  `ADVERTISE_IP` to the address of the machine running the proxy.
- **Folders show up, but photos or videos don't load:** the API key is
  missing a permission - the proxy's log (`docker logs immich-dlna-proxy`)
  names which one. Edit the key in Immich and add it.
- **Some videos won't play:** your TV probably doesn't support their
  format. Set `VIDEO_SOURCE=transcoded` to serve the versions Immich
  already converted for its app.
- **`permission denied` for the cache** when mounting a host folder: the
  container runs as user `65532`, so run
  `chown -R 65532:65532 /path/to/cache` (see
  [this fix](docs/configuration.md#fixing-cache-put-failed--permission-denied)).

For more details on a request, set `DEBUG=true`.

---

## Documentation

- [Configuration](docs/configuration.md) - every setting in detail,
  Docker, Unraid, cache tuning, multiple accounts
- [Proxmox](docs/proxmox.md) - step-by-step LXC setup
- [Architecture](docs/architecture.md) - how the DLNA protocol pieces fit
  together, caching and streaming internals
- [Contributing](CONTRIBUTING.md)

## All settings

| Variable         | Required | Default          | Description                                   |
|-------------------|:--------:|------------------|------------------------------------------------|
| `IMMICH_URL`       | yes      | –                | Base URL of your Immich server, e.g. `http://192.168.1.10:2283` |
| `IMMICH_API_KEY`   | yes*     | –                | Immich API key (needs album.read / asset.read / asset.download / asset.view / person.read - see [permissions](docs/configuration.md#api-key-permissions)). *Not required if `IMMICH_API_KEYS` is set. |
| `IMMICH_API_KEYS`  | no       | –                | Comma-separated API keys, for exposing more than one Immich user's library (e.g. one household sharing a server) - each gets its own top-level folder named after its account. See [Configuration](docs/configuration.md#multiple-immich-accounts). |
| `LISTEN_ADDR`      | no       | `:8200`          | HTTP bind address/port                         |
| `FRIENDLY_NAME`    | no       | `Immich Photos`  | Name shown on TVs when browsing servers        |
| `DEVICE_UUID`      | no       | fixed default    | Set your own stable UUID if running >1 instance |
| `SSDP_INTERFACE`   | no       | all interfaces   | Restrict SSDP to one NIC name, e.g. `eth0`     |
| `ADVERTISE_IP`     | no       | auto-detected    | IP announced to TVs via SSDP, if auto-detection picks the wrong one |
| `CACHE_DIR`        | no       | `/config/cache`  | Where cached photo/video bytes are stored       |
| `CACHE_MAX_MB`     | no       | `2048`           | Soft size budget in MB before LRU eviction kicks in |
| `DISABLE_CACHE`    | no       | `false`          | Set to `true` to disable caching entirely       |
| `PHOTO_SOURCE`     | no       | `auto`           | `auto`: JPEG/PNG as original, HEIC & other formats TVs can't show as Immich's JPEG preview. `original` / `preview` force one or the other. |
| `VIDEO_SOURCE`     | no       | `original`       | `transcoded` serves the version Immich transcoded (H.264 MP4 by default) for TVs that can't play HEVC/MKV etc. |
| `EXTRA_FOLDERS`    | no       | `favorites,onthisday` | Optional root folders, in order: `favorites`, `onthisday`, `places`, `random` (or `none`) |
| `TZ`               | no       | `UTC`            | Time zone for the "On this day" folder, e.g. `Europe/Berlin` |
| `TIMELINE_GROUPING` | no     | `none`           | `year` or `month` to split the Timeline folder into year (and month) folders |
| `TITLE_DATE_PREFIX` | no     | `false`          | Prefix titles with the capture date, for TVs that always sort by title |
| `TITLE_DATE_PREFIX_DESC` | no | `false`         | With `TITLE_DATE_PREFIX`, make title sorting come out newest-first |
| `MAX_RESOLUTION`   | no       | (unset = disabled) | Downscale photos larger than this to fit, e.g. `1920x1080`. Aspect ratio is preserved; smaller images are left untouched. |
| `MEDIA_FETCH_CONCURRENCY` | no | `4`         | Max photos/thumbnails allowed to download from Immich at once; extra requests queue for a free slot (giving up after 30s) instead of piling onto Immich |
| `LISTING_CACHE_SECONDS` | no | `30`         | How long album/people listings are reused across `Browse` calls; `0` always asks Immich live |
| `TIMELINE_REFRESH_MINUTES` | no | `15`      | The whole-library listing (Timeline, Places, Random) is loaded at startup and served from memory; it's refreshed in the background once older than this |
| `DEBUG`            | no       | `false`          | Set to `true` to log every media request and other per-photo details |

Details for each, including validation rules, are in
[Configuration](docs/configuration.md#environment-variables).

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

Assets with `type == "IMAGE"` or `type == "VIDEO"` are shown; any other
asset type is skipped. Written in Go, no external dependencies - just the
standard library.

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
- Set `DISABLE_CACHE=true` to always proxy live from Immich, with nothing
  written to disk.

## Building from source

Requires Go (see `go.mod` for the version):

```bash
export IMMICH_URL=http://192.168.1.10:2283
export IMMICH_API_KEY=your-api-key
go run .
```

Or copy `.env.example` to `.env`, fill in your values, and run `make run`
(`.env` is gitignored). See `make help` for other targets (`build`, `test`,
`ci`, ...). To build the Docker image yourself:
`docker build -t immich-dlna-proxy .`

## Known limitations

- Immich's REST API has changed across major versions. If album/asset
  listing returns errors, check your own server's live OpenAPI docs at
  `{IMMICH_URL}/api/doc` and compare with `immich/types.go` /
  `immich/client.go` - and please open an issue.
- Photos and videos are only converted when `PHOTO_SOURCE` /
  `VIDEO_SOURCE` say so, using what Immich already generated - the proxy
  doesn't transcode anything itself. Very large photo originals or video
  formats your TV doesn't support may be slow or fail with
  `PHOTO_SOURCE=original` / `VIDEO_SOURCE=original`.
- The first playback of an uncached video downloads it from Immich twice
  at the same time: once proxied to the TV, once into the cache.
- The disk cache has no encryption/access control beyond normal
  filesystem permissions; anyone with access to `CACHE_DIR` can read
  cached photos directly.
- `MAX_RESOLUTION` downscaling only supports JPEG and PNG (the two
  formats the Go standard library can decode *and* re-encode). HEIC,
  WebP, TIFF, and similar are served as Immich's JPEG preview by default
  (`PHOTO_SOURCE=auto`), which Immich already limits in size; with
  `PHOTO_SOURCE=original` they're served untouched. The downscaler uses
  a simple box filter - fine for "smaller file for an old TV", not for
  archival-quality thumbnails.
- DLNA compatibility varies a lot between TV brands (Samsung/LG/Sony each
  have their own quirks); expect to need some debugging with your
  specific TV. Tools like `python3 -m ssdp` or the "BubbleUPnP" Android
  app are useful for poking at the server independently of a TV.
- Album and person folders advertise a cover thumbnail (`albumArtURI`),
  but not every DLNA client renders container-level cover art; some
  still show a generic folder icon. This is cosmetic.

## Security

The server has no authentication and isn't meant to be exposed beyond a
trusted LAN — see [SECURITY.md](SECURITY.md) for the reasoning and how to
report a vulnerability.

## License

[MIT](LICENSE)
