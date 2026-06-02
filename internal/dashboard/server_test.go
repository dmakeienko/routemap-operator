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

package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	routemapsv1alpha1 "github.com/dmakeienko/routemap-operator/api/v1alpha1"
	"github.com/dmakeienko/routemap-operator/internal/inventory"
	"github.com/dmakeienko/routemap-operator/internal/store"
)

func newTestServer(s *store.Store) *Server {
	return &Server{BindAddr: ":0", Store: s}
}

func populateStore(s *store.Store) {
	key := types.NamespacedName{Namespace: "prod", Name: "my-rm"}
	s.Set(key, store.RoutemapView{
		Spec: routemapsv1alpha1.RoutemapSpec{DisplayName: "My App"},
		Endpoints: []inventory.Endpoint{
			{
				SourceKind: "Ingress",
				SourceRef:  types.NamespacedName{Namespace: "prod", Name: "ing1"},
				Host:       "app.example.com",
				Path:       "/",
				Backend:    "app-svc:80",
				Health:     inventory.HealthStateHealthy,
			},
		},
	})
}

func TestRouteHandler_Dashboard(t *testing.T) {
	s := store.New()
	populateStore(s)
	srv := newTestServer(s)

	req := httptest.NewRequest(http.MethodGet, "/prod/my-rm", nil)
	w := httptest.NewRecorder()
	srv.routeHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "My App") {
		t.Error("dashboard body missing display name")
	}
	if !strings.Contains(body, "app.example.com") {
		t.Error("dashboard body missing host")
	}
}

func TestRouteHandler_APIEndpoints(t *testing.T) {
	s := store.New()
	populateStore(s)
	srv := newTestServer(s)

	req := httptest.NewRequest(http.MethodGet, "/prod/my-rm/api/endpoints", nil)
	w := httptest.NewRecorder()
	srv.routeHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var list []endpointJSON
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 endpoint, got %d", len(list))
	}
	if list[0].Host != "app.example.com" {
		t.Errorf("Host = %q", list[0].Host)
	}
	if list[0].Health != inventory.HealthStateHealthy {
		t.Errorf("Health = %q", list[0].Health)
	}
}

func TestRouteHandler_NotFound_UnknownKey(t *testing.T) {
	s := store.New()
	srv := newTestServer(s)

	req := httptest.NewRequest(http.MethodGet, "/ns/absent", nil)
	w := httptest.NewRecorder()
	srv.routeHandler(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestRouteHandler_NotFound_ShortPath(t *testing.T) {
	s := store.New()
	srv := newTestServer(s)

	for _, path := range []string{"/", "/onlyone"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.routeHandler(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("path %q: status = %d, want 404", path, w.Code)
		}
	}
}

func TestHealthzEndpoint(t *testing.T) {
	s := store.New()
	srv := newTestServer(s)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", srv.routeHandler)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("healthz status = %d", w.Code)
	}
}

func TestToJSON(t *testing.T) {
	ep := inventory.Endpoint{
		SourceKind: "Ingress",
		SourceRef:  types.NamespacedName{Namespace: "ns", Name: "ing"},
		Host:       "x.com",
		Path:       "/api",
		PathType:   "Prefix",
		Backend:    "svc:8080",
		TLS:        true,
		HealthPath: "/_health",
		Health:     inventory.HealthStateUnhealthy,
	}
	j := toJSON(ep)
	if j.Host != "x.com" || j.TLS != true || j.Health != inventory.HealthStateUnhealthy {
		t.Errorf("toJSON mismatch: %+v", j)
	}
	if j.SourceRef.Namespace != "ns" || j.SourceRef.Name != "ing" {
		t.Errorf("SourceRef mismatch: %+v", j.SourceRef)
	}
}

func TestDashboardDefaultTitle(t *testing.T) {
	s := store.New()
	key := types.NamespacedName{Namespace: "ns", Name: "myrm"}
	s.Set(key, store.RoutemapView{
		Spec:      routemapsv1alpha1.RoutemapSpec{}, // no DisplayName
		Endpoints: nil,
	})
	srv := newTestServer(s)

	req := httptest.NewRequest(http.MethodGet, "/ns/myrm", nil)
	w := httptest.NewRecorder()
	srv.routeHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "myrm") {
		t.Error("dashboard should fall back to CR name as title")
	}
}
