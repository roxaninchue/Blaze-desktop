package screen

import (
	"sync"
	"time"
)

// ThumbMaxAge is how long a snapshot stays valid while the window's picture hash
// hasn't changed (where a cheap thumbnail exists, Windows today).
const ThumbMaxAge = 30 * time.Second

// Fresh says whether a cached snapshot can answer a read of window w. The cache is
// keyed by the window (id, pid and title) plus, where a thumbnail is cheap, a
// perceptual hash of its picture: same window and same picture = same text.
// ttl <= 0 always reads again.
func Fresh(last *Snapshot, w Win, th uint64, hasThumb bool, ttl time.Duration, now time.Time) bool {
	if last == nil || last.Err != "" || ttl <= 0 || last.key != w.Key() {
		return false
	}
	age := now.Sub(last.At)
	if age < 0 {
		return false
	}
	if hasThumb {
		limit := ttl
		if limit < ThumbMaxAge {
			limit = ThumbMaxAge
		}
		return Near(th, last.Thumb) && age < limit
	}
	return age < ttl
}

// Answers caches what a vision model said about a picture: same key (window,
// region, question) and a near-identical picture hash within MaxAge reuse the
// answer instead of paying for another vision call.
type Answers struct {
	mu sync.Mutex
	m  map[string]answer
}

type answer struct {
	hash uint64
	text string
	at   time.Time
}

const (
	answersMax    = 24
	AnswersMaxAge = 60 * time.Second
)

// Get returns the cached answer and its age.
func (c *Answers) Get(key string, hash uint64, now time.Time) (string, time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.m[key]
	if !ok || hash == 0 || !Near(a.hash, hash) {
		return "", 0, false
	}
	age := now.Sub(a.at)
	if age < 0 || age >= AnswersMaxAge {
		return "", 0, false
	}
	return a.text, age, true
}

// Put stores an answer; expired entries go first, then the oldest when full.
func (c *Answers) Put(key string, hash uint64, text string, now time.Time) {
	if hash == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]answer{}
	}
	for k, a := range c.m {
		if now.Sub(a.at) >= AnswersMaxAge {
			delete(c.m, k)
		}
	}
	for len(c.m) >= answersMax {
		var ok string
		var t time.Time
		for k, a := range c.m {
			if ok == "" || a.at.Before(t) {
				ok, t = k, a.at
			}
		}
		delete(c.m, ok)
	}
	c.m[key] = answer{hash: hash, text: text, at: now}
}

// Len is the number of cached answers.
func (c *Answers) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}
