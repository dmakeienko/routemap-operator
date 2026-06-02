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

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	testNS         = "prod"
	pathTypePrefix = "Prefix"
	hostSecure     = "secure.example.com"
	hostSvc        = "svc.example.com"
)

func TestIngressToEndpoints(t *testing.T) {
	prefixType := networkingv1.PathTypePrefix
	exactType := networkingv1.PathTypeExact

	routemapKey := types.NamespacedName{Namespace: "default", Name: "my-routemap"}
	annotationKey := "routemap.github.com/healthcheck"

	tests := []struct {
		name     string
		ingress  *networkingv1.Ingress
		wantLen  int
		validate func(t *testing.T, eps []Endpoint)
	}{
		{
			name: "single rule single path no TLS",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: "ing1", Namespace: testNS},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{
						{
							Host: "api.example.com",
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{
											Path:     "/v1",
											PathType: &prefixType,
											Backend: networkingv1.IngressBackend{
												Service: &networkingv1.IngressServiceBackend{
													Name: "api-svc",
													Port: networkingv1.ServiceBackendPort{Number: 8080},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantLen: 1,
			validate: func(t *testing.T, eps []Endpoint) {
				ep := eps[0]
				if ep.Host != "api.example.com" {
					t.Errorf("Host = %q, want api.example.com", ep.Host)
				}
				if ep.Path != "/v1" {
					t.Errorf("Path = %q, want /v1", ep.Path)
				}
				if ep.PathType != pathTypePrefix {
					t.Errorf("PathType = %q, want %s", ep.PathType, pathTypePrefix)
				}
				if ep.Backend != "api-svc:8080" {
					t.Errorf("Backend = %q, want api-svc:8080", ep.Backend)
				}
				if ep.TLS {
					t.Error("TLS should be false")
				}
				if ep.SourceKind != "Ingress" {
					t.Errorf("SourceKind = %q, want Ingress", ep.SourceKind)
				}
				if ep.SourceRef.Name != "ing1" || ep.SourceRef.Namespace != testNS {
					t.Errorf("SourceRef = %v, unexpected", ep.SourceRef)
				}
				if ep.RoutemapKey != routemapKey {
					t.Errorf("RoutemapKey = %v, want %v", ep.RoutemapKey, routemapKey)
				}
			},
		},
		{
			name: "TLS host detection",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: "ing2", Namespace: testNS},
				Spec: networkingv1.IngressSpec{
					TLS: []networkingv1.IngressTLS{
						{Hosts: []string{hostSecure}},
					},
					Rules: []networkingv1.IngressRule{
						{
							Host: hostSecure,
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{Path: "/", PathType: &prefixType, Backend: networkingv1.IngressBackend{}},
									},
								},
							},
						},
						{
							Host: "plain.example.com",
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{Path: "/", PathType: &prefixType, Backend: networkingv1.IngressBackend{}},
									},
								},
							},
						},
					},
				},
			},
			wantLen: 2,
			validate: func(t *testing.T, eps []Endpoint) {
				for _, ep := range eps {
					if ep.Host == hostSecure && !ep.TLS {
						t.Errorf("%s should have TLS=true", hostSecure)
					}
					if ep.Host == "plain.example.com" && ep.TLS {
						t.Error("plain.example.com should have TLS=false")
					}
				}
			},
		},
		{
			name: "health annotation picked up",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "ing3",
					Namespace:   testNS,
					Annotations: map[string]string{annotationKey: "/_health"},
				},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{
						{
							Host: hostSvc,
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{Path: "/", PathType: &exactType, Backend: networkingv1.IngressBackend{}},
									},
								},
							},
						},
					},
				},
			},
			wantLen: 1,
			validate: func(t *testing.T, eps []Endpoint) {
				if eps[0].HealthPath != "/_health" {
					t.Errorf("HealthPath = %q, want /_health", eps[0].HealthPath)
				}
			},
		},
		{
			name: "rule with nil HTTP produces no endpoints",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: "ing4", Namespace: testNS},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{{Host: "empty.example.com"}},
				},
			},
			wantLen: 0,
		},
		{
			name: "backend with named port",
			ingress: &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: "ing5", Namespace: testNS},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{
						{
							Host: hostSvc,
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{
											Path:     "/",
											PathType: &prefixType,
											Backend: networkingv1.IngressBackend{
												Service: &networkingv1.IngressServiceBackend{
													Name: "my-svc",
													Port: networkingv1.ServiceBackendPort{Name: "http"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantLen: 1,
			validate: func(t *testing.T, eps []Endpoint) {
				if eps[0].Backend != "my-svc:http" {
					t.Errorf("Backend = %q, want my-svc:http", eps[0].Backend)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IngressToEndpoints(tc.ingress, routemapKey, annotationKey)
			if len(got) != tc.wantLen {
				t.Fatalf("got %d endpoints, want %d", len(got), tc.wantLen)
			}
			if tc.validate != nil {
				tc.validate(t, got)
			}
		})
	}
}
