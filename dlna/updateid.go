package dlna

import (
	"hash/fnv"
	"strconv"
	"sync"

	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// updateTracker maintains ContentDirectory's SystemUpdateID. DLNA clients
// compare it across GetSystemUpdateID calls (and the UpdateID in Browse
// responses) to decide whether their cached view of the server is stale;
// one that never changes lets some TVs keep showing an old album list
// until they're restarted. There's no change notification from Immich,
// so the ID is bumped whenever a freshly fetched album or people listing
// differs from the previous one (see observe).
type updateTracker struct {
	mu   sync.Mutex
	id   uint32
	seen map[string]uint64 // listing key -> hash of its last fetched contents
}

func newUpdateTracker() *updateTracker {
	return &updateTracker{id: 1, seen: map[string]uint64{}}
}

// observe records the hash of a freshly fetched listing, bumping the
// SystemUpdateID if it differs from the last one seen for key. The first
// sighting of a key never bumps it - the server's view hasn't changed from
// anything a client could have seen yet. Safe on a nil tracker.
func (t *updateTracker) observe(key string, h uint64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.seen[key]; ok && prev != h {
		t.id++
		if t.id == 0 { // ui4 wrapped; 0 reads as "unset" to some clients
			t.id = 1
		}
	}
	t.seen[key] = h
}

// current returns the SystemUpdateID as the decimal string SOAP responses
// carry.
func (t *updateTracker) current() string {
	if t == nil {
		return "1"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return strconv.FormatUint(uint64(t.id), 10)
}

// hashAlbums fingerprints what an album listing shows a client: each
// album's ID, name, asset count and cover.
func hashAlbums(albums []immich.Album) uint64 {
	h := fnv.New64a()
	for _, a := range albums {
		_, _ = h.Write([]byte(a.ID + "\x00" + a.AlbumName + "\x00" + strconv.Itoa(a.AssetCount) + "\x00" + a.AlbumThumbnailAssetID + "\x01"))
	}
	return h.Sum64()
}

// hashPeople fingerprints a people listing: each person's ID, name and
// whether they have a face thumbnail.
func hashPeople(people []immich.Person) uint64 {
	h := fnv.New64a()
	for _, p := range people {
		_, _ = h.Write([]byte(p.ID + "\x00" + p.Name + "\x00" + strconv.FormatBool(p.HasThumbnail()) + "\x01"))
	}
	return h.Sum64()
}

// refreshSystemUpdateID re-lists every configured account's albums and
// people (through the listing cache, so at most once per
// LISTING_CACHE_SECONDS) so a GetSystemUpdateID poll can notice changes
// made in Immich even when nobody is browsing. Failures are ignored: the
// ID then simply stays as it was.
func (s *Server) refreshSystemUpdateID() {
	for i := range s.users {
		c := s.cachedClient(i)
		_, _ = c.ListAlbums()
		_, _ = c.ListPeople()
	}
}
