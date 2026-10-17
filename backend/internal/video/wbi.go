package video

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// wbiKeyTTL is how long one pair of key file names is trusted. The site rotates
// them without announcement, and every signed call carries the key's effect in
// its `w_rid`; a key that has gone stale fails loudly (a non-zero code) rather
// than silently, and the next search after the TTL fetches a new one.
const wbiKeyTTL = 6 * time.Hour

// mixinKeyEncTab is the fixed permutation applied to the two key file names.
// It is not a secret — it ships in the site's own JavaScript and in every public
// description of the algorithm — so it lives here as data, in the order it is
// applied.
var mixinKeyEncTab = [64]int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
	37, 48, 7, 16, 24, 55, 40, 61, 26, 17, 0, 1, 60, 51, 30, 4,
	22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11, 36, 20, 34, 44, 52,
}

// signingCharsDropped are the characters the algorithm removes from values
// before hashing. They are dropped from the query as well, so that what is
// signed and what is sent are the same string.
const signingCharsDropped = "!'()*"

// mixinKey derives the 32-character signing key from the two file names the nav
// endpoint returns. Both names look like
// `https://i0.hdslb.com/bfs/wbi/<64 hex>.png`; only the stem matters.
func mixinKey(imgURL, subURL string) (string, error) {
	img, err := keyFileName(imgURL)
	if err != nil {
		return "", err
	}
	sub, err := keyFileName(subURL)
	if err != nil {
		return "", err
	}
	raw := img + sub
	if len(raw) < 64 {
		return "", fmt.Errorf("video: signing key is %d characters, want at least 64", len(raw))
	}
	var key strings.Builder
	key.Grow(len(mixinKeyEncTab))
	for _, index := range mixinKeyEncTab {
		key.WriteByte(raw[index])
	}
	return key.String()[:32], nil
}

func keyFileName(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("video: signing key URL: %w", err)
	}
	name := path.Base(parsed.Path)
	if index := strings.LastIndexByte(name, '.'); index >= 0 {
		name = name[:index]
	}
	if name == "" || name == "." || name == "/" {
		return "", fmt.Errorf("video: signing key URL has no file name")
	}
	return name, nil
}

// signQuery renders params as the query string to send, with `w_rid` appended.
//
// The order is fixed by url.Values.Encode — keys sorted, values escaped — and
// the hash is taken over exactly that string plus the key, so a caller must send
// the returned query rather than re-encode the map: an encoding that differs by
// one byte produces a different `w_rid`.
func signQuery(params url.Values, key string) string {
	filtered := make(url.Values, len(params))
	for name, values := range params {
		for _, value := range values {
			filtered.Add(name, strings.Map(dropSigningChar, value))
		}
	}
	query := filtered.Encode()
	sum := md5.Sum([]byte(query + key))
	return query + "&w_rid=" + hex.EncodeToString(sum[:])
}

func dropSigningChar(r rune) rune {
	if strings.ContainsRune(signingCharsDropped, r) {
		return -1
	}
	return r
}

// wbiKeyCache holds the derived signing key until it expires. A failed fetch is
// never stored, so an outage costs one attempt per search rather than six hours
// of failing ones.
type wbiKeyCache struct {
	mu      sync.Mutex
	key     string
	expires time.Time
	now     func() time.Time
}

func (c *wbiKeyCache) get() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key == "" || !c.now().Before(c.expires) {
		return "", false
	}
	return c.key, true
}

func (c *wbiKeyCache) put(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.key = key
	c.expires = c.now().Add(wbiKeyTTL)
}
