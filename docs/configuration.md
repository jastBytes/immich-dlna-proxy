# Configuration

All configuration is done via environment variables - there's no config
file. `config.Load()` (`config/config.go`) reads and validates them at
startup and exits immediately with an error message if something
required is missing or malformed.

## Environment variables

| Variable | Required | Default | Description |
|---|:---:|---|---|
| `IMMICH_URL` | yes | – | Base URL of your Immich server, reachable from wherever the proxy runs, e.g. `http://192.168.1.10:2283`. No trailing slash needed. |
| `IMMICH_API_KEY` | yes | – | Immich API key. Needs at least `album.read`, `asset.read`, `asset.download`, and `person.read` permissions. Create one under Immich → Account Settings → API Keys. `asset.download` is easy to miss - without it, albums/people browse fine but every photo fails to load (proxy logs `DownloadOriginal(...): unexpected status 403`). |
| `LISTEN_ADDR` | no | `:8200` | `host:port` the HTTP part (description.xml, SOAP control, media streaming) binds to. |
| `FRIENDLY_NAME` | no | `Immich Photos` | Name shown on TVs when they list available DLNA servers. |
| `DEVICE_UUID` | no | fixed built-in default | Stable UUID identifying this device to DLNA clients. Set your own if you run more than one instance on the same network - every instance needs a distinct UUID or clients get confused about which is which. |
| `SSDP_INTERFACE` | no | all interfaces | Restrict SSDP (discovery) to a single network interface name, e.g. `eth0`. Useful on multi-homed hosts to avoid announcing on, say, a Docker bridge interface. |
| `CACHE_DIR` | no | `/config/cache` | Directory where cached photo bytes are stored. Set to match a persistent volume/mount when running in a container. |
| `CACHE_MAX_MB` | no | `2048` | Soft size budget for `CACHE_DIR` in megabytes. Once exceeded, least-recently-viewed photos are deleted first until back under budget. |
| `DISABLE_CACHE` | no | `false` | Set to `true` to disable disk caching entirely and always stream live from Immich (nothing written to disk). |
| `MAX_RESOLUTION` | no | (unset) | Downscale photos larger than this, e.g. `1920x1080`. Preserves aspect ratio; only JPEG/PNG are supported (others pass through untouched). See [Downscaling](architecture.md#downscaling) for details. |

`CACHE_MAX_MB` must parse as an integer; a non-numeric value fails
startup with a clear error rather than silently falling back to a
default.

## Running locally (no Docker)

Requires Go 1.26.5+.

```bash
export IMMICH_URL=http://192.168.1.10:2283
export IMMICH_API_KEY=your-api-key
go run .
```

Logs go to stdout, including the friendly name/UUID being announced and
whether the disk cache is enabled.

## Running in Docker

### Networking

**Use `--network host`.** SSDP discovery relies on UDP multicast, which
generally does not traverse Docker's default bridge network. Without
host networking, the HTTP endpoints still work if you know the IP/port,
but TVs won't auto-discover the server via the normal "scan for DLNA
servers" flow.

### Persisting the cache

Mount a host directory at `/config/cache` (the default `CACHE_DIR`) so
cached photos survive container recreation/updates:

```bash
docker run -d \
  --name immich-dlna-proxy \
  --network host \
  -v /path/on/host/cache:/config/cache \
  -e IMMICH_URL=http://192.168.1.10:2283 \
  -e IMMICH_API_KEY=your-api-key \
  -e FRIENDLY_NAME="Living Room Photos" \
  ghcr.io/jastBytes/immich-dlna-proxy:latest
```

See the main [README](../README.md#run-in-docker) for the full pull vs.
local-build instructions and image tagging scheme.

## Running on Unraid (via the provided template)

[`unraid-template.xml`](../unraid-template.xml) is a Community
Applications-style template. This app isn't published to the CA app
store, so the **Template** dropdown on the Add Container page (a plain
`<select>` - you can't type or paste into it) won't list it until you've
added it by one of these two methods:

- **Drop the file locally (works on stock Unraid, no plugins needed).**
  Either copy `unraid-template.xml` to
  `/boot/config/plugins/dockerMan/templates-user/immich-dlna-proxy.xml`
  over the network (reachable as
  `\\<tower>\flash\config\plugins\dockerMan\templates-user\`), or run
  this from the Unraid web terminal:
  ```bash
  mkdir -p /boot/config/plugins/dockerMan/templates-user
  curl -o /boot/config/plugins/dockerMan/templates-user/immich-dlna-proxy.xml \
    https://raw.githubusercontent.com/jastBytes/immich-dlna-proxy/main/unraid-template.xml
  ```
  Then open **Docker tab → Add Container** and reload the page - pick
  "immich-dlna-proxy" from the **"Select a template"** dropdown, which
  now lists it because Unraid scans that folder to build the list.
  You'll need to re-run this if the template ever changes.
- **Template Repositories (auto-updating, requires the Community
  Applications plugin).** If you have CA installed, Docker tab → scroll
  to the bottom → **"Template Repositories"** box → add
  `https://github.com/jastBytes/immich-dlna-proxy` → **Save**. Unraid
  scans the repo for `.xml` templates and adds them to the dropdown, and
  re-checks the repo periodically so template updates show up without
  manual re-downloading. Without CA, this box isn't present at all - use
  the method above instead.

Either way, once "immich-dlna-proxy" is selectable from the **"Select a
template"** dropdown, choosing it should populate every field below
(Repository, Network Type, Icon, and all the Config fields - Immich URL,
Immich API Key, cache path, etc.). If it doesn't, or the container name
doesn't work, please open an issue.

It pre-configures:

- **Network Type: Host** (required, see above)
- **Repository:** `ghcr.io/jastBytes/immich-dlna-proxy:latest`
- **Cache path:** maps `/config/cache` inside the container to
  `/mnt/user/appdata/immich-dlna-proxy/cache` on the array by default -
  change this in the template's "Cache" field if you'd rather use a
  cache pool or different location
- All environment variables from the table above, with `Immich URL` and
  `Immich API Key` marked required and everything else under "Show more
  settings..." (advanced)

After adding the container, fill in **Immich URL** and **Immich API
Key** at minimum, then start it. Check the container's log for the
"Starting SSDP responder" line to confirm it came up correctly.

## Tuning the cache

- Lower `CACHE_MAX_MB` if disk space on your Unraid array/cache pool is
  tight, or raise it if you have a large photo library and want more of
  it to stay "instantly" viewable after the first watch.
- If you're just testing/debugging and don't want stale cached files
  interfering, set `DISABLE_CACHE=true` temporarily rather than manually
  clearing `CACHE_DIR`.
- To clear the cache manually, it's safe to delete everything inside
  `CACHE_DIR` while the container is stopped (or even while running -
  the worst case is a few extra Immich downloads for photos that were
  mid-cache).
- Note that `CACHE_DIR` stores whatever `MAX_RESOLUTION` produced - if
  you change `MAX_RESOLUTION` later, previously cached files stay at
  the old resolution until they're evicted (by size budget) or you clear
  the cache manually.

## Downscaling large photos

Set `MAX_RESOLUTION` to a `WIDTHxHEIGHT` bounding box, e.g.:

```bash
-e MAX_RESOLUTION=1920x1080
```

Photos larger than this are downscaled to fit (aspect ratio preserved)
before being cached/served; smaller photos are left untouched. Leave it
unset to always serve originals at full resolution (the default).

This is mainly useful for two things:

- **Faster loading on older/weaker TVs** that struggle with very large
  originals (e.g. 48MP+ photos).
- **Smaller cache footprint** - downscaled photos take up much less
  space in `CACHE_DIR`, so a given `CACHE_MAX_MB` budget holds more
  photos.

Only JPEG and PNG originals are downscaled; other formats (HEIC, WebP,
...) are served unchanged regardless of this setting. See
[Architecture → Downscaling](architecture.md#downscaling) for how it
works internally.
