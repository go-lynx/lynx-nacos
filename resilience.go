package nacos

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/go-lynx/lynx-nacos/conf"
	"github.com/go-lynx/lynx/log"
)

// RetryManager provides retry logic with exponential backoff
type RetryManager struct {
	maxRetries    int
	retryInterval time.Duration
	backoffFactor float64
}

// NewRetryManager creates a new retry manager
func NewRetryManager(maxRetries int, retryInterval time.Duration) *RetryManager {
	return &RetryManager{
		maxRetries:    maxRetries,
		retryInterval: retryInterval,
		backoffFactor: 2.0,
	}
}

// DoWithRetry executes operation with retry
func (r *RetryManager) DoWithRetry(operation func() error) error {
	var lastErr error

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if err := operation(); err == nil {
			if attempt > 0 {
				log.Infof("Operation succeeded after %d retries", attempt)
			}
			return nil
		} else {
			lastErr = err
			if attempt < r.maxRetries {
				backoffTime := r.calculateBackoff(attempt)
				log.Warnf("Operation failed (attempt %d/%d): %v, retrying in %v",
					attempt+1, r.maxRetries+1, err, backoffTime)
				time.Sleep(backoffTime)
			}
		}
	}

	return fmt.Errorf("operation failed after %d attempts, last error: %w", r.maxRetries+1, lastErr)
}

// DoWithRetryContext executes operation with retry (supports context)
func (r *RetryManager) DoWithRetryContext(ctx context.Context, operation func() error) error {
	var lastErr error

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("operation cancelled: %w", ctx.Err())
		default:
		}

		if err := operation(); err == nil {
			if attempt > 0 {
				log.Infof("Operation succeeded after %d retries", attempt)
			}
			return nil
		} else {
			lastErr = err
			if attempt < r.maxRetries {
				backoffTime := r.calculateBackoff(attempt)
				log.Warnf("Operation failed (attempt %d/%d): %v, retrying in %v",
					attempt+1, r.maxRetries+1, err, backoffTime)

				select {
				case <-time.After(backoffTime):
				case <-ctx.Done():
					return fmt.Errorf("operation cancelled during retry: %w", ctx.Err())
				}
			}
		}
	}

	return fmt.Errorf("operation failed after %d attempts, last error: %w", r.maxRetries+1, lastErr)
}

func (r *RetryManager) calculateBackoff(attempt int) time.Duration {
	backoffSeconds := float64(r.retryInterval) * math.Pow(r.backoffFactor, float64(attempt))
	maxBackoff := 30 * time.Second
	if time.Duration(backoffSeconds) > maxBackoff {
		return maxBackoff
	}
	return time.Duration(backoffSeconds)
}

// CircuitBreaker implements circuit breaker protection
type CircuitBreaker struct {
	threshold       float64
	halfOpenTimeout time.Duration
	failureCount    int
	successCount    int
	lastFailure     time.Time
	state           CircuitState
	mu              chan struct{}
}

// CircuitState represents circuit breaker state
type CircuitState int

const (
	CircuitStateClosed CircuitState = iota
	CircuitStateOpen
	CircuitStateHalfOpen
)

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(threshold float64, halfOpenTimeout time.Duration) *CircuitBreaker {
	if halfOpenTimeout <= 0 {
		halfOpenTimeout = conf.DefaultCircuitBreakerHalfOpenTimeout
	}
	return &CircuitBreaker{
		threshold:       threshold,
		halfOpenTimeout: halfOpenTimeout,
		state:           CircuitStateClosed,
		mu:              make(chan struct{}, 1),
	}
}

// Do executes operation with circuit breaker protection
func (cb *CircuitBreaker) Do(operation func() error) error {
	cb.mu <- struct{}{}
	defer func() { <-cb.mu }()

	switch cb.state {
	case CircuitStateOpen:
		if time.Since(cb.lastFailure) > cb.halfOpenTimeout {
			cb.state = CircuitStateHalfOpen
			log.Infof("Circuit breaker transitioning to half-open state")
		} else {
			return fmt.Errorf("circuit breaker is open")
		}
	case CircuitStateHalfOpen:
		log.Infof("Circuit breaker in half-open state, allowing one attempt")
	case CircuitStateClosed:
	default:
		return fmt.Errorf("invalid circuit breaker state: %v", cb.state)
	}

	err := operation()
	if err != nil {
		cb.recordFailure()
	} else {
		cb.recordSuccess()
	}

	return err
}

func (cb *CircuitBreaker) recordFailure() {
	cb.failureCount++
	cb.lastFailure = time.Now()

	total := cb.failureCount + cb.successCount
	failureRate := float64(cb.failureCount) / float64(total)

	if cb.state == CircuitStateClosed && failureRate >= cb.threshold {
		cb.state = CircuitStateOpen
		log.Warnf("Circuit breaker opened: failure rate %.2f >= threshold %.2f",
			failureRate, cb.threshold)
	} else if cb.state == CircuitStateHalfOpen {
		cb.state = CircuitStateOpen
		log.Warnf("Circuit breaker reopened after failed attempt")
	}
}

func (cb *CircuitBreaker) recordSuccess() {
	cb.successCount++

	if cb.state == CircuitStateHalfOpen {
		cb.state = CircuitStateClosed
		cb.resetCounters()
		log.Infof("Circuit breaker closed after successful attempt")
	}
}

func (cb *CircuitBreaker) resetCounters() {
	cb.failureCount = 0
	cb.successCount = 0
}
