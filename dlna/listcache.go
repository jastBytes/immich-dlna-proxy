package dlna

import (
	"strconv"
	"sync"
	"time"

	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// listingCache briefly remembers Immich listing responses (albums, people,
// an album's or person's assets, the timeline), for
// config.Config.ListingCacheTTL. DLNA clients page through a container
// with many small Browse calls (StartingIndex/RequestedCount) and
// typically BrowseMetadata the container first - without this, every one
// of those calls re-fetched the complete listing from Immich, which for
// the timeline of a large library means paging through every asset
// Immich has, once per page the TV shows. Concurrent loads of the same key
// share one Immich request. Failed loads are never cached.
//
// A zero TTL disables it entirely: every call goes straight to Immich.
type listingCache struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]*listingEntry
}

type listingEntry struct {
	done    chan struct{} // closed once val/err are set
	val     any
	err     error
	expires time.Time
}

func newListingCache(ttl time.Duration) *listingCache {
	return &listingCache{ttl: ttl, entries: map[string]*listingEntry{}}
}

// cachedListing returns the cached value for key, or calls load (once,
// however many callers ask concurrently) and caches its result for c.ttl.
// Callers must treat the returned value as read-only - it's shared.
func cachedListing[T any](c *listingCache, key string, load func() (T, error)) (T, error) {
	if c == nil || c.ttl <= 0 {
		return load()
	}

	c.mu.Lock()
	now := time.Now()
	for k, e := range c.entries {
		if e.isDone() && now.After(e.expires) {
			delete(c.entries, k)
		}
	}
	e, ok := c.entries[key]
	if !ok {
		e = &listingEntry{done: make(chan struct{})}
		c.entries[key] = e
	}
	c.mu.Unlock()

	if ok {
		<-e.done
		if e.err != nil {
			var zero T
			return zero, e.err
		}
		return e.val.(T), nil
	}

	val, err := load()
	e.val, e.err = val, err
	e.expires = time.Now().Add(c.ttl)
	close(e.done)
	if err != nil {
		c.mu.Lock()
		if c.entries[key] == e {
			delete(c.entries, key)
		}
		c.mu.Unlock()
	}
	return val, err
}

func (e *listingEntry) isDone() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// cachedClient is the subset of immich.Client that Browse uses, fronted
// by the listing cache. prefix keeps different accounts' entries apart.
type cachedClient struct {
	client   *immich.Client
	cache    *listingCache
	prefix   string
	idx      int
	updates  *updateTracker
	timeline *timelineStore
}

func (s *Server) cachedClient(userIdx int) cachedClient {
	idx := userIdx
	if idx < 0 {
		idx = 0
	}
	return cachedClient{client: s.users[idx].Client, cache: s.listings, prefix: strconv.Itoa(idx) + "|", idx: idx, updates: s.updates, timeline: s.timeline}
}

// ListAlbums also feeds the SystemUpdateID tracker whenever the listing is
// actually fetched from Immich (not on a listing-cache hit).
func (c cachedClient) ListAlbums() ([]immich.Album, error) {
	return cachedListing(c.cache, c.prefix+"albums", func() ([]immich.Album, error) {
		albums, err := c.client.ListAlbums()
		if err == nil {
			c.updates.observe(c.prefix+"albums", hashAlbums(albums))
		}
		return albums, err
	})
}

func (c cachedClient) GetAlbum(id string) (*immich.Album, error) {
	return cachedListing(c.cache, c.prefix+"album:"+id, func() (*immich.Album, error) { return c.client.GetAlbum(id) })
}

func (c cachedClient) GetAlbumAssets(id string) ([]immich.Asset, error) {
	return cachedListing(c.cache, c.prefix+"album-assets:"+id, func() ([]immich.Asset, error) { return c.client.GetAlbumAssets(id) })
}

// ListPeople feeds the SystemUpdateID tracker like ListAlbums.
func (c cachedClient) ListPeople() ([]immich.Person, error) {
	return cachedListing(c.cache, c.prefix+"people", func() ([]immich.Person, error) {
		people, err := c.client.ListPeople()
		if err == nil {
			c.updates.observe(c.prefix+"people", hashPeople(people))
		}
		return people, err
	})
}

func (c cachedClient) ListFavoriteAssets() ([]immich.Asset, error) {
	return cachedListing(c.cache, c.prefix+"favorites", c.client.ListFavoriteAssets)
}

func (c cachedClient) GetPerson(id string) (*immich.Person, error) {
	return cachedListing(c.cache, c.prefix+"person:"+id, func() (*immich.Person, error) { return c.client.GetPerson(id) })
}

func (c cachedClient) GetPersonAssets(id string) ([]immich.Asset, error) {
	return cachedListing(c.cache, c.prefix+"person-assets:"+id, func() ([]immich.Asset, error) { return c.client.GetPersonAssets(id) })
}

// ListTimelineAssets is served from the timeline store, not the listing
// cache: the full listing is too slow to fetch while a TV waits - see
// timelineStore.
func (c cachedClient) ListTimelineAssets() ([]immich.Asset, error) {
	return c.timeline.get(c.idx, c.client)
}

func (c cachedClient) GetAsset(id string) (*immich.Asset, error) {
	return cachedListing(c.cache, c.prefix+"asset:"+id, func() (*immich.Asset, error) { return c.client.GetAsset(id) })
}
