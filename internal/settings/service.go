package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultCacheTTL = 5 * time.Minute
)

const (
	SourceDB    = "db"
	SourceEnv   = "env"
	SourceUnset = "unset"
)

const (
	maskDisplay     = "•••"
	unsetDisplay    = "(unset)"
	previewMaxChars = 80
)

var (
	ErrNotFound      = errors.New("settings: not found")
	ErrNotRegistered = errors.New("settings: key not registered")
	ErrUnset         = errors.New("settings: value unset")
)

type Definition struct {
	Key         string
	EnvVar      string
	Secret      bool
	Description string
}

type Preview struct {
	Key         string
	Source      string
	Set         bool
	Secret      bool
	Description string
	Display     string
}

type Backend interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
}

type AuditLogger interface {
	LogSettingChange(ctx context.Context, adminUserID *uuid.UUID, key, preview string) error
}

type cacheEntry struct {
	value    string
	storedAt time.Time
}

type Service struct {
	cipher   *Cipher
	backend  Backend
	auditor  AuditLogger
	registry map[string]Definition

	mu       sync.RWMutex
	cache    map[string]cacheEntry
	cacheTTL time.Duration

	now func() time.Time
	env func(string) string
}

type Option func(*Service)

func WithRegistry(defs []Definition) Option {
	return func(s *Service) {
		s.registry = make(map[string]Definition, len(defs))
		for _, d := range defs {
			s.registry[d.Key] = d
		}
	}
}

func WithCacheTTL(d time.Duration) Option {
	return func(s *Service) { s.cacheTTL = d }
}

func WithEnv(fn func(string) string) Option { return func(s *Service) { s.env = fn } }

func WithClock(fn func() time.Time) Option { return func(s *Service) { s.now = fn } }

func New(c *Cipher, backend Backend, auditor AuditLogger, opts ...Option) *Service {
	s := &Service{
		cipher:   c,
		backend:  backend,
		auditor:  auditor,
		registry: defaultRegistryMap(),
		cache:    map[string]cacheEntry{},
		cacheTTL: DefaultCacheTTL,
		now:      time.Now,
		env:      os.Getenv,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) Get(ctx context.Context, key string) (string, error) {
	def, ok := s.definition(key)
	if !ok {
		return "", ErrNotRegistered
	}

	s.mu.RLock()
	e, hit := s.cache[key]
	s.mu.RUnlock()
	if hit && s.now().Sub(e.storedAt) < s.cacheTTL {
		return e.value, nil
	}

	v, _, err := s.load(ctx, def)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.cache[key] = cacheEntry{value: v, storedAt: s.now()}
	s.mu.Unlock()
	return v, nil
}

func (s *Service) Set(ctx context.Context, adminUserID *uuid.UUID, key, value string) error {
	def, ok := s.definition(key)
	if !ok {
		return ErrNotRegistered
	}
	blob, err := s.cipher.Encrypt([]byte(value))
	if err != nil {
		return err
	}
	if err := s.backend.Set(ctx, key, blob); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.cache, key)
	s.mu.Unlock()

	if s.auditor != nil {
		preview := previewDisplay(def, value, true)
		if err := s.auditor.LogSettingChange(ctx, adminUserID, key, preview); err != nil {
			return fmt.Errorf("settings: audit: %w", err)
		}
	}
	return nil
}

func (s *Service) Preview(ctx context.Context, key string) (Preview, error) {
	def, ok := s.definition(key)
	if !ok {
		return Preview{}, ErrNotRegistered
	}
	return s.previewFor(ctx, def), nil
}

func (s *Service) GetAll(ctx context.Context) []Preview {
	s.mu.RLock()
	defs := make([]Definition, 0, len(s.registry))
	for _, d := range s.registry {
		defs = append(defs, d)
	}
	s.mu.RUnlock()
	out := make([]Preview, 0, len(defs))
	for _, d := range defs {
		out = append(out, s.previewFor(ctx, d))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// InvalidateCache clears the cache, e.g. on rotation events. Test helper too.
func (s *Service) InvalidateCache() {
	s.mu.Lock()
	s.cache = map[string]cacheEntry{}
	s.mu.Unlock()
}

func (s *Service) definition(key string) (Definition, bool) {
	s.mu.RLock()
	d, ok := s.registry[key]
	s.mu.RUnlock()
	return d, ok
}

func (s *Service) load(ctx context.Context, def Definition) (value, source string, err error) {
	blob, err := s.backend.Get(ctx, def.Key)
	if err == nil {
		plain, derr := s.cipher.Decrypt(blob)
		if derr != nil {
			return "", "", derr
		}
		return string(plain), SourceDB, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", "", err
	}
	if def.EnvVar != "" {
		if v := s.env(def.EnvVar); v != "" {
			return v, SourceEnv, nil
		}
	}
	return "", SourceUnset, ErrUnset
}

func (s *Service) previewFor(ctx context.Context, def Definition) Preview {
	p := Preview{Key: def.Key, Secret: def.Secret, Description: def.Description}
	v, src, err := s.load(ctx, def)
	if err == nil {
		p.Set = true
		p.Source = src
		p.Display = previewDisplay(def, v, true)
	} else {
		p.Set = false
		p.Source = SourceUnset
		p.Display = unsetDisplay
	}
	return p
}

func previewDisplay(def Definition, value string, set bool) string {
	if !set || value == "" {
		return unsetDisplay
	}
	if def.Secret {
		return maskDisplay
	}
	if len(value) > previewMaxChars {
		return value[:previewMaxChars-3] + "..."
	}
	return value
}
