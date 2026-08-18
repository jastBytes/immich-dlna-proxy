// Package cache implements a simple disk-backed LRU cache for the original
// photo bytes streamed to DLNA clients. Each cached asset is stored as two
// files: "<assetID>" (the bytes) and "<assetID>.type" (its MIME type).
//
// "Last used" is approximated by the main file's mtime, which is touched on
// every cache hit. Eviction is a straightforward "delete oldest mtime files
// until under the size budget" sweep, run in the background after writes.
package cache

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Cache struct {
	dir      string
	maxBytes int64

	mu sync.Mutex // serializes eviction sweeps
}

func New(dir string, maxBytes int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cache: cannot create dir %s: %w", dir, err)
	}
	return &Cache{dir: dir, maxBytes: maxBytes}, nil
}

func (c *Cache) mainPath(assetID string) string {
	return filepath.Join(c.dir, assetID)
}

func (c *Cache) typePath(assetID string) string {
	return filepath.Join(c.dir, assetID+".type")
}

// Get returns the cache file path, its stored MIME type, and its mtime if
// the asset is cached. On a hit, it also touches the file's mtime so it
// counts as recently used for eviction purposes.
func (c *Cache) Get(assetID string) (path string, mimeType string, modTime time.Time, ok bool) {
	main := c.mainPath(assetID)
	if _, err := os.Stat(main); err != nil {
		return "", "", time.Time{}, false
	}

	typeBytes, err := os.ReadFile(c.typePath(assetID))
	if err != nil {
		// Bytes exist but metadata is missing/corrupt - treat as a miss so
		// it gets re-downloaded and re-written cleanly.
		return "", "", time.Time{}, false
	}

	now := time.Now()
	_ = os.Chtimes(main, now, now)
	_ = os.Chtimes(c.typePath(assetID), now, now)

	return main, string(typeBytes), now, true
}

// Put atomically stores r's contents under assetID along with its MIME
// type, then kicks off a background eviction sweep. It returns the final
// on-disk path of the cached bytes.
func (c *Cache) Put(assetID, mimeType string, r io.Reader) (path string, err error) {
	tmp, err := os.CreateTemp(c.dir, ".tmp-"+assetID+"-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	final := c.mainPath(assetID)
	if err := os.Rename(tmpName, final); err != nil {
		return "", err
	}
	if err := os.WriteFile(c.typePath(assetID), []byte(mimeType), 0o644); err != nil {
		return "", err
	}

	go c.evict()

	return final, nil
}

type entry struct {
	assetID string
	path    string
	size    int64
	mtime   time.Time
}

// evict deletes the least-recently-used cached assets until the total
// cache size is back under maxBytes. It's safe to call concurrently;
// overlapping calls simply serialize on the mutex.
func (c *Cache) evict() {
	if c.maxBytes <= 0 {
		return // unlimited
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	dirEntries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}

	var entries []entry
	var total int64
	for _, de := range dirEntries {
		name := de.Name()
		if de.IsDir() || filepath.Ext(name) == ".type" || strings.HasPrefix(name, ".tmp-") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		entries = append(entries, entry{
			assetID: name,
			path:    filepath.Join(c.dir, name),
			size:    info.Size(),
			mtime:   info.ModTime(),
		})
	}

	if total <= c.maxBytes {
		return
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].mtime.Before(entries[j].mtime) })

	for _, e := range entries {
		if total <= c.maxBytes {
			break
		}
		os.Remove(e.path)
		os.Remove(c.typePath(e.assetID))
		total -= e.size
	}
}
