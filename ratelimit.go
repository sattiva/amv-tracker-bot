package main

import (
	"sync"
	"time"
)

type RateLimiter struct {
	sync.Mutex
	seen   map[string][]time.Time
	limit  int
	window time.Duration
}

// discord would let us do way more than this, 3 is just where people start
// complaining so stay under it
var Limiter = &RateLimiter{seen: make(map[string][]time.Time), limit: 3, window: 4 * time.Second}

func (rl *RateLimiter) Allow(id string) bool {
	if id == "" {
		return true
	}
	now := time.Now()
	cutoff := now.Add(-rl.window)

	rl.Lock()
	defer rl.Unlock()
	var live []time.Time
	for _, t := range rl.seen[id] {
		if t.After(cutoff) {
			live = append(live, t)
		}
	}
	if len(live) >= rl.limit {
		rl.seen[id] = live
		return false
	}
	rl.seen[id] = append(live, now)
	return true
}

func (rl *RateLimiter) Clean() {
	cutoff := time.Now().Add(-5 * time.Minute)
	rl.Lock()
	defer rl.Unlock()
	for id, times := range rl.seen {
		if len(times) == 0 || times[len(times)-1].Before(cutoff) {
			delete(rl.seen, id)
		}
	}
}
