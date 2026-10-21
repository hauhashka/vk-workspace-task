package cache

import (
	"os"
	"testing"
)

func TestRoundTripAndCorruption(t *testing.T) {
	c, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if e, err := c.Get("u"); e != nil || err != nil {
		t.Fatalf("empty cache: %v %v", e, err)
	}
	want := Entry{URL: "u", ETag: `"1"`, Link: `<n>; rel="next"`, Body: `[{"a":"б"}]`}
	if err := c.Put(want); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get("u")
	if err != nil || got == nil || *got != want {
		t.Fatalf("got %+v, %v", got, err)
	}
	if err := os.WriteFile(c.path("u"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if e, err := c.Get("u"); e != nil || err == nil {
		t.Fatalf("corrupted entry must be reported: %v %v", e, err)
	}
}

func TestNilCache(t *testing.T) {
	var c *Cache
	if e, err := c.Get("u"); e != nil || err != nil {
		t.Fatal("nil cache must be a miss")
	}
	if err := c.Put(Entry{URL: "u"}); err != nil {
		t.Fatal(err)
	}
}
