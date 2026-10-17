package book

import (
	"strconv"
	"sync"
)

// keyedLocks is a set of named locks that never waits: a key is either free,
// in which case the caller takes it, or held, in which case the caller is told
// so and goes and does something else.
//
// It is the first of this package's two concurrency guards. It stops this
// process from asking the same model the same question twice — which is a
// second charge and a second write racing the first — and it is deliberately
// not a queue: a request that arrives while a generation is running is refused
// with a state the reader can act on, not parked until a slot frees up.
//
// It is process-local. Two instances would not see each other's keys, and that
// is acceptable because the second guard does not depend on this one:
// book_chapters.outline_revision is checked in the write itself, so the worst
// a second instance does is spend a model call whose answer is then discarded.
// What it cannot do is put a body under the wrong title.
type keyedLocks struct {
	mu   sync.Mutex
	held map[string]struct{}
}

// newKeyedLocks returns an empty lock set.
func newKeyedLocks() *keyedLocks {
	return &keyedLocks{held: make(map[string]struct{})}
}

// try takes key if it is free. The returned release is idempotent, so a caller
// may defer it unconditionally after checking ok.
func (l *keyedLocks) try(key string) (release func(), ok bool) {
	if l == nil {
		return func() {}, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = make(map[string]struct{})
	}
	if _, taken := l.held[key]; taken {
		return func() {}, false
	}
	l.held[key] = struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			delete(l.held, key)
		})
	}, true
}

// outlineKey is one outline generation for one book.
func outlineKey(bookID string) string { return "outline:" + bookID }

// chapterKey is one chapter generation for one book.
func chapterKey(bookID string, ordinal int) string {
	return "chapter:" + bookID + ":" + strconv.Itoa(ordinal)
}
