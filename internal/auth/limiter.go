package auth

import (
	"sync"
	"time"
)

// Limiter slows password guessing per client address. The first few failures
// are free, so a typo costs nothing; after that each failure doubles the wait
// before the next attempt, up to maxLockout. It is in memory only: a restart
// forgets it, which costs an attacker a restart they cannot trigger.
type Limiter struct {
	now func() time.Time

	mu      sync.Mutex
	clients map[string]*attempts
}

type attempts struct {
	failures int
	until    time.Time // no attempt is allowed before this
	last     time.Time
}

const (
	freeFailures = 5
	maxLockout   = 15 * time.Minute
	// forgetAfter drops a client that has stopped trying.
	forgetAfter = time.Hour
)

// NewLimiter returns an empty limiter.
func NewLimiter() *Limiter {
	return &Limiter{now: time.Now, clients: map[string]*attempts{}}
}

// Attempt asks to check a password for client. A non-zero result is how long
// it must wait first. Otherwise the attempt is already counted as a failure,
// in the same step, so concurrent guesses cannot all pass before the first
// one is recorded; Succeed takes it back.
func (l *Limiter) Attempt(client string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if a := l.clients[client]; a != nil {
		if d := a.until.Sub(now); d > 0 {
			return d
		}
	}

	for k, a := range l.clients {
		if now.Sub(a.last) > forgetAfter {
			delete(l.clients, k)
		}
	}
	a := l.clients[client]
	if a == nil {
		a = &attempts{}
		l.clients[client] = a
	}
	a.failures++
	a.last = now
	if over := a.failures - freeFailures; over > 0 {
		d := maxLockout
		if over < 20 {
			d = min(time.Second<<over, maxLockout)
		}
		a.until = now.Add(d)
	}
	return 0
}

// Succeed clears client's record.
func (l *Limiter) Succeed(client string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.clients, client)
}
