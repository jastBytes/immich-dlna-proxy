# Architecture

`immich-dlna-proxy` makes Immich albums and people browsable and viewable
on DLNA clients (mainly Smart TVs) by implementing the relevant slice of
the UPnP/DLNA MediaServer protocol and translating it into calls against
the Immich REST API. It's a single Go binary with no external
dependencies.

Photos and videos (`type == "IMAGE"` or `type == "VIDEO"`) are exposed;
any other asset type Immich might report (e.g. audio) is skipped.

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

On shutdown (SIGINT/SIGTERM, e.g. `docker stop`), `main.go` cancels
`RunSSDP`'s context and it sends one `NOTIFY ssdp:byebye` per advertised
type, so TVs drop the server right away instead of listing it until the
last announcement's `max-age` (30 minutes) runs out; the HTTP server then
gets 5 seconds to finish in-flight requests.

The `LOCATION` URL's IP is chosen by `advertiseIP` (`ssdp.go`):
`ADVERTISE_IP` if set; otherwise, with `SSDP_INTERFACE` set, that
interface's own IPv4 address (so a multi-homed host announces the
address on the network it actually advertises to); otherwise whatever
local IP the OS would pick to reach the public internet
(`detectLocalIP`); and on a LAN with no default route at all, the first
non-loopback IPv4 address. It's combined with the configured HTTP port.
This is why **host networking is required in Docker** - see
[Configuration](configuration.md#networking) for why.

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

The description also declares an `<iconList>` with 48x48 and 120x120 PNG
icons (`/icon48.png`, `/icon120.png`), embedded into the binary via
`go:embed` from `dlna/icons/`. This is what TVs and other DLNA control
points show next to the server's friendly name in their media-server
list - it's rasterized once from `icon.svg` since embedded DLNA stacks
generally expect PNG/JPEG icons, not SVG.

### 3. Control (ContentDirectory Browse)

This is where Immich data gets turned into DLNA objects. The client
issues a SOAP `Browse` action against `/ctl/ContentDirectory` with an
`ObjectID` and a `BrowseFlag` (`BrowseDirectChildren` to list children,
`BrowseMetadata` to describe the object itself). The root exposes three
fixed folders, "Albums", "People", and "Timeline"; the proxy maps object
IDs to Immich concepts like this (this is the mapping for a single
configured Immich account - see
[Multiple Immich accounts](#multiple-immich-accounts) below for how it
changes with `IMMICH_API_KEYS`):

| ObjectID | Represents | `BrowseDirectChildren` returns |
|---|---|---|
| `0` | Root | Three containers: `albums`, `people`, and `timeline`, followed by any optional folders `EXTRA_FOLDERS` enables (see [Optional folders](#optional-folders-extra_folders)) |
| `albums` | "Albums" folder | One `container` per album (`GET /api/albums`) |
| `album:<id>` | One album | One `item` per **photo/video** asset in that album (`GET /api/albums/{id}`, filtered to `type == "IMAGE"` or `"VIDEO"`) |
| `people` | "People" folder | One `container` per **named** person (`GET /api/people`, filtered to entries with a non-empty `name` - unconfirmed/unnamed face clusters are skipped) |
| `person:<id>` | One person | One `item` per photo/video they appear in (`GET /api/people/{id}/assets`, filtered to `type == "IMAGE"` or `"VIDEO"`) |
| `timeline` | "Timeline" folder | One `item` per photo/video across the whole library, most recently taken first (`POST /api/search/metadata` with `order: "desc"` and no album/person filter, filtered to `type == "IMAGE"` or `"VIDEO"`) - or, with `TIMELINE_GROUPING=year`/`month`, one `container` per year (newest first, plus `timeline:unknown` for assets without a capture date) |
| `timeline:<YYYY>` | One year (grouping only) | With `year`: that year's items. With `month`: one `container` per month (`timeline:<YYYY-MM>`) |
| `timeline:<YYYY-MM>` | One month (`month` grouping only) | That month's items |
| `favorites` | "Favorites" folder (optional) | One `item` per favorite photo/video, newest first (`POST /api/search/metadata` with `isFavorite: true`) |
| `onthisday` | "On this day" folder (optional) | One `item` per photo/video captured on today's month and day (local capture date) in an earlier year, newest first (one `POST /api/search/metadata` with `takenAfter`/`takenBefore` per year) |
| `places` | "Places" folder (optional) | One `container` per country (`place:<country>`), from the timeline listing's reverse-geocoded `exifInfo.country` |
| `place:<country>` | One country | One `container` per city (`place:<country>:<city>`; assets with no city go in an "Other" city `place:<country>:`) |
| `place:<country>:<city>` | One city | That city's items |
| `random` | "Random" folder (optional) | Up to 100 randomly picked photos/videos from the timeline listing, reshuffled hourly |
| `asset:<id>` | One photo/video | N/A (items have no children); `BrowseMetadata` returns the item itself |

Neither "People" nor "Timeline" report a `childCount` at the root (both
omit the DIDL-Lite attribute, `-1`): for "Timeline" specifically, getting
an accurate count would mean paginating through every asset in the
library just to render the root listing, which doesn't scale for large
libraries - same reasoning as the People folder below, just applied to
the whole library instead of per-person.

The timeline groups (`dlna/timeline.go`) are computed in memory from the
same full timeline listing, bucketed by each asset's capture date
(`fileCreatedAt`, UTC). Their titles (`2024`, `2024-05`) double as their
sort keys, so they come out chronological even on TVs that sort
everything by title themselves; the flat default is unchanged.

Listing responses from Immich are kept in memory for
`LISTING_CACHE_SECONDS` (default 30) by `listingCache`
(`dlna/listcache.go`): a TV typically sends `BrowseMetadata` for a
folder and then pages through its children with several
`BrowseDirectChildren` calls, and without this each of those re-fetched
the complete listing - for the timeline of a large library, that meant
paging through every asset Immich has once per page shown. Concurrent
loads of the same listing share one Immich request, failed loads are
never cached, and each account's entries are kept apart. With
`LISTING_CACHE_SECONDS=0`, every `Browse` hits Immich live. (The photo
and video *bytes* are cached separately, on disk - see
[Caching](#caching).)

Failed `Browse`/`Search` calls are answered with a UPnP SOAP fault (HTTP
500 with a `UPnPError` detail, as the UPnP Device Architecture
requires), built by `writeBrowseError` (`dlna/soap.go`): `701 No such
object` for an unknown or malformed ObjectID, or when Immich answers 404
for it (a deleted album, a stale ID remembered by the TV), and `501
Action Failed` when Immich is unreachable or errors. Bare HTTP error
pages are something several TV stacks treat as the whole server being
broken. SOAP request bodies are capped at 64 KB (`readSOAPBody`).

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
but some DLNA clients (Samsung's `SEC_HHP` stack, used across many TV
generations) only undo `&lt;`/`&gt;`/`&amp;` before treating the result
as raw XML rather than doing real entity decoding, so a `&#34;`-escaped
quote breaks every attribute in the embedded DIDL-Lite (`id=&#34;0&#34;`
isn't valid attribute syntax) and the client silently treats the
container as unbrowsable. The symptom is that the client fetches the
description fine, calls `BrowseMetadata` on root, and simply never calls
`BrowseDirectChildren` - worth knowing if you're chasing a similar
"browses the root but nothing below it" report, since the response still
looks like well-formed XML at a glance.

### Multiple Immich accounts

With a single `IMMICH_API_KEY` (the default), the table above is the whole
`ObjectID` space. Setting `IMMICH_API_KEYS` to more than one key changes the
root: instead of listing "Albums"/"People"/"Timeline" directly, root now
lists one container per configured account, `user:<idx>` (`idx` is the
account's position in `IMMICH_API_KEYS`), titled with that account's own
Immich display name (fetched via `GET /api/users/me` for each key at
startup, in `main.go`'s `buildUsers` - this is also why a
misconfigured/unauthorized key in `IMMICH_API_KEYS` fails startup
immediately rather than silently producing an unlabeled folder). Browsing
into `user:<idx>` then behaves exactly like the single-account root did,
but every `ObjectID` underneath it carries a `user:<idx>:` prefix so a
later `Browse` call knows which account's client to use -
`user:0:albums`, `user:0:album:<id>`, `user:0:person:<id>`,
`user:0:timeline`, `user:0:asset:<id>`, and so on. This is implemented by
`browseUserScope` in `dlna/contentdirectory.go`, which is the entire
single-account Browse implementation generalized over an injected
`childPrefix`; `browseMultiUser` handles only the extra top layer
(root and `user:<idx>`) and delegates into it once per account.

Each asset's `<res>` URL is scoped the same way: with one account it's
`/media/{assetID}` as always, but with multiple accounts it's
`/media/{idx}/{assetID}`, so `dlna/server.go`'s media handler
(`parseMediaPath`) knows which account's API key to download with - an
asset ID alone isn't enough, since each account can only download assets
it has permission to see. Album and person cover URLs (`albumArtURI`,
described further down in this section) and a video's generated
thumbnail (`GET /thumbnail/{assetID}`, or `/thumbnail/{idx}/{assetID}`
with multiple accounts) go through the same scoping, via
`mediaURL`/`personThumbnailURL`/`thumbnailURL`. The disk cache is still
keyed by asset ID alone (see [Caching](#caching)): Immich asset/person
IDs are UUIDs, globally unique across every account on the same server,
so no per-account cache namespacing is needed.

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

None of the above helps on a client that never sends `SortCriteria` and
instead always re-sorts and displays whatever `Browse` returns by
`dc:title` itself, with no on-device option to choose date order instead
(Samsung's Smart TV browser is the known case). `TITLE_DATE_PREFIX`
(`config.Config.TitleDatePrefix`) works around that at the title level
instead of the sort level: when set, `assetTitle` in
`dlna/contentdirectory.go` prefixes every photo/video's `dc:title` with
its capture date (`2024-05-01 13:04:05 IMG_1234.jpg`), so a client
sorting alphabetically by title ends up chronological anyway. An asset
with a missing/unparseable `fileCreatedAt` keeps its bare filename
(nothing sortable to prefix with). Album/person container titles are
unaffected - only leaf photo/video items go through `assetTitle`.

That default order is oldest-first, because a plain calendar date string
sorts in ascending order under simple alphabetical/ASCII comparison - the
only kind of sort a client that ignores `SortCriteria` ever does - and
there's no way to make a *readable* date sort in reverse under that same
comparison. `TITLE_DATE_PREFIX_DESC`
(`config.Config.TitleDatePrefixDescending`, only meaningful alongside
`TITLE_DATE_PREFIX`) works around that by leading the title with a
zero-padded countdown instead: `farFutureUnix - capturedAt.Unix()` (seconds
until a fixed instant far beyond any real photo, `9999999999` /
2286-11-20 UTC), so a newer asset gets a smaller number and sorts first,
followed by the same human-readable date and filename as before, e.g.
`8285431354 2024-05-01 13:04:05 IMG_1234.jpg`.

Independently of either `TITLE_DATE_PREFIX` setting, `buildAssetItem`
also populates each item's `<dc:date>` element (`YYYY-MM-DD`, from the
same `Asset.CapturedAt`) and its `<res>` element's `size` attribute (from
Immich's `exifInfo.fileSizeInByte`) whenever Immich provides them, and
omits both otherwise rather than rendering a placeholder. Without them, a
DLNA client that shows a per-item info readout (Samsung's Smart TV
browser again) has nothing to read and falls back to displaying "0
bytes" and the Unix epoch ("Jan 1 1970") instead of leaving the fields
blank. Search results only include `exifInfo` when asked for it, so
every `POST /api/search/metadata` request sets `withExif: true`
(`mergePage` in `immich/client.go`). `size` is also omitted whenever the
served bytes aren't the original file - a photo served as Immich's
preview (`PHOTO_SOURCE`), a video served as Immich's playback version
(`VIDEO_SOURCE=transcoded`), or a JPEG/PNG with `MAX_RESOLUTION` set,
which may be downscaled - since a renderer that trusts `size` over
`Content-Length` could otherwise cut the image off or wait for bytes
that never come. (Photos whose EXIF orientation gets baked in are
re-encoded too, so their served size can still differ slightly; that's
unknowable before downloading them.)

A handful of other details matter for Samsung TVs specifically (found by
diffing wire traffic against a real minidlna instance on the same TV,
which is a useful technique if you run into similar client-specific
quirks): the DIDL-Lite root element declares
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
the TV to actually display the photo or play the video. It also includes
an `<upnp:albumArtURI>` tag: without it, media browsers like Home
Assistant's list titles but show a placeholder icon instead of a
thumbnail (they don't fall back to `<res>` for previews). `buildAssetItem`
(`dlna/contentdirectory.go`) picks the `<upnp:class>`
(`object.item.imageItem.photo` vs `object.item.videoItem.movie`) and the
albumArtURI target based on `Asset.IsVideo()`:

- **Photos** use their own `/media/<assetID>` URL as the albumArtURI too
  - the photo is its own thumbnail. Which bytes that URL serves depends
  on `PHOTO_SOURCE` (see [Photo formats](#photo-formats-photo_source)):
  a photo served as Immich's JPEG preview is advertised as `image/jpeg`
  with `DLNA.ORG_CI=1` and no `size`, since those describe the original
  file, not what the TV will receive.
- **Videos** use `http://<host>/thumbnail/<assetID>` instead, since a
  video file can't be decoded as a preview image the way a photo can.
  That endpoint (`dlna/server.go`'s `handleThumbnail`) serves Immich's
  `GET /api/assets/{id}/thumbnail?size=preview` through the same
  cache/fetch-queue path as `/media/` (see
  [Media streaming](#media-streaming)), cached under `thumb:<assetID>` -
  a TV scrolling through a video-heavy album requests these as rapidly
  as photo thumbnails.

Every `<res>` element's `protocolInfo` carries the DLNA fourth field
minidlna sends (`dlnaFeatures` in `dlna/didl.go`):
`DLNA.ORG_OP=01;DLNA.ORG_CI=<0|1>;DLNA.ORG_FLAGS=<flags>`. `OP=01`
advertises byte-range seeking - every media URL supports `Range` -
which several Samsung and LG renderers require before they show a seek
bar or allow seeking at all; `FLAGS` marks images as interactive and
video as streaming transfers. No `DLNA.ORG_PN` profile is given: the
right JPEG profile depends on pixel dimensions (`JPEG_LRG` tops out at
4096x4096, smaller than many phone photos), and a wrong profile is worse
than none. The same field is sent as the `contentFeatures.dlna.org`
header on `/media/` and `/thumbnail/` responses, together with
`transferMode.dlna.org` (`Streaming` for video, `Interactive` for
images). Video items also carry `duration` (from Immich's `duration`,
e.g. `0:01:05.250`) and `resolution` (from `exifImageWidth`/`Height`)
so TVs can show a video's length and seek bar before playback starts.
Photos get no `resolution`: orientation fixing and `MAX_RESOLUTION` can
change their served dimensions.

Album and person `<container>` elements carry the same
`<upnp:albumArtURI>` tag when a cover image is available, so folder
views get a real thumbnail instead of a generic folder icon:

- **Albums** reuse `albumThumbnailAssetId` from Immich's own album
  response (`GET /api/albums` and `GET /api/albums/{id}`) - it's a
  regular asset ID, so the URL is `/media/<thumbnailAssetID>` (built by
  `albumArtURI`/`mediaURL`), the same endpoint photo items use - or
  `/media/<idx>/<thumbnailAssetID>` with multiple `IMMICH_API_KEYS`
  configured (see [Multiple Immich accounts](#multiple-immich-accounts)).
  An album with no assets has no thumbnail asset, so `albumArtURI` is
  omitted for it.
- **People** don't have an asset-backed thumbnail; Immich instead serves
  a generated face-crop image directly from
  `GET /api/people/{id}/thumbnail`. The proxy fronts this at
  `GET /media/person/{personID}` (`Server.handlePersonThumbnail` in
  `dlna/server.go`, via `Client.GetPersonThumbnail`, built by
  `personArtURI`/`personThumbnailURL` - same `/media/person/<idx>/<id>`
  scoping as albums with multiple accounts configured), cached under the
  key `"person:<id>"` so it can never collide with an asset cached under
  a plain asset ID. `albumArtURI` is omitted for a person with no
  `thumbnailPath` (Immich hasn't generated a face crop for them yet).

## Media streaming

`GET /media/{assetID}` is a plain (non-SOAP) HTTP endpoint that serves
the actual photo/video bytes. It's handled in `dlna/server.go` and
behaves differently depending on whether the disk cache is enabled (the
default), and whether the asset is a photo or a video.

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

- **Cache hit** (photo or video): the file is opened from `CACHE_DIR` and
  served via Go's `http.ServeContent`, which handles `Range` requests,
  `ETag`, and `Last-Modified` automatically. No Immich call happens at
  all.
- **Cache miss, photo:** the proxy downloads the *complete* original from
  Immich (ignoring any `Range` header on the inbound request - it always
  wants the whole file), normalizes its EXIF orientation and downscales
  it as configured (see [Orientation](#orientation) and
  [Downscaling](#downscaling)), writes the result to disk, then serves it
  from the newly written file the same way as a cache hit. This means
  the very first view of a photo waits for the full download before any
  bytes reach the TV; subsequent views are effectively instant.
- **Cache miss, video:** `isVideoMimeType` (checked against the
  `Content-Type` Immich's download response carries) takes the request
  off the photo path above. Videos are never buffered into memory or
  decoded, and the TV isn't made to wait for the whole (possibly
  multi-GB) file either - many TVs give up long before that. Instead,
  `proxyRange` re-requests the original from Immich with the TV's own
  `Range` header forwarded (`Client.OpenOriginalRange`) and relays the
  status (`200`/`206`/`416`) and the headers a player needs to seek
  (`Content-Length`, `Content-Range`, `Accept-Ranges`), so playback and
  seeking start immediately. Meanwhile the full download already
  started is handed to `fillCacheInBackground`, detached from the
  request, which streams it into `cache.Put` - so a TV that only probes
  the first few bytes or stops playback early still ends up with the
  video cached, and the next playback is served from disk like any
  cache hit. The cost is that the first playback briefly downloads the
  video from Immich twice in parallel. Video playback releases its
  `fetchSem` slot right away (see below): those slots exist to protect
  Immich from thumbnail bursts, not to cap how many TVs can play at
  once.
- **Cache disabled** (`DISABLE_CACHE=true`), photo: same download,
  orientation fix, and optional downscale as a photo cache miss above,
  but the result is served straight from memory instead of being written
  to disk.
- **Cache disabled, video:** proxied through with `Range` exactly like
  an uncached video above, just without the background cache fill - so
  seeking works in this mode too.

Concurrent cache misses for the same key share one download: the first
request registers a *flight* (`Server.flights`, `joinFlight`) and the
others wait for it and then serve from the freshly written cache file,
rather than each downloading the same file again (a TV and its preview
pane, or two TVs, asking for the same photo). If the key turns out to be
a video, the waiters stop waiting as soon as that's known and proxy
their own request through as above, instead of waiting for the whole
file. This needs the disk cache - with `DISABLE_CACHE=true` there's
nowhere to share the result, so each request fetches on its own.

A download that Immich answers with `404` (asset deleted, or not visible
to that account's API key) is answered with `404`, not `502`.

`GET /media/person/{personID}` (or `/media/person/{idx}/{personID}` with
multiple `IMMICH_API_KEYS` configured - see
[Multiple Immich accounts](#multiple-immich-accounts)) is a sibling
endpoint for person cover thumbnails (see
[Container album art](#3-control-contentdirectory-browse) above), and
`GET /thumbnail/{assetID}` one for video preview thumbnails. Both share
the same cache-hit/cache-miss/cache-disabled/dedup flow -
`Server.serveMedia` implements the common path for all three handlers,
parameterized by a `mediaSource` - except they fetch from
`Client.GetPersonThumbnail`/`GetAssetThumbnail` instead of
`Client.DownloadOriginal`, are cached under `person:<id>`/`thumb:<id>`
so they never collide with an asset's own bytes, and skip the
orientation-fix/downscale step: Immich already generates them as small,
correctly-oriented images, so there's nothing to normalize.

### Download timeouts and cancellation

The binary downloads behind `/media/*` and `/thumbnail/*`
(`Client.DownloadOriginal`, `OpenOriginalRange`, `GetAssetThumbnail`,
`GetPersonThumbnail`)
go through `immich.Client.Stream`, a separate `http.Client` from the one
used for JSON API calls. The JSON client has a 30s overall timeout; the
streaming one deliberately doesn't, because `http.Client.Timeout` also
covers reading the response body - a multi-GB video that takes longer
than 30s to transfer would otherwise be cut off mid-download and never
make it into the cache. Instead:

- Immich must start answering (response headers) within 30s
  (`ResponseHeaderTimeout`).
- A single read from the body that blocks for more than 30s
  (`stallTimeout`) aborts the download. The timer only runs *while* a
  read is in progress, so a paused TV applying TCP backpressure on the
  `DISABLE_CACHE=true` pass-through path never counts as a stall; a slow
  but progressing download is never interrupted.
- Every photo/thumbnail download follows the inbound request's
  lifetime, so when the DLNA client hangs up (e.g. a TV scrolled past a
  thumbnail), the Immich download is cancelled too and its `fetchSem`
  slot freed, rather than finishing a download nobody is waiting for; a
  partially downloaded file is discarded (`cache.Put` removes its temp
  file on error) and simply fetched again on the next request. The one
  exception is a video's background cache fill, which is deliberately
  detached from the request that started it (see above) - it's bounded
  by the stall timeout alone. The proxied playback request itself
  follows the TV's connection like any other.

### ID validation

Asset, album and person IDs arrive from unauthenticated DLNA clients -
in `/media/`, `/media/person/` and `/thumbnail/` URL paths and in Browse
`ObjectID`s - and are spliced into Immich API paths requested with this
proxy's API key, and into cache filenames. `immich.ValidID` (letters,
digits and `-` only, which every Immich UUID satisfies) is checked
before any of them is used; anything else gets a `404` without Immich
ever being called. Without it, a crafted ID such as `..%2F..%2Fusers`
could make the proxy fetch and relay arbitrary Immich GET endpoints. The
client additionally `url.PathEscape`s every ID it puts into a path, as
defense in depth.

### Bounding concurrent Immich fetches

A TV rapidly scrolling through a large album fires off a `/media/{id}`
request per thumbnail it renders; if most of those are cache misses (a
first browse, or `DISABLE_CACHE=true`), that's a burst of simultaneous
downloads that can overwhelm Immich. `Server.fetchSem`
(`dlna/server.go`), a buffered channel sized by `MEDIA_FETCH_CONCURRENCY`
(default 4), caps how many of those cache-miss fetches (`/media/`,
`/media/person/` and `/thumbnail/` alike) run at once - a slot is held
only while bytes are actually coming from Immich (released before
decode/resize/cache write for photos, and as soon as a video is
identified), so browsing an already-cached album stays unthrottled and
video playback never blocks thumbnails. A request that can't get a slot within 30s
(`fetchQueueTimeout`) gives up and responds `503` rather than queuing
indefinitely or piling onto Immich once a slot frees up long after the TV
has moved on.

## Photo formats (`PHOTO_SOURCE`)

DLNA only requires renderers to support JPEG, and in practice PNG is the
only other photo format TVs reliably display. iPhones shoot HEIC by
default, and Immich libraries also hold WebP, TIFF, RAW-derived and
other formats that most TVs show as a broken thumbnail. Immich already
generates a JPEG preview of every photo (`GET
/api/assets/{id}/thumbnail?size=preview`, by default 1440px on the
long edge), so `PHOTO_SOURCE` (`servesPreview` in `dlna/photosource.go`)
picks which bytes `/media/` serves for a photo:

- `auto` (default): JPEG/PNG originals as-is, everything else as the
  preview. An asset with no MIME type in Immich's metadata keeps the
  original.
- `original`: always the original file (the behavior before this option existed).
- `preview`: always the preview - smaller and faster on slow TVs or
  networks, at reduced resolution.

Videos are never affected. The decision is made twice with the same
inputs: in Browse, so the item is advertised as `image/jpeg` (a TV
decides from `protocolInfo` whether it can display an item at all), and
in `handleMedia` on a cache miss, which looks the asset up
(`Client.GetAsset`, through the listing cache the Browse that listed it
has usually just filled) and fetches the preview instead of the
original; if that lookup fails, it falls back to the original. The
result is cached under the asset ID like any other `/media/` response,
so changing `PHOTO_SOURCE` only affects photos not yet cached - clear
`CACHE_DIR` after changing it. Immich's preview format must be left at
its default, JPEG (Immich → Administration → Settings → Image Settings);
a WebP preview would defeat the point.

## Video formats (`VIDEO_SOURCE`)

The proxy never transcodes video itself, but Immich does: under its own
transcoding policy (Administration → Settings → Video Transcoding;
H.264/AAC in MP4 by default) it keeps a transcoded copy of videos whose
codec or container isn't in its accepted lists, and serves it at `GET
/api/assets/{id}/video/playback` (falling back to the original where it
didn't transcode). With `VIDEO_SOURCE=transcoded`, `/media/` serves that
endpoint for videos - both the full download that fills the cache
(`Client.DownloadPlayback`) and the proxied, `Range`-forwarding request
that starts playback (`Client.OpenPlaybackRange`) - so a TV that can't
play e.g. HEVC or MKV gets Immich's H.264 MP4 instead. Browse advertises
such items as `video/mp4` with `DLNA.ORG_CI=1`, without `size` or
`resolution` (Immich's target resolution, 720p by default, usually
differs from the original's); `duration` is kept. The default,
`original`, serves the original file as before. To make every video
playable, set Immich's transcode policy to what your TV needs (e.g.
"All videos" for maximum compatibility, at the cost of Immich
transcoding everything). Like `PHOTO_SOURCE`, the choice shares the
original's cache key, so clear `CACHE_DIR` after changing it.

The decision per asset (`mediaVariantFor` in `dlna/videosource.go`)
combines both settings with one asset lookup: a video under
`transcoded` gets the playback version, a photo `PHOTO_SOURCE` selects
gets the preview, everything else the original.

## Optional folders (`EXTRA_FOLDERS`)

Besides Albums/People/Timeline, the root can list optional folders,
chosen and ordered by `EXTRA_FOLDERS` (default `favorites,onthisday`;
`none` for none) - see `dlna/extrafolders.go`:

- **Favorites** (`favorites`): every asset marked as a favorite in
  Immich, newest first - its own `isFavorite` metadata search.
- **On this day** (`onthisday`): assets captured on today's month and
  day in earlier years, newest first - the same selection as Immich's
  own "On this day" memories, without needing the `memory.read`
  permission or Immich's memory-generation job. It does *not* filter the
  full timeline listing: paging through a large library (33,000 assets
  took over two minutes against Immich's demo server) takes longer than
  many TVs wait for a Browse response, and a TV that times out just
  shows the folder empty. Instead `onThisDaySearch` looks up the
  library's oldest asset (one single-result search) and then runs one
  `takenAfter`/`takenBefore` search per earlier year, four at a time -
  the same demo library answers in under two seconds. Each search spans
  the date in every time zone (UTC-12 to UTC+14) and keeps the assets
  whose *local* capture date (`localDateTime`, the camera's wall clock)
  is that day, so a photo taken at 00:30 on 1 October in Berlin counts
  for 1 October, as in Immich. The result is cached per day through the
  listing cache. "Today" is the server's local date: set `TZ` (e.g.
  `TZ=Europe/Berlin`); the binary embeds the time zone database
  (`time/tzdata`), since the scratch image has none, so `TZ` works in
  Docker too.
- **Places** (`places`): country → city → items, from Immich's
  reverse-geocoded `exifInfo.country`/`city`. Assets without a country
  (no GPS data, or not geocoded) don't appear. Country and city names
  are query-escaped in the ObjectIDs (`place:<country>:<city>`), so a
  `:` in a name can't be mistaken for the separator.
- **Random** (`random`): up to 100 assets picked at random, seeded by
  the current hour (and account). A fresh pick on every Browse call
  would break paging - a TV fetches a folder in several pages and would
  get duplicates and gaps - so the pick is stable for an hour, then
  reshuffles.

Places and Random are computed from the same timeline listing the
Timeline folder uses (and the listing cache holds), so they cost no
extra Immich requests beyond it - but, like the Timeline, they need the
whole library listed once, which on a very large library can exceed a
TV's Browse timeout until the listing is cached. None of the optional folders report
a `childCount` at the root, for the same reason as the Timeline.

## SystemUpdateID

ContentDirectory's `SystemUpdateID` (returned by `GetSystemUpdateID` and
as `UpdateID` in every Browse response) tells clients whether their
cached view of the server is stale; some TVs keep showing an old album
list until it changes. `updateTracker` (`dlna/updateid.go`) starts it at
1 and bumps it whenever a freshly fetched album or people listing
(i.e. not a listing-cache hit) differs from the previous one -
fingerprinting each album's ID, name, asset count and cover and each
person's ID, name and face thumbnail. Since Immich sends no change
notifications and the proxy doesn't implement UPnP eventing, a
`GetSystemUpdateID` poll re-lists every account's albums and people
itself (through the listing cache, so at most once per
`LISTING_CACHE_SECONDS`), letting a polling TV notice changes even when
nobody is browsing.

## Health check

`GET /healthz` answers `200 ok` when the proxy is up and Immich answers
`GET /api/server/ping` (unauthenticated, 5s timeout), `503` otherwise.
The Docker image's `HEALTHCHECK` runs `immich-dlna-proxy healthcheck`,
a mode of the same binary (the scratch image has no curl/wget) that
requests `/healthz` on the port from `LISTEN_ADDR` and exits 0 or 1, so
Docker and Unraid show the container as unhealthy when Immich can't be
reached. `/healthz` requests are only logged with `DEBUG=true`.

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
  `<assetID>.type` (a one-line MIME type sidecar). Person cover
  thumbnails use the key `person:<id>`, video preview thumbnails
  `thumb:<id>`.
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
- `Get` and `Put` hand back an already-open file rather than a path, so
  an eviction sweep deleting the file between lookup and open can't turn
  a hit into an error - an unlinked file stays readable through an open
  handle. That also means an entry bigger than the whole budget (which
  the sweep deletes right after writing it) is still served once.

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

- **Video transcoding by the proxy itself.** The proxy never transcodes,
  remuxes or transrates anything. With `VIDEO_SOURCE=transcoded` it
  serves the version Immich transcoded (see
  [Video formats](#video-formats-video_source)); otherwise, or where
  Immich didn't transcode a video, a TV that can't decode the original
  codec/container simply can't play it.
- **Non-photo/video asset types.** Only `type == "IMAGE"` and
  `type == "VIDEO"` assets are ever listed or served; anything else
  Immich might report is skipped.
- **Unnamed people.** Immich creates a Person for every detected face
  cluster, including ones you haven't confirmed/named yet. Only named
  people show up as folders - there's no "unknown faces" browsing.
- **Image format conversion by the proxy itself.** Non-JPEG/PNG photos
  are served as Immich's own JPEG preview (see
  [Photo formats](#photo-formats-photo_source)), never converted
  locally; JPEG/PNG can be downscaled (see `MAX_RESOLUTION` below).
- **Persistent listing cache, eventing.** Listings are only kept in
  memory for `LISTING_CACHE_SECONDS`. There's no UPnP eventing (GENA):
  clients only learn about changes by polling `GetSystemUpdateID` (see
  [SystemUpdateID](#systemupdateid)).
- **Authentication/authorization at the DLNA layer.** Anyone who can
  reach the proxy's HTTP port on your LAN can browse and view all
  albums visible to the configured API key. There's no per-client access
  control - this mirrors how DLNA works in general (it has no built-in
  auth), so treat it the same as any other LAN media server.
