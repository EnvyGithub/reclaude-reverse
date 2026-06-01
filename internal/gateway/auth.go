package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// SKKey represents a session key for a user.
type SKKey struct {
	SK         string    `json:"sk"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	Enabled    bool      `json:"enabled"`
	DailyLimit int       `json:"daily_limit"` // 0 = unlimited
	UsedToday  int       `json:"used_today"`
}

// SKStore manages session keys.
type SKStore struct {
	path string
	keys map[string]*SKKey // sk -> key
	mu   sync.RWMutex
}

// skFile is the JSON file format.
type skFile struct {
	Keys []SKKey `json:"keys"`
}

// NewSKStore loads or creates an SK store from a JSON file.
func NewSKStore(path string) (*SKStore, error) {
	store := &SKStore{
		path: path,
		keys: make(map[string]*SKKey),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[AUTH] No SK file at %s, starting empty", path)
			return store, nil
		}
		return nil, fmt.Errorf("read SK file: %w", err)
	}

	var f skFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse SK file: %w", err)
	}

	for i := range f.Keys {
		k := f.Keys[i]
		store.keys[k.SK] = &k
	}

	log.Printf("[AUTH] Loaded %d SK keys from %s", len(store.keys), path)
	return store, nil
}

// Verify checks if an SK is valid and within limits.
func (s *SKStore) Verify(sk string) (*SKKey, error) {
	s.mu.RLock()
	key, ok := s.keys[sk]
	s.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("invalid SK")
	}
	if !key.Enabled {
		return nil, fmt.Errorf("SK disabled")
	}
	if key.DailyLimit > 0 && key.UsedToday >= key.DailyLimit {
		return nil, fmt.Errorf("SK daily limit reached (%d/%d)", key.UsedToday, key.DailyLimit)
	}

	return key, nil
}

// RecordUsage increments the usage counter for an SK.
func (s *SKStore) RecordUsage(sk string) {
	s.mu.RLock()
	key, ok := s.keys[sk]
	s.mu.RUnlock()
	if ok {
		key.UsedToday++
	}
}

// GenerateSK creates a new SK key and saves it.
func (s *SKStore) GenerateSK(name string, dailyLimit int) (string, error) {
	sk := generateSKToken()

	key := &SKKey{
		SK:         sk,
		Name:       name,
		CreatedAt:  time.Now(),
		Enabled:    true,
		DailyLimit: dailyLimit,
	}

	s.mu.Lock()
	s.keys[sk] = key
	s.mu.Unlock()

	if err := s.save(); err != nil {
		return "", err
	}

	log.Printf("[AUTH] Generated SK for %q: %s", name, sk)
	return sk, nil
}

// List returns all SK keys.
func (s *SKStore) List() []SKKey {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []SKKey
	for _, k := range s.keys {
		result = append(result, *k)
	}
	return result
}

// SetEnabled enables or disables an SK.
func (s *SKStore) SetEnabled(sk string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, ok := s.keys[sk]
	if !ok {
		return fmt.Errorf("SK not found")
	}
	key.Enabled = enabled
	return s.save()
}

func (s *SKStore) save() error {
	var f skFile
	for _, k := range s.keys {
		f.Keys = append(f.Keys, *k)
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

func generateSKToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return "sk-gw-" + hex.EncodeToString(b)
}
