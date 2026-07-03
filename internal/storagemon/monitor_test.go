package storagemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPickRandomSnappyInDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.snappy", "b.snappy", "note.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := pickRandomSnappyInDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(got)
	if base != "a.snappy" && base != "b.snappy" {
		t.Fatalf("unexpected pick: %s", got)
	}
}

func TestPickRandomSnappyWithCache(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "batch-1")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f.snappy"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	var cache subdirCache

	got, err := pickRandomSnappy(root, &cache)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "f.snappy" {
		t.Fatalf("unexpected file: %s", got)
	}
	if len(cache.subs) != 1 {
		t.Fatalf("expected cached subdir, got %v", cache.subs)
	}
}

func TestDistinctRandomIndices(t *testing.T) {
	idx := distinctRandomIndices(100, 16)
	if len(idx) != 16 {
		t.Fatalf("want 16 indices, got %d", len(idx))
	}
	seen := make(map[int]bool)
	for _, i := range idx {
		if i < 0 || i >= 100 {
			t.Fatalf("index out of range: %d", i)
		}
		if seen[i] {
			t.Fatalf("duplicate index: %d", i)
		}
		seen[i] = true
	}
}
