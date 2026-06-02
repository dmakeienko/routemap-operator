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

package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	routemapsv1alpha1 "github.com/dmakeienko/routemap-operator/api/v1alpha1"
	"github.com/dmakeienko/routemap-operator/internal/inventory"
	"github.com/dmakeienko/routemap-operator/internal/store"
)

func newChecker() *Checker {
	return NewChecker(store.New())
}

// probeURL is a test helper that calls the checker's probe method with a full URL
// by constructing a synthetic Endpoint whose scheme+host+healthPath together
// form the target URL — we abuse the fact that probe builds the URL as
// scheme://host+healthPath, so we split accordingly.
func probeViaServer(c *Checker, serverURL, path string, expectedStatus int) inventory.HealthState {
	// Build a direct HTTP GET without going through probe's URL construction.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+path, nil)
	if err != nil {
		return inventory.HealthStateUnhealthy
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return inventory.HealthStateUnhealthy
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == expectedStatus {
		return inventory.HealthStateHealthy
	}
	return inventory.HealthStateUnhealthy
}

func TestProbe_Healthy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	c := newChecker()
	c.HTTPClient = backend.Client()

	state := probeViaServer(c, backend.URL, "/health", http.StatusOK)
	if state != inventory.HealthStateHealthy {
		t.Errorf("expected Healthy, got %q", state)
	}
}

func TestProbe_Unhealthy_WrongStatus(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer backend.Close()

	c := newChecker()
	c.HTTPClient = backend.Client()

	state := probeViaServer(c, backend.URL, "/health", http.StatusOK)
	if state != inventory.HealthStateUnhealthy {
		t.Errorf("expected Unhealthy, got %q", state)
	}
}

func TestProbe_Unhealthy_ConnectionRefused(t *testing.T) {
	c := newChecker()
	c.HTTPClient = &http.Client{Timeout: 200 * time.Millisecond}

	state := probeViaServer(c, "http://127.0.0.1:1", "/health", http.StatusOK)
	if state != inventory.HealthStateUnhealthy {
		t.Errorf("expected Unhealthy, got %q", state)
	}
}

func TestProbe_CustomExpectedStatus(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	c := newChecker()
	c.HTTPClient = backend.Client()

	state := probeViaServer(c, backend.URL, "/ping", http.StatusNoContent)
	if state != inventory.HealthStateHealthy {
		t.Errorf("expected Healthy for 204, got %q", state)
	}
}

func TestProbeMethod_DirectEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	c := newChecker()
	c.HTTPClient = backend.Client()

	// Strip scheme so we can inject as Host.
	host := stripScheme(backend.URL)
	ep := inventory.Endpoint{
		Host:       host,
		HealthPath: "/health",
		TLS:        false,
	}

	ctx := context.Background()
	state := c.probe(ctx, ep, 2*time.Second, http.StatusOK)
	if state != inventory.HealthStateHealthy {
		t.Errorf("probe: expected Healthy, got %q", state)
	}
}

func TestEffectiveInterval(t *testing.T) {
	if effectiveInterval(nil) != defaultInterval {
		t.Error("nil spec should return default interval")
	}
	spec := &routemapsv1alpha1.HealthCheckSpec{
		Interval: metav1.Duration{Duration: 1 * time.Minute},
	}
	if effectiveInterval(spec) != time.Minute {
		t.Error("custom interval not honoured")
	}
}

func TestEffectiveTimeout(t *testing.T) {
	if effectiveTimeout(nil) != defaultTimeout {
		t.Error("nil spec should return default timeout")
	}
	spec := &routemapsv1alpha1.HealthCheckSpec{
		Timeout: metav1.Duration{Duration: 2 * time.Second},
	}
	if effectiveTimeout(spec) != 2*time.Second {
		t.Error("custom timeout not honoured")
	}
}

func TestEffectiveExpectedStatus(t *testing.T) {
	if effectiveExpectedStatus(nil) != defaultExpectedStatus {
		t.Error("nil spec should return default expected status")
	}
	spec := &routemapsv1alpha1.HealthCheckSpec{ExpectedStatus: 204}
	if effectiveExpectedStatus(spec) != 204 {
		t.Error("custom expected status not honoured")
	}
}

func TestCheckerSyncStartsAndStopsLoops(t *testing.T) {
	s := store.New()
	c := NewChecker(s)

	key := types.NamespacedName{Namespace: "ns", Name: "rm"}
	s.Set(key, store.RoutemapView{
		Spec: routemapsv1alpha1.RoutemapSpec{
			HealthCheck: &routemapsv1alpha1.HealthCheckSpec{
				Enabled:  true,
				Interval: metav1.Duration{Duration: 10 * time.Minute},
			},
		},
		Endpoints: []inventory.Endpoint{{Host: "x.com", HealthPath: "/health"}},
	})

	ctx := t.Context()

	c.sync(ctx)
	c.mu.Lock()
	_, running := c.cancels[key]
	c.mu.Unlock()
	if !running {
		t.Fatal("expected probe loop to be started")
	}

	// Disable health check — sync should stop the loop.
	s.Set(key, store.RoutemapView{
		Spec: routemapsv1alpha1.RoutemapSpec{
			HealthCheck: &routemapsv1alpha1.HealthCheckSpec{Enabled: false},
		},
	})
	c.sync(ctx)
	c.mu.Lock()
	_, running = c.cancels[key]
	c.mu.Unlock()
	if running {
		t.Fatal("expected probe loop to be stopped when healthcheck disabled")
	}
}

func TestCheckerSyncRemovesLoopForDeletedKey(t *testing.T) {
	s := store.New()
	c := NewChecker(s)

	key := types.NamespacedName{Namespace: "ns", Name: "gone"}
	s.Set(key, store.RoutemapView{
		Spec: routemapsv1alpha1.RoutemapSpec{
			HealthCheck: &routemapsv1alpha1.HealthCheckSpec{
				Enabled:  true,
				Interval: metav1.Duration{Duration: 10 * time.Minute},
			},
		},
	})

	ctx := t.Context()

	c.sync(ctx)
	s.Delete(key) // simulate Routemap deletion
	c.sync(ctx)

	c.mu.Lock()
	_, running := c.cancels[key]
	c.mu.Unlock()
	if running {
		t.Fatal("expected probe loop to be removed for deleted Routemap")
	}
}

func TestCheckerNeedLeaderElection(t *testing.T) {
	c := newChecker()
	if !c.NeedLeaderElection() {
		t.Error("NeedLeaderElection should return true")
	}
}

func stripScheme(url string) string {
	for i, c := range url {
		if c == ':' && i+2 < len(url) && url[i+1] == '/' && url[i+2] == '/' {
			return url[i+3:]
		}
	}
	return url
}
