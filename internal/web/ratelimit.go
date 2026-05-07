package web

import (
	"sync"
	"time"
)

type LoginLimiter struct {
	mu              sync.Mutex
	state           map[string]*loginState
	window          time.Duration
	maxPerWindow    int
	backoffAfter    int
	backoffBaseUnit time.Duration
	now             func() time.Time
}

type loginState struct {
	failures   []time.Time
	lockUntil  time.Time
	streakFail int
}

func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{
		state:           map[string]*loginState{},
		window:          15 * time.Minute,
		maxPerWindow:    5,
		backoffAfter:    3,
		backoffBaseUnit: time.Second,
		now:             time.Now,
	}
}

func (l *LoginLimiter) WithClock(now func() time.Time) *LoginLimiter {
	l.now = now
	return l
}

// Check returns retryAfter > 0 if the IP must wait, plus a bool indicating whether the rate-limit window is exhausted.
func (l *LoginLimiter) Check(ip string) (retryAfter time.Duration, allowed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.state[ip]
	if st == nil {
		return 0, true
	}
	if now.Before(st.lockUntil) {
		return st.lockUntil.Sub(now), false
	}
	cutoff := now.Add(-l.window)
	st.failures = pruneBefore(st.failures, cutoff)
	if len(st.failures) >= l.maxPerWindow {
		oldest := st.failures[0]
		return oldest.Add(l.window).Sub(now), false
	}
	return 0, true
}

func (l *LoginLimiter) RecordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.state[ip]
	if st == nil {
		st = &loginState{}
		l.state[ip] = st
	}
	st.failures = append(st.failures, now)
	st.streakFail++
	if st.streakFail > l.backoffAfter {
		exp := st.streakFail - l.backoffAfter
		if exp > 8 {
			exp = 8
		}
		dur := l.backoffBaseUnit * (1 << exp)
		st.lockUntil = now.Add(dur)
	}
}

func (l *LoginLimiter) RecordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.state, ip)
}

func pruneBefore(t []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for ; i < len(t); i++ {
		if t[i].After(cutoff) {
			break
		}
	}
	return t[i:]
}
