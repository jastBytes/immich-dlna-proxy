package dlna

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jastBytes/immich-dlna-proxy/config"
	"github.com/jastBytes/immich-dlna-proxy/immich"
)

func TestListingCacheReusesWithinTTLAndExpires(t *testing.T) {
	c := newListingCache(50 * time.Millisecond)
	var loads int
	load := func() (int, error) { loads++; return loads, nil }

	if v, _ := cachedListing(c, "k", load); v != 1 {
		t.Fatalf("first = %d", v)
	}
	if v, _ := cachedListing(c, "k", load); v != 1 {
		t.Fatalf("second (within TTL) = %d, want cached 1", v)
	}
	time.Sleep(80 * time.Millisecond)
	if v, _ := cachedListing(c, "k", load); v != 2 {
		t.Fatalf("after TTL = %d, want reloaded 2", v)
	}
}

func TestListingCacheDoesNotCacheErrors(t *testing.T) {
	c := newListingCache(time.Minute)
	var loads int
	load := func() (int, error) {
		loads++
		if loads == 1 {
			return 0, errors.New("immich down")
		}
		return 42, nil
	}
	if _, err := cachedListing(c, "k", load); err == nil {
		t.Fatal("expected error")
	}
	if v, err := cachedListing(c, "k", load); err != nil || v != 42 {
		t.Fatalf("after error = %d, %v; want a fresh load", v, err)
	}
}

func TestListingCacheSharesConcurrentLoads(t *testing.T) {
	c := newListingCache(time.Minute)
	var loads atomic.Int32
	release := make(chan struct{})
	load := func() (int, error) { loads.Add(1); <-release; return 7, nil }

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, _ := cachedListing(c, "k", load); v != 7 {
				t.Errorf("got %d", v)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := loads.Load(); n != 1 {
		t.Errorf("loads = %d, want 1", n)
	}
}

func TestListingCacheZeroTTLAlwaysLoads(t *testing.T) {
	c := newListingCache(0)
	var loads int
	load := func() (int, error) { loads++; return loads, nil }
	_, _ = cachedListing(c, "k", load)
	_, _ = cachedListing(c, "k", load)
	if loads != 2 {
		t.Errorf("loads = %d, want 2", loads)
	}
}

// Paging through a container (BrowseMetadata, then several
// BrowseDirectChildren pages) fetches the listing from Immich once.
func TestBrowsePagingReusesCachedListing(t *testing.T) {
	var searches atomic.Int32
	fakeImmich := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search/metadata" {
			http.NotFound(w, r)
			return
		}
		searches.Add(1)
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assets":{"nextPage":null,"items":[
			{"id":"a1","originalFileName":"1.jpg","originalMimeType":"image/jpeg","type":"IMAGE"},
			{"id":"a2","originalFileName":"2.jpg","originalMimeType":"image/jpeg","type":"IMAGE"}]}}`))
	}))
	t.Cleanup(fakeImmich.Close)

	cfg := &config.Config{ImmichURL: fakeImmich.URL, APIKeys: []string{"k"}, FriendlyName: "Test", ListingCacheTTL: time.Minute}
	ts := httptest.NewServer(NewServer(cfg, []UserClient{{Client: immich.New(fakeImmich.URL, "k")}}, nil).Mux())
	t.Cleanup(ts.Close)

	for i := 0; i < 3; i++ {
		if !strings.Contains(didlResult(t, browse(t, ts.URL, "timeline", "BrowseDirectChildren")), `id="asset:a2"`) {
			t.Fatal("timeline listing missing a2")
		}
	}
	if n := searches.Load(); n != 1 {
		t.Errorf("Immich searched %d times, want 1", n)
	}
}
