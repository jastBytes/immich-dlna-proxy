package cache

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPutGetRoundtrip(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, 0) // unlimited
	if err != nil {
		t.Fatal(err)
	}

	path, err := c.Put("asset1", "image/jpeg", strings.NewReader("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hello world" {
		t.Fatalf("unexpected cached content: %q err=%v", data, err)
	}

	gotPath, mime, _, ok := c.Get("asset1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if mime != "image/jpeg" {
		t.Fatalf("expected mime image/jpeg, got %s", mime)
	}
	if gotPath != path {
		t.Fatalf("path mismatch: %s vs %s", gotPath, path)
	}

	if _, _, _, ok := c.Get("does-not-exist"); ok {
		t.Fatal("expected cache miss for unknown asset")
	}
}

func TestEvictionRemovesOldestFirst(t *testing.T) {
	dir := t.TempDir()
	// Budget only large enough for ~2 of the 5-byte payloads we write below.
	c, err := New(dir, 12)
	if err != nil {
		t.Fatal(err)
	}

	for i, id := range []string{"a", "b", "c"} {
		if _, err := c.Put(id, "image/jpeg", strings.NewReader("12345")); err != nil {
			t.Fatal(err)
		}
		// Ensure distinct mtimes so ordering is deterministic across
		// filesystems with coarse mtime resolution.
		time.Sleep(20 * time.Millisecond)
		_ = i
	}

	// eviction runs in a goroutine from Put; give it a moment to finish.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		count := 0
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".type") {
				count++
			}
		}
		if count <= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, _, _, ok := c.Get("a"); ok {
		t.Error("expected oldest entry 'a' to have been evicted")
	}
	if _, _, _, ok := c.Get("c"); !ok {
		t.Error("expected newest entry 'c' to still be cached")
	}
}
