// Package inmem provides a simple in-memory repository implementation.
package inmem

import (
	"context"
	"errors"
	"sync"
)

// IDer is an interface for types that have an ID of type K.
type IDer[K comparable] interface {
	ID() K
}

// Repository is a generic in-memory repository for types that implement the IDer interface.
type Repository[K comparable, V IDer[K]] struct {
	mu   sync.RWMutex
	data map[K]*V
}

// NewRepository creates a new in-memory repository.
func NewRepository[K comparable, V IDer[K]]() *Repository[K, V] {
	return &Repository[K, V]{
		data: make(map[K]*V),
	}
}

// Load retrieves a value by its ID and populates the provided pointer.
func (r *Repository[K, V]) Load(ctx context.Context, val *V) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if val == nil {
		return errors.New("loading: value empty")
	}

	v, exists := r.data[(*val).ID()]
	if !exists || v == nil {
		return errors.New("loading: not found")
	}

	*val = *v

	return nil
}

// Save stores a value in the repository.
func (r *Repository[K, V]) Save(ctx context.Context, val *V) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if val == nil {
		return errors.New("saving: value empty")
	}

	key := (*val).ID()

	if _, exists := r.data[key]; exists {
		return errors.New("saving: already exists")
	}

	r.data[key] = val

	return nil
}
