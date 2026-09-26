package cache

import (
	"io"
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

	f, err := c.Put("asset1", "image/jpeg", strings.NewReader("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil || string(data) != "hello world" {
		t.Fatalf("unexpected cached content: %q err=%v", data, err)
	}

	got, mime, _, ok := c.Get("asset1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	defer func() { _ = got.Close() }()
	if mime != "image/jpeg" {
		t.Fatalf("expected mime image/jpeg, got %s", mime)
	}
	if data, _ := io.ReadAll(got); string(data) != "hello world" {
		t.Fatalf("Get content = %q", data)
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
		f, err := c.Put(id, "image/jpeg", strings.NewReader("12345"))
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
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

	if f, _, _, ok := c.Get("a"); ok {
		_ = f.Close()
		t.Error("expected oldest entry 'a' to have been evicted")
	}
	if f, _, _, ok := c.Get("c"); !ok {
		t.Error("expected newest entry 'c' to still be cached")
	} else {
		_ = f.Close()
	}
}

// An entry bigger than the whole budget is evicted by the sweep Put kicks
// off right away - but the handle Put returns must still serve it.
func TestPutReturnsReadableFileEvenIfEvictedImmediately(t *testing.T) {
	c, err := New(t.TempDir(), 3)
	if err != nil {
		t.Fatal(err)
	}
	f, err := c.Put("big", "video/mp4", strings.NewReader("too large for the budget"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(c.mainPath("big")); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "too large for the budget" {
		t.Fatalf("read after eviction = %q, err=%v", data, err)
	}
}
