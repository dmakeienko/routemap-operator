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

package store

import (
	"sync"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	routemapsv1alpha1 "github.com/dmakeienko/routemap-operator/api/v1alpha1"
	"github.com/dmakeienko/routemap-operator/internal/inventory"
)

func key(ns, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: ns, Name: name}
}

func TestStoreSetAndGet(t *testing.T) {
	s := New()

	k := key("default", "rm1")
	view := RoutemapView{
		Spec: routemapsv1alpha1.RoutemapSpec{DisplayName: "Test"},
		Endpoints: []inventory.Endpoint{
			{Host: "a.example.com", Health: inventory.HealthStateUnknown},
		},
	}

	s.Set(k, view)
	got, ok := s.Get(k)
	if !ok {
		t.Fatal("expected key to be present")
	}
	if len(got.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(got.Endpoints))
	}
	if got.Endpoints[0].Host != "a.example.com" {
		t.Errorf("Host = %q", got.Endpoints[0].Host)
	}
}

func TestStoreGetMissing(t *testing.T) {
	s := New()
	_, ok := s.Get(key("ns", "missing"))
	if ok {
		t.Error("expected false for missing key")
	}
}

func TestStoreDelete(t *testing.T) {
	s := New()
	k := key("ns", "rm")
	s.Set(k, RoutemapView{})
	s.Delete(k)
	_, ok := s.Get(k)
	if ok {
		t.Error("expected key to be absent after Delete")
	}
}

func TestStoreKeys(t *testing.T) {
	s := New()
	s.Set(key("a", "1"), RoutemapView{})
	s.Set(key("b", "2"), RoutemapView{})
	keys := s.Keys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}

func TestStoreUpdateEndpointHealth(t *testing.T) {
	s := New()
	k := key("default", "rm")
	srcRef := key("prod", "ing1")
	s.Set(k, RoutemapView{
		Endpoints: []inventory.Endpoint{
			{SourceRef: srcRef, Health: inventory.HealthStateUnknown},
		},
	})

	s.UpdateEndpointHealth(k, srcRef, inventory.HealthStateHealthy)

	got, _ := s.Get(k)
	if got.Endpoints[0].Health != inventory.HealthStateHealthy {
		t.Errorf("Health = %q, want Healthy", got.Endpoints[0].Health)
	}
}

func TestStoreUpdateEndpointHealthMissingKey(t *testing.T) {
	s := New()
	// Should not panic.
	s.UpdateEndpointHealth(key("ns", "absent"), key("ns", "src"), inventory.HealthStateHealthy)
}

func TestStoreConcurrentAccess(t *testing.T) {
	s := New()
	k := key("ns", "rm")
	s.Set(k, RoutemapView{Endpoints: []inventory.Endpoint{{Host: "x.com"}}})

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.Get(k)
		}()
		go func() {
			defer wg.Done()
			s.Set(k, RoutemapView{Endpoints: []inventory.Endpoint{{Host: "y.com"}}})
		}()
	}
	wg.Wait()
}
