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

package inventory

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func ptrOf[T any](v T) *T { return &v }

func TestHTTPRouteToEndpoints(t *testing.T) {
	routemapKey := types.NamespacedName{Namespace: "default", Name: "my-routemap"}
	annotationKey := "routemap.github.com/healthcheck"

	tests := []struct {
		name     string
		route    *gatewayv1.HTTPRoute
		wantLen  int
		validate func(t *testing.T, eps []Endpoint)
	}{
		{
			name: "single hostname single match",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "rt1", Namespace: testNS},
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{"app.example.com"},
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{
									Path: &gatewayv1.HTTPPathMatch{
										Type:  ptrOf(gatewayv1.PathMatchPathPrefix),
										Value: ptrOf("/api"),
									},
								},
							},
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "api-svc",
									Port: ptrOf(gatewayv1.PortNumber(8080)),
								}}},
							},
						},
					},
				},
			},
			wantLen: 1,
			validate: func(t *testing.T, eps []Endpoint) {
				ep := eps[0]
				if ep.Host != "app.example.com" {
					t.Errorf("Host = %q", ep.Host)
				}
				if ep.Path != "/api" {
					t.Errorf("Path = %q", ep.Path)
				}
				if ep.PathType != "PathPrefix" {
					t.Errorf("PathType = %q", ep.PathType)
				}
				if ep.Backend != "api-svc:8080" {
					t.Errorf("Backend = %q", ep.Backend)
				}
				if ep.SourceKind != "HTTPRoute" {
					t.Errorf("SourceKind = %q", ep.SourceKind)
				}
				if ep.RoutemapKey != routemapKey {
					t.Errorf("RoutemapKey mismatch")
				}
			},
		},
		{
			name: "two hostnames produce two endpoints",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "rt2", Namespace: testNS},
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{"a.example.com", "b.example.com"},
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{Path: &gatewayv1.HTTPPathMatch{Type: ptrOf(gatewayv1.PathMatchExact), Value: ptrOf("/")}},
							},
						},
					},
				},
			},
			wantLen: 2,
		},
		{
			name: "rule with no matches emits one endpoint per hostname",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "rt3", Namespace: testNS},
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{hostSvc},
					Rules: []gatewayv1.HTTPRouteRule{
						{
							BackendRefs: []gatewayv1.HTTPBackendRef{
								{BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{Name: "fallback"}}},
							},
						},
					},
				},
			},
			wantLen: 1,
			validate: func(t *testing.T, eps []Endpoint) {
				if eps[0].Path != "/" || eps[0].PathType != pathTypePrefix {
					t.Errorf("default path/type incorrect: path=%q type=%q", eps[0].Path, eps[0].PathType)
				}
				if eps[0].Backend != "fallback" {
					t.Errorf("Backend = %q, want fallback", eps[0].Backend)
				}
			},
		},
		{
			name: "health annotation picked up",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "rt4",
					Namespace:   testNS,
					Annotations: map[string]string{annotationKey: "/healthz"},
				},
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{hostSvc},
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{Path: &gatewayv1.HTTPPathMatch{Type: ptrOf(gatewayv1.PathMatchPathPrefix), Value: ptrOf("/")}},
							},
						},
					},
				},
			},
			wantLen: 1,
			validate: func(t *testing.T, eps []Endpoint) {
				if eps[0].HealthPath != "/healthz" {
					t.Errorf("HealthPath = %q", eps[0].HealthPath)
				}
			},
		},
		{
			name: "nil path match defaults to / Prefix",
			route: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "rt5", Namespace: testNS},
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{"x.example.com"},
					Rules: []gatewayv1.HTTPRouteRule{
						{
							Matches: []gatewayv1.HTTPRouteMatch{
								{}, // nil path
							},
						},
					},
				},
			},
			wantLen: 1,
			validate: func(t *testing.T, eps []Endpoint) {
				if eps[0].Path != "/" || eps[0].PathType != pathTypePrefix {
					t.Errorf("got path=%q type=%q", eps[0].Path, eps[0].PathType)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HTTPRouteToEndpoints(tc.route, routemapKey, annotationKey)
			if len(got) != tc.wantLen {
				t.Fatalf("got %d endpoints, want %d", len(got), tc.wantLen)
			}
			if tc.validate != nil {
				tc.validate(t, got)
			}
		})
	}
}
