package video

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// The two file names below are the pair the site's own documentation uses for
// the algorithm, so the key and the signature here are checked against a value
// this repository did not produce.
const (
	documentedImgKey = "https://i0.hdslb.com/bfs/wbi/7cd084941338484aae1ad9425b84077c.png"
	documentedSubKey = "https://i0.hdslb.com/bfs/wbi/4932caff0ff746eab6f01bf08b70ac45.png"
	documentedKey    = "ea1db124af3c7062474693fa704f4ff8"
)

func TestMixinKeyFollowsTheDocumentedPermutation(t *testing.T) {
	key, err := mixinKey(documentedImgKey, documentedSubKey)
	if err != nil {
		t.Fatalf("mixinKey() error = %v", err)
	}
	if key != documentedKey {
		t.Fatalf("mixinKey() = %q, want %q", key, documentedKey)
	}
}

func TestMixinKeyRefusesNamesThatCannotCarryAKey(t *testing.T) {
	for _, test := range []struct{ name, img, sub string }{
		{"empty", "", documentedSubKey},
		{"a directory", "https://i0.hdslb.com/bfs/wbi/", documentedSubKey},
		{"a short stem", "https://i0.hdslb.com/bfs/wbi/abc.png", documentedSubKey},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := mixinKey(test.img, test.sub); err == nil {
				t.Fatal("mixinKey() accepted a name it cannot sign with")
			}
		})
	}
}

func TestSignQueryMatchesTheDocumentedExample(t *testing.T) {
	params := url.Values{
		"foo": {"114"},
		"bar": {"514"},
		"zab": {"1919810"},
		"wts": {"1702204169"},
	}
	got := signQuery(params, documentedKey)
	want := "bar=514&foo=114&wts=1702204169&zab=1919810&w_rid=8f6f2b5b3d485fe1886cec6a0be8c5d4"
	if got != want {
		t.Fatalf("signQuery() = %q, want %q", got, want)
	}
}

func TestSignQueryDropsTheCharactersTheAlgorithmIgnores(t *testing.T) {
	// The four characters are dropped from the values before the hash, and the
	// query sent has to be the query hashed — otherwise the site computes a
	// signature over text this client did not send.
	signed := signQuery(url.Values{"keyword": {"a!b'c(d)e*f"}}, documentedKey)

	if !strings.HasPrefix(signed, "keyword=abcdef&w_rid=") {
		t.Fatalf("signQuery() = %q, want the dropped characters gone from the query", signed)
	}
	// Dropping them has to change nothing else: the same value without them is
	// the same request.
	if same := signQuery(url.Values{"keyword": {"abcdef"}}, documentedKey); same != signed {
		t.Errorf("signQuery() = %q for the cleaned spelling, want %q", same, signed)
	}
}

func TestKeyCacheForgetsAKeyWhenItExpires(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	cache := &wbiKeyCache{now: func() time.Time { return now }}
	cache.put("one")

	if key, ok := cache.get(); !ok || key != "one" {
		t.Fatalf("get() = %q, %v; want the key just stored", key, ok)
	}
	now = now.Add(wbiKeyTTL)
	if _, ok := cache.get(); ok {
		t.Error("get() returned a key past its TTL")
	}
}
