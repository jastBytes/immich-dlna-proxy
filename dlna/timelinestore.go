package dlna

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/jastBytes/immich-dlna-proxy/immich"
)

// timelineFirstLoadWait is how long a Browse waits for an account's very
// first timeline load before giving up with an error. TVs typically wait
// 20-30 seconds for a Browse response; answering a little earlier with a
// SOAP fault lets them show an error (and retry) rather than an empty
// folder. A var so tests can shrink it.
var timelineFirstLoadWait = 15 * time.Second

// errTimelineLoading is returned while an account's timeline is still being
// fetched for the first time.
var errTimelineLoading = errors.New("timeline is still loading from Immich, try again shortly")

// timelineStore keeps each account's full timeline listing (every photo
// and video, newest first) in memory, for the Timeline, Places and Random
// folders.
//
// Fetching that listing means paging through the whole library, which on
// a large one takes longer than TVs wait for a Browse response - a
// 33,000-asset library takes ~40s even at 1,000 assets per page - and a TV
// that times out shows the folder as empty. So the listing is loaded in
// the background at startup (WarmUp) and Browse always answers from the
// copy in memory immediately; once that copy is older than the refresh
// interval (TIMELINE_REFRESH_MINUTES), the next access starts a background
// refresh and keeps serving the previous copy until it completes. New
// photos therefore show up after at most one refresh interval plus one
// fetch. If a refresh fails, the previous copy stays in use.
type timelineStore struct {
	refresh time.Duration

	mu      sync.Mutex
	entries map[int]*timelineEntry
}

type timelineEntry struct {
	assets    []immich.Asset // nil until the first successful load
	fetchedAt time.Time
	loading   bool
	// firstDone is closed when the first load attempt finishes, whether it
	// succeeded or not, waking Browse calls waiting on it.
	firstDone chan struct{}
	firstErr  error
}

// defaultTimelineRefresh applies when no refresh interval is configured
// (config.Load always sets one; this covers hand-built configs).
const defaultTimelineRefresh = 15 * time.Minute

func newTimelineStore(refresh time.Duration) *timelineStore {
	if refresh <= 0 {
		refresh = defaultTimelineRefresh
	}
	return &timelineStore{refresh: refresh, entries: map[int]*timelineEntry{}}
}

// get returns account idx's timeline: the copy in memory if there is one
// (starting a background refresh if it's stale), otherwise it starts the
// first load and waits up to timelineFirstLoadWait for it. The returned
// slice is shared - callers must not modify it.
func (t *timelineStore) get(idx int, client *immich.Client) ([]immich.Asset, error) {
	t.mu.Lock()
	e := t.entry(idx)
	if e.assets != nil {
		if !e.loading && time.Since(e.fetchedAt) >= t.refresh {
			t.startLoadLocked(idx, e, client)
		}
		assets := e.assets
		t.mu.Unlock()
		return assets, nil
	}
	if !e.loading {
		// A failed first attempt left firstDone closed; give the retry a
		// fresh one to wait on.
		select {
		case <-e.firstDone:
			e.firstDone = make(chan struct{})
		default:
		}
		t.startLoadLocked(idx, e, client)
	}
	done := e.firstDone
	t.mu.Unlock()

	select {
	case <-done:
	case <-time.After(timelineFirstLoadWait):
		return nil, errTimelineLoading
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if e.assets == nil {
		return nil, e.firstErr
	}
	return e.assets, nil
}

// warmUp starts loading account idx's timeline in the background, if it
// isn't loaded or loading already.
func (t *timelineStore) warmUp(idx int, client *immich.Client) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e := t.entry(idx); e.assets == nil && !e.loading {
		t.startLoadLocked(idx, e, client)
	}
}

func (t *timelineStore) entry(idx int) *timelineEntry {
	e, ok := t.entries[idx]
	if !ok {
		e = &timelineEntry{firstDone: make(chan struct{})}
		t.entries[idx] = e
	}
	return e
}

// startLoadLocked fetches the timeline in a new goroutine. t.mu must be
// held.
func (t *timelineStore) startLoadLocked(idx int, e *timelineEntry, client *immich.Client) {
	e.loading = true
	go func() {
		start := time.Now()
		assets, err := client.ListTimelineAssets()
		t.mu.Lock()
		defer t.mu.Unlock()
		e.loading = false
		first := e.assets == nil
		if err != nil {
			log.Printf("timeline load for account %d failed after %s: %v", idx, time.Since(start).Round(time.Millisecond), err)
			if first {
				e.firstErr = err
				close(e.firstDone)
			}
			return
		}
		if assets == nil {
			assets = []immich.Asset{} // loaded, just empty - distinct from "not loaded yet"
		}
		e.assets, e.fetchedAt = assets, time.Now()
		debugf("timeline for account %d loaded: %d assets in %s", idx, len(assets), time.Since(start).Round(time.Millisecond))
		if first {
			e.firstErr = nil
			close(e.firstDone)
		}
	}()
}

// WarmUp starts loading every configured account's timeline in the
// background, so the first Browse of the Timeline (or Places/Random)
// folder doesn't have to wait for the whole library to be listed. Call it
// once at startup; it returns immediately.
func (s *Server) WarmUp() {
	for i, u := range s.users {
		s.timeline.warmUp(i, u.Client)
	}
}
