package gateway

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// TokenAccount represents a single Pro account in the pool.
type TokenAccount struct {
	ID         string `json:"id"`
	OAuthToken string `json:"oauth_token"`
	MaxRPM     int    `json:"max_rpm"`     // Max requests per minute (0=unlimited)
	MaxDaily   int    `json:"max_daily"`   // Max requests per day (0=unlimited)
	Enabled    bool   `json:"enabled"`

	// Runtime state (not persisted)
	usedToday    int
	recentCalls  []time.Time // timestamps of recent calls for RPM tracking
	mu           sync.Mutex
}

// TokenPool manages multiple Pro accounts with load balancing.
type TokenPool struct {
	accounts []*TokenAccount
	mu       sync.RWMutex
	resetDay int // day of month when counters were last reset
}

// NewTokenPool creates a pool from config.
func NewTokenPool(accounts []TokenAccount) *TokenPool {
	pool := &TokenPool{
		resetDay: time.Now().Day(),
	}
	for i := range accounts {
		acct := accounts[i]
		pool.accounts = append(pool.accounts, &acct)
	}
	log.Printf("[POOL] Initialized with %d accounts", len(pool.accounts))
	return pool
}

// Pick selects the best available account (least-used strategy).
// Returns the OAuth token to use, or error if none available.
func (p *TokenPool) Pick() (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Reset daily counters if new day
	p.maybeResetDaily()

	now := time.Now()
	var best *TokenAccount
	bestUsage := int(^uint(0) >> 1) // max int

	for _, acct := range p.accounts {
		acct.mu.Lock()

		if !acct.Enabled {
			acct.mu.Unlock()
			continue
		}

		// Check daily limit
		if acct.MaxDaily > 0 && acct.usedToday >= acct.MaxDaily {
			acct.mu.Unlock()
			continue
		}

		// Check RPM limit
		if acct.MaxRPM > 0 {
			// Count calls in last 60 seconds
			cutoff := now.Add(-60 * time.Second)
			recent := 0
			for _, t := range acct.recentCalls {
				if t.After(cutoff) {
					recent++
				}
			}
			if recent >= acct.MaxRPM {
				acct.mu.Unlock()
				continue
			}
		}

		// Pick least used
		if acct.usedToday < bestUsage {
			bestUsage = acct.usedToday
			best = acct
		}
		acct.mu.Unlock()
	}

	if best == nil {
		return "", "", fmt.Errorf("no available accounts (all at capacity)")
	}

	// Record usage
	best.mu.Lock()
	best.usedToday++
	best.recentCalls = append(best.recentCalls, now)
	// Trim old entries (keep last 2 minutes)
	cutoff := now.Add(-2 * time.Minute)
	trimmed := best.recentCalls[:0]
	for _, t := range best.recentCalls {
		if t.After(cutoff) {
			trimmed = append(trimmed, t)
		}
	}
	best.recentCalls = trimmed
	best.mu.Unlock()

	log.Printf("[POOL] Selected account %s (used today: %d)", best.ID, best.usedToday)
	return best.ID, best.OAuthToken, nil
}

// Stats returns usage statistics for all accounts.
func (p *TokenPool) Stats() []AccountStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var stats []AccountStats
	for _, acct := range p.accounts {
		acct.mu.Lock()
		stats = append(stats, AccountStats{
			ID:        acct.ID,
			Enabled:   acct.Enabled,
			UsedToday: acct.usedToday,
			MaxDaily:  acct.MaxDaily,
			MaxRPM:    acct.MaxRPM,
		})
		acct.mu.Unlock()
	}
	return stats
}

// AccountStats is a snapshot of account usage.
type AccountStats struct {
	ID        string `json:"id"`
	Enabled   bool   `json:"enabled"`
	UsedToday int    `json:"used_today"`
	MaxDaily  int    `json:"max_daily"`
	MaxRPM    int    `json:"max_rpm"`
}

func (p *TokenPool) maybeResetDaily() {
	today := time.Now().Day()
	if today != p.resetDay {
		p.resetDay = today
		for _, acct := range p.accounts {
			acct.mu.Lock()
			acct.usedToday = 0
			acct.mu.Unlock()
		}
		log.Printf("[POOL] Daily counters reset")
	}
}
