/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package store provides a thread-safe in-memory store that bridges the
// reconciler (writer) with the dashboard server and health checker (readers).
package store

import (
	"sync"

	"k8s.io/apimachinery/pkg/types"

	routemapsv1alpha1 "github.com/dmakeienko/routemap-operator/api/v1alpha1"
	"github.com/dmakeienko/routemap-operator/internal/inventory"
)

// RoutemapView is the snapshot the dashboard and health checker operate on.
type RoutemapView struct {
	Spec      routemapsv1alpha1.RoutemapSpec
	Endpoints []inventory.Endpoint
}

// Store is a thread-safe in-memory map from Routemap key to its current view.
type Store struct {
	mu   sync.RWMutex
	data map[types.NamespacedName]RoutemapView
}

// New returns an initialised Store.
func New() *Store {
	return &Store{data: make(map[types.NamespacedName]RoutemapView)}
}

// Set atomically replaces the view for key.
func (s *Store) Set(key types.NamespacedName, view RoutemapView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = view
}

// Get returns the view for key and whether it was present.
func (s *Store) Get(key types.NamespacedName) (RoutemapView, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Delete removes the view for key (called when a Routemap is deleted).
func (s *Store) Delete(key types.NamespacedName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
}

// Keys returns a snapshot of all known keys.
func (s *Store) Keys() []types.NamespacedName {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]types.NamespacedName, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

// UpdateEndpointHealth replaces the health state of a single endpoint identified
// by its source ref inside the given Routemap view. The function is a no-op if
// the key or endpoint is not found.
func (s *Store) UpdateEndpointHealth(key types.NamespacedName, sourceRef types.NamespacedName, state inventory.HealthState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	view, ok := s.data[key]
	if !ok {
		return
	}
	for i := range view.Endpoints {
		if view.Endpoints[i].SourceRef == sourceRef {
			view.Endpoints[i].Health = state
		}
	}
	s.data[key] = view
}
