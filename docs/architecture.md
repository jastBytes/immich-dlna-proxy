# Architecture

`immich-dlna-proxy` makes Immich albums and people browsable and viewable
on DLNA clients (mainly Smart TVs) by implementing the relevant slice of
the UPnP/DLNA MediaServer protocol and translating it into calls against
the Immich REST API. It's a single Go binary with no external
dependencies.

Only photos (`type == "IMAGE"`) are exposed. Videos are skipped entirely
in this version.

## The three protocol layers

A DLNA client interacts with a MediaServer in three stages, and the
proxy implements one component for each:

| Stage | Protocol | Implemented in |
|---|---|---|
| 1. Discovery | SSDP (UDP multicast) | `dlna/ssdp.go` |
| 2. Description | HTTP GET of a device/service description XML | `dlna/description.go` |
| 3. Control | SOAP-over-HTTP actions (Browse, GetProtocolInfo, ...) | `dlna/contentdirectory.go`, `dlna/connectionmanager.go`, `dlna/mediareceiverregistrar.go` |

Media itself (the actual photo bytes) is served over a plain HTTP `GET`,
outside the SOAP layer - see [Media streaming](#media-streaming) below.

### 1. Discovery (SSDP)

On startup, `dlna.RunSSDP` joins the standard SSDP multicast group
(`239.255.255.250:1900`) and does two things:

- **Answers `M-SEARCH` requests.** When a TV searches the network for
  media servers, the proxy replies directly to the searcher (unicast)
  with an `HTTP/1.1 200 OK` response containing a `LOCATION` header
  pointing at its own `description.xml`. It answers every NT/ST it
  advertises individually (`upnp:rootdevice`, its `uuid:`, the device
  type, and each service type - ContentDirectory, ConnectionManager,
  X_MS_MediaReceiverRegistrar), since control points commonly search for
  a specific service type rather than the device type - a server that
  only answers device-level searches would be invisible to them. A
  search for `ssdp:all` gets one reply per advertised type, same as an
  alive `NOTIFY` burst.
- **Sends periodic `NOTIFY ssdp:alive` announcements** (every 15
  minutes) to the multicast group, so clients that are already listening
  pick up the server without having to search first.

The `LOCATION` URL uses whatever local IP the OS would pick to reach the
public internet (`detectLocalIP` in `ssdp.go`), combined with the
configured HTTP port. This is why **host networking is required in
Docker** - see [Configuration](configuration.md#networking) for why.

### 2. Description

`GET /description.xml` returns a UPnP device description declaring the
device as a `MediaServer:1` with three services:

- `ContentDirectory:1` - lets clients browse the library (albums, photos)
- `ConnectionManager:1` - a required-by-spec service that mostly just
  advertises supported protocols/formats; some TVs (notably Samsung) are
  strict about its presence even though the proxy doesn't do anything
  interesting with it
- `X_MS_MediaReceiverRegistrar:1` - a Microsoft-defined extension that
  Xbox, Windows Media Player, and a number of Samsung TV firmwares use to
  check whether they're "authorized" to see a media server's content
  before browsing it. If it isn't advertised at all, these clients
  silently treat the server as having no content instead of erroring, so
  the proxy advertises it and always answers "yes, authorized"
  (`dlna/mediareceiverregistrar.go`).

Each service's SCPD (Service Control Protocol Description) is served
separately at `/ContentDirectory.xml`, `/ConnectionManager.xml`, and
`/X_MS_MediaReceiverRegistrar.xml`, and declares which SOAP actions each
service supports.

### 3. Control (ContentDirectory Browse)

This is where Immich data gets turned into DLNA objects. The client
issues a SOAP `Browse` action against `/ctl/ContentDirectory` with an
`ObjectID` and a `BrowseFlag` (`BrowseDirectChildren` to list children,
`BrowseMetadata` to describe the object itself). The root exposes two
fixed folders, "Albums" and "People"; the proxy maps object IDs to
Immich concepts like this:

| ObjectID | Represents | `BrowseDirectChildren` returns |
|---|---|---|
| `0` | Root | Two containers: `albums` and `people` |
| `albums` | "Albums" folder | One `container` per album (`GET /api/albums`) |
| `album:<id>` | One album | One `item` per **photo** asset in that album (`GET /api/albums/{id}`, filtered to `type == "IMAGE"`) |
| `people` | "People" folder | One `container` per **named** person (`GET /api/people`, filtered to entries with a non-empty `name` - unconfirmed/unnamed face clusters are skipped) |
| `person:<id>` | One person | One `item` per photo they appear in (`GET /api/people/{id}/assets`, filtered to `type == "IMAGE"`) |
| `asset:<id>` | One photo | N/A (items have no children); `BrowseMetadata` returns the item itself |

Each `Browse` call hits the Immich API fresh - album/asset/people
**listings** are not cached (only the underlying image bytes are, see
[Caching](#caching) below). A TV re-opening the same album or person
repeatedly will re-fetch the listing every time.

People folders don't report a `childCount` (the DIDL-Lite attribute is
simply omitted): getting an accurate photo count per person would need
one extra `GetPersonStatistics` call per person just to render the
"People" listing, which doesn't scale for libraries with many tagged
people. DLNA clients treat a container without `childCount` as
"browsable, count unknown" rather than empty, so this doesn't stop
anyone from opening the folder.

The response is a small DIDL-Lite XML document (built in `dlna/didl.go`)
embedded, escaped, inside the SOAP response body - this is standard UPnP
ContentDirectory behavior, not something specific to this proxy. That
escaping is done with a minimal escaper (`xmlTextEscape` in
`dlna/contentdirectory.go`) that only handles `&`, `<`, `>` - not
`html.EscapeString`, which also turns `"` into `&#34;`. That's valid XML,
but some DLNA clients (confirmed via packet capture: Samsung's `SEC_HHP`
stack, used across many TV generations) only undo `&lt;`/`&gt;`/`&amp;`
before treating the result as raw XML rather than doing real entity
decoding, so a `&#34;`-escaped quote breaks every attribute in the
embedded DIDL-Lite (`id=&#34;0&#34;` isn't valid attribute syntax) and
the client silently treats the container as unbrowsable. Get this wrong
and the symptom is exactly "the client fetches the description fine,
calls `BrowseMetadata` on root, and simply never calls
`BrowseDirectChildren`" - a genuinely hard bug to spot without capturing
a working reference implementation's wire traffic to diff against, since
the response still looks like well-formed XML at a glance.

`BrowseDirectChildren` honors `StartingIndex`/`RequestedCount` (see
`page` in `dlna/contentdirectory.go`) - `RequestedCount` 0 means "no
limit" per spec. A client that paginates (anything with more items in a
container than fit on one page) previously got the identical full list
back on every page instead of successive slices of it.

`GetSortCapabilities` advertises `dc:title,dc:date`. A `Browse` or
`Search` request whose `SortCriteria` includes `+dc:title`/`-dc:title` or
`+dc:date`/`-dc:date` (see `parseSortCriteria`/`sortByTitle`/`sortPhotos`
in `dlna/contentdirectory.go`) gets sorted before pagination is applied:
`dc:title` sorts albums, people, or photos case-insensitively by
name/title; `dc:date` sorts photos by capture time (`Asset.CapturedAt`,
parsed from Immich's `fileCreatedAt`) and only has an effect on photo
listings (`album:<id>`/`person:<id>`) - albums and people have no
per-item date to sort by. An asset with a missing or unparseable
`fileCreatedAt` sorts as the oldest possible photo rather than erroring.
Only the first recognized property in a comma-separated `SortCriteria`
is honored; any other property is ignored, and an empty/unrecognized
`SortCriteria` leaves Immich's own listing order untouched.

A handful of other details turned out to matter for Samsung TVs
specifically, verified by diffing wire traffic against a real minidlna
instance on the same TV: the DIDL-Lite root element declares
`xmlns:sec="http://www.sec.co.kr/dlna"` (Samsung's own, unused-but-
expected extension namespace) alongside the standard DIDL-Lite/dc/upnp/
dlna namespaces; the root container carries an
`<upnp:searchClass includeDerived="1">object.item.imageItem</upnp:searchClass>`;
every container carries `searchable="1"` and `<upnp:storageUsed>-1</upnp:storageUsed>`;
and every HTTP response (not just SSDP replies) carries a `SERVER` header
ending in `DLNADOC/1.50` plus an `EXT` header - written with the exact
all-caps casing and no trailing space after the colon
(`writeRawUPnPResponse` bypasses `http.ResponseWriter` and writes the
response by hand via `http.Hijacker`, since Go's header writer always
canonicalizes the name to `Ext` and always inserts `": "`).

Each `item` element includes a `<res>` tag pointing at
`http://<host>/media/<assetID>` - that's the URL that ends up loaded by
the TV to actually display the photo. It also includes an
`<upnp:albumArtURI>` tag pointing at the same URL: without it, media
browsers like Home Assistant's list titles but show a placeholder icon
instead of a thumbnail (they don't fall back to `<res>` for previews).

## Media streaming

`GET /media/{assetID}` is a plain (non-SOAP) HTTP endpoint that serves
the actual photo bytes. It's handled in `dlna/server.go` and behaves
differently depending on whether the disk cache is enabled (the default):

```
                 ┌─────────────┐   Browse (SOAP)    ┌──────────────────┐
      TV  ──────▶│ ContentDir- │───────────────────▶│  Immich REST API │
                 │  ectory     │◀───album/asset list─┤  /api/albums...  │
                 └─────────────┘                     └──────────────────┘
                        │ <res> URL: /media/{id}
                        ▼
                 ┌─────────────┐   cache hit    ┌───────────────┐
      TV  ──────▶│ /media/{id} │───────────────▶│  disk cache   │
                 │             │                │ CACHE_DIR/{id}│
                 │             │◀───────────────┤               │
                 │             │                └───────────────┘
                 │             │   cache miss
                 │             │──────────────────────┐
                 │             │                       ▼
                 │             │              ┌──────────────────┐
                 │             │◀─────────────┤  Immich REST API │
                 └─────────────┘  full original │ /api/assets/.../original │
                                                └──────────────────┘
```

- **Cache hit:** the file is opened from `CACHE_DIR` and served via
  Go's `http.ServeContent`, which handles `Range` requests, `ETag`, and
  `Last-Modified` automatically. No Immich call happens at all.
- **Cache miss:** the proxy downloads the *complete* original from
  Immich (ignoring any `Range` header on the inbound request - it always
  wants the whole file), normalizes its EXIF orientation and downscales
  it as configured (see [Orientation](#orientation) and
  [Downscaling](#downscaling)), writes the result to disk, then serves it
  from the newly written file the same way as a cache hit. This means
  the very first view of a photo waits for the full download before any
  bytes reach the TV; subsequent views are effectively instant.
- **Cache disabled** (`DISABLE_CACHE=true`): same download, orientation
  fix, and optional downscale as a cache miss above, but the result is
  served straight from memory instead of being written to disk.

## Orientation

Every JPEG is checked for an EXIF orientation tag (`imageproc/orientation.go`)
before being cached/served, regardless of whether `MAX_RESOLUTION` is set.

- Most DLNA renderers (TVs, media players) show the raw pixel grid and
  ignore the EXIF orientation tag entirely. Phone cameras commonly record
  portrait photos "sideways" with a rotate-90 tag rather than rotating the
  pixels themselves, since that's cheaper for the camera - so without this
  step, those photos show up sideways on a TV even though photo apps that
  honor EXIF display them correctly.
- If the tag says anything other than "normal" (1), the proxy decodes the
  image, bakes the required rotation/flip into the pixels, and re-encodes
  without the tag (so the now-correct pixels aren't rotated again by a
  renderer that *does* honor EXIF). If there's no tag, or it's already 1,
  the image is left untouched.
- Only JPEG is supported (the format that carries EXIF here); other
  formats pass through unchanged.

## Caching

See [`cache/cache.go`](../cache/cache.go) for the implementation. Key
points:

- Each cached asset is stored as two files: `<assetID>` (the bytes) and
  `<assetID>.type` (a one-line MIME type sidecar).
- "Last used" is approximated by the main file's **mtime**, which gets
  touched (`os.Chtimes`) on every cache hit. There's no separate access
  log or database.
- Writes are atomic: bytes are written to a temp file in the same
  directory, then `os.Rename`d into place, so a crash mid-download never
  leaves a truncated file at the final path.
- After every write, a background goroutine checks whether the total
  cache size exceeds `CACHE_MAX_MB`. If so, it lists all cached files,
  sorts by mtime ascending, and deletes the oldest ones (bytes + type
  sidecar together) until back under budget. This is a plain LRU
  eviction sweep, not a background daemon - it only runs reactively
  after writes.

## Downscaling

If `MAX_RESOLUTION` is set (e.g. `1920x1080`), photos larger than that
are downscaled to fit within it (aspect ratio preserved) before being
cached/served. See [`imageproc/resize.go`](../imageproc/resize.go).

- Only **JPEG and PNG** are supported, since those are the only formats
  the Go standard library can both decode and re-encode. Anything else
  (HEIC, WebP, TIFF, ...) is served at its original resolution
  regardless of `MAX_RESOLUTION` - the proxy checks the format up front
  and passes unsupported ones through untouched rather than failing the
  request.
- Downscaling uses a **box filter**: each destination pixel is the
  average of the block of source pixels it maps to. It's a deliberately
  simple, dependency-free algorithm - a reasonable trade-off for
  "smaller file so an old TV loads it faster", not a high-quality
  resampling filter like Lanczos.
- Resizing happens **before** the cache write (on a cache miss) or,
  when caching is disabled, on every request. Either way, the decision
  of whether to resize is cheap: `image.DecodeConfig` reads just the
  header to check dimensions, and the (comparatively expensive) full
  decode + box filter + re-encode only runs when the image actually
  exceeds the configured bounds.
- Every request already buffers and decodes the full photo in memory for
  [orientation](#orientation) normalization, so `MAX_RESOLUTION` doesn't
  cost an extra decode pass on top of that.

## What isn't implemented

- **Videos.** Only `type == "IMAGE"` assets are ever listed or served.
- **Unnamed people.** Immich creates a Person for every detected face
  cluster, including ones you haven't confirmed/named yet. Only named
  people show up as folders - there's no "unknown faces" browsing.
- **Transcoding/format conversion.** JPEG/PNG can be downscaled (see
  `MAX_RESOLUTION` below) but never converted to a different format.
- **Listing cache.** Album/asset/people browsing always hits the Immich
  API live (only the image bytes are cached).
- **Authentication/authorization at the DLNA layer.** Anyone who can
  reach the proxy's HTTP port on your LAN can browse and view all
  albums visible to the configured API key. There's no per-client access
  control - this mirrors how DLNA works in general (it has no built-in
  auth), so treat it the same as any other LAN media server.
