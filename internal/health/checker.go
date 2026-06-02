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

// Package health provides the asynchronous endpoint health-check runnable.
package health

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	routemapsv1alpha1 "github.com/dmakeienko/routemap-operator/api/v1alpha1"
	"github.com/dmakeienko/routemap-operator/internal/inventory"
	"github.com/dmakeienko/routemap-operator/internal/store"
)

var (
	probeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "routemap_endpoint_health_probes_total",
		Help: "Total number of health probes performed.",
	}, []string{"routemap", "host", "result"})
)

const defaultInterval = 30 * time.Second
const defaultTimeout = 5 * time.Second
const defaultExpectedStatus = 200

// Checker is a controller-runtime Runnable that periodically probes endpoints
// with a HealthPath annotation and writes results back to the Store.
// It should run only on the leader (NeedLeaderElection returns true).
type Checker struct {
	Store      *store.Store
	HTTPClient *http.Client // injectable for tests

	mu      sync.Mutex
	cancels map[types.NamespacedName]context.CancelFunc
}

// NewChecker returns an initialised Checker.
func NewChecker(s *store.Store) *Checker {
	return &Checker{
		Store:      s,
		HTTPClient: &http.Client{Timeout: defaultTimeout},
		cancels:    make(map[types.NamespacedName]context.CancelFunc),
	}
}

// NeedLeaderElection returns true so only the leader performs probes.
func (c *Checker) NeedLeaderElection() bool { return true }

// Start implements manager.Runnable. It watches the Store for Routemaps with
// health-check enabled and launches per-Routemap probe loops.
func (c *Checker) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("health-checker")
	log.Info("Health checker started")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.stopAll()
			return nil
		case <-ticker.C:
			c.sync(ctx)
		}
	}
}

// sync reconciles running probe loops against the current Store state.
func (c *Checker) sync(ctx context.Context) {
	keys := c.Store.Keys()
	active := make(map[types.NamespacedName]bool, len(keys))
	for _, k := range keys {
		view, ok := c.Store.Get(k)
		if !ok {
			continue
		}
		active[k] = true
		if view.Spec.HealthCheck == nil || !view.Spec.HealthCheck.Enabled {
			c.stopLoop(k)
			continue
		}
		c.ensureLoop(ctx, k, view.Spec.HealthCheck)
	}
	// Stop loops for deleted Routemaps.
	c.mu.Lock()
	for k := range c.cancels {
		if !active[k] {
			c.cancels[k]()
			delete(c.cancels, k)
		}
	}
	c.mu.Unlock()
}

func (c *Checker) ensureLoop(ctx context.Context, key types.NamespacedName, spec *routemapsv1alpha1.HealthCheckSpec) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, running := c.cancels[key]; running {
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	c.cancels[key] = cancel
	go c.probeLoop(loopCtx, key, spec)
}

func (c *Checker) stopLoop(key types.NamespacedName) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cancel, ok := c.cancels[key]; ok {
		cancel()
		delete(c.cancels, key)
	}
}

func (c *Checker) stopAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cancel := range c.cancels {
		cancel()
	}
	c.cancels = make(map[types.NamespacedName]context.CancelFunc)
}

func (c *Checker) probeLoop(ctx context.Context, key types.NamespacedName, spec *routemapsv1alpha1.HealthCheckSpec) {
	log := logf.Log.WithName("health-checker").WithValues("routemap", key)

	interval := effectiveInterval(spec)
	timeout := effectiveTimeout(spec)
	expected := effectiveExpectedStatus(spec)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			view, ok := c.Store.Get(key)
			if !ok {
				return
			}
			for _, ep := range view.Endpoints {
				if ep.HealthPath == "" {
					continue
				}
				state := c.probe(ctx, ep, timeout, expected)
				c.Store.UpdateEndpointHealth(key, ep.SourceRef, state)
				result := string(state)
				probeTotal.WithLabelValues(key.String(), ep.Host, result).Inc()
				log.V(1).Info("Probed endpoint", "host", ep.Host, "path", ep.HealthPath, "result", result)
			}
		}
	}
}

// probe performs a single HTTP GET and returns the health state.
func (c *Checker) probe(ctx context.Context, ep inventory.Endpoint, timeout time.Duration, expectedStatus int) inventory.HealthState {
	scheme := "http"
	if ep.TLS {
		scheme = "https"
	}
	url := fmt.Sprintf("%s://%s%s", scheme, ep.Host, ep.HealthPath)

	client := c.HTTPClient
	if client.Timeout != timeout {
		client = &http.Client{Timeout: timeout}
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return inventory.HealthStateUnhealthy
	}

	resp, err := client.Do(req)
	if err != nil {
		return inventory.HealthStateUnhealthy
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == expectedStatus {
		return inventory.HealthStateHealthy
	}
	return inventory.HealthStateUnhealthy
}

func effectiveInterval(spec *routemapsv1alpha1.HealthCheckSpec) time.Duration {
	if spec != nil && spec.Interval.Duration > 0 {
		return spec.Interval.Duration
	}
	return defaultInterval
}

func effectiveTimeout(spec *routemapsv1alpha1.HealthCheckSpec) time.Duration {
	if spec != nil && spec.Timeout.Duration > 0 {
		return spec.Timeout.Duration
	}
	return defaultTimeout
}

func effectiveExpectedStatus(spec *routemapsv1alpha1.HealthCheckSpec) int {
	if spec != nil && spec.ExpectedStatus > 0 {
		return int(spec.ExpectedStatus)
	}
	return defaultExpectedStatus
}
