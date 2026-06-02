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

// Package dashboard provides the HTTP server that serves per-Routemap dashboards.
package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/dmakeienko/routemap-operator/internal/inventory"
	"github.com/dmakeienko/routemap-operator/internal/store"
)

// Server is a controller-runtime Runnable that serves Routemap dashboards.
type Server struct {
	BindAddr string
	Store    *store.Store
	OIDC     *OIDCConfig // optional; nil means OIDC is not configured globally
}

// NeedLeaderElection returns false — the dashboard is read-only and safe on all replicas.
func (s *Server) NeedLeaderElection() bool { return false }

// Start implements manager.Runnable.
func (s *Server) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("dashboard")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", s.routeHandler)

	srv := &http.Server{
		Addr:              s.BindAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := net.Listen("tcp", s.BindAddr)
	if err != nil {
		return err
	}
	log.Info("Dashboard server listening", "addr", ln.Addr().String())

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// routeHandler dispatches based on URL structure:
//
//	GET /{namespace}/{name}               — dashboard HTML
//	GET /{namespace}/{name}/api/endpoints — JSON endpoint list
//	GET /{namespace}/{name}/callback      — OIDC callback
func (s *Server) routeHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		http.NotFound(w, r)
		return
	}
	ns, name := parts[0], parts[1]
	tail := ""
	if len(parts) == 3 {
		tail = parts[2]
	}

	key := types.NamespacedName{Namespace: ns, Name: name}
	view, ok := s.Store.Get(key)
	if !ok {
		http.NotFound(w, r)
		return
	}

	// OIDC callback handled before auth check.
	if tail == "callback" && s.OIDC != nil {
		s.handleOIDCCallback(w, r, view)
		return
	}

	// Apply OIDC middleware when this Routemap requires auth.
	if view.Spec.Auth != nil && s.OIDC != nil {
		if !s.OIDC.Validate(w, r, ns, name, view) {
			return
		}
	}

	switch tail {
	case "api/endpoints":
		s.handleAPIEndpoints(w, view.Endpoints)
	default:
		s.handleDashboard(w, ns, name, view)
	}
}

// endpointJSON is the wire format for /api/endpoints.
type endpointJSON struct {
	SourceKind string `json:"SourceKind"`
	SourceRef  struct {
		Namespace string `json:"Namespace"`
		Name      string `json:"Name"`
	} `json:"SourceRef"`
	Host       string                `json:"Host"`
	Path       string                `json:"Path"`
	PathType   string                `json:"PathType"`
	Backend    string                `json:"Backend"`
	TLS        bool                  `json:"TLS"`
	HealthPath string                `json:"HealthPath"`
	Health     inventory.HealthState `json:"Health"`
	LastProbe  time.Time             `json:"LastProbe"`
}

func toJSON(ep inventory.Endpoint) endpointJSON {
	j := endpointJSON{
		SourceKind: ep.SourceKind,
		Host:       ep.Host,
		Path:       ep.Path,
		PathType:   ep.PathType,
		Backend:    ep.Backend,
		TLS:        ep.TLS,
		HealthPath: ep.HealthPath,
		Health:     ep.Health,
		LastProbe:  ep.LastProbe,
	}
	j.SourceRef.Namespace = ep.SourceRef.Namespace
	j.SourceRef.Name = ep.SourceRef.Name
	return j
}

func (s *Server) handleAPIEndpoints(w http.ResponseWriter, endpoints []inventory.Endpoint) {
	list := make([]endpointJSON, len(endpoints))
	for i, ep := range endpoints {
		list[i] = toJSON(ep)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// dashboardData is passed to the HTML template.
type dashboardData struct {
	Title          string
	Namespace      string
	Name           string
	TotalEndpoints int
	EndpointsJSON  template.JS
}

func (s *Server) handleDashboard(w http.ResponseWriter, ns, name string, view store.RoutemapView) {
	title := view.Spec.DisplayName
	if title == "" {
		title = name
	}

	list := make([]endpointJSON, len(view.Endpoints))
	for i, ep := range view.Endpoints {
		list[i] = toJSON(ep)
	}
	jsonBytes, _ := json.Marshal(list)

	data := dashboardData{
		Title:          title,
		Namespace:      ns,
		Name:           name,
		TotalEndpoints: len(view.Endpoints),
		EndpointsJSON:  template.JS(jsonBytes), //nolint:gosec // safe: internal store data serialised as JSON
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		logf.Log.WithName("dashboard").Error(err, "Failed to render dashboard template")
	}
}
