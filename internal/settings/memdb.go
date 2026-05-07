package settings

import (
	"context"
	"sync"
)

type MemBackend struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func NewMemBackend() *MemBackend {
	return &MemBackend{m: map[string][]byte{}}
}

func (b *MemBackend) Get(_ context.Context, key string) ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	v, ok := b.m[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nil
}

func (b *MemBackend) Set(_ context.Context, key string, value []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	b.m[key] = cp
	return nil
}

func (b *MemBackend) Delete(_ context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.m[key]; !ok {
		return ErrNotFound
	}
	delete(b.m, key)
	return nil
}
