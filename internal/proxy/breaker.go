package proxy

import (
	"sync"
	"sync/atomic"
	"time"
)

type CircuitBreaker struct {
	state       int32 // 0=closed, 1=open, 2=half-open
	failures    int32
	mu          sync.Mutex
	lastFailure time.Time
	threshold   int
	timeout     time.Duration
	name        string
}

func NewCircuitBreaker(name string, threshold int, timeout time.Duration) *CircuitBreaker {
	if threshold == 0 {
		threshold = 5
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &CircuitBreaker{
		threshold: threshold,
		timeout:   timeout,
		name:      name,
	}
}

func (cb *CircuitBreaker) Allow() bool {
	state := atomic.LoadInt32(&cb.state)
	switch state {
	case 0: // CLOSED
		return true
	case 1: // OPEN
		cb.mu.Lock()
		defer cb.mu.Unlock()
		if time.Since(cb.lastFailure) >= cb.timeout {
			// Transition to HALF-OPEN
			atomic.StoreInt32(&cb.state, 2)
			return true
		}
		return false
	case 2: // HALF-OPEN
		return true // Allow one probe through (see explanation on split-brain)
	}
	return false
}

func (cb *CircuitBreaker) RecordSuccess() {
	state := atomic.LoadInt32(&cb.state)
	if state == 2 || state == 0 {
		atomic.StoreInt32(&cb.failures, 0)
		atomic.StoreInt32(&cb.state, 0) // Reset to CLOSED
	}
}

func (cb *CircuitBreaker) RecordFailure() {
	fails := atomic.AddInt32(&cb.failures, 1)
	if fails >= int32(cb.threshold) {
		cb.mu.Lock()
		defer cb.mu.Unlock()
		// Only update lastFailure if we are actually transitioning to OPEN
		// or if we want to reset the timeout window on subsequent failures
		cb.lastFailure = time.Now()
		atomic.StoreInt32(&cb.state, 1) // OPEN
	}
}

func (cb *CircuitBreaker) State() string {
	state := atomic.LoadInt32(&cb.state)
	switch state {
	case 0:
		return "closed"
	case 1:
		return "open"
	case 2:
		return "half-open"
	default:
		return "unknown"
	}
}

func (cb *CircuitBreaker) Failures() int {
	return int(atomic.LoadInt32(&cb.failures))
}

func (cb *CircuitBreaker) Threshold() int {
	return cb.threshold
}

func (cb *CircuitBreaker) Name() string {
	return cb.name
}
