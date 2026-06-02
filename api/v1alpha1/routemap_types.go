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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SourceKind enumerates the discoverable resource types.
// +kubebuilder:validation:Enum=Ingress;HTTPRoute
type SourceKind string

const (
	SourceKindIngress   SourceKind = "Ingress"
	SourceKindHTTPRoute SourceKind = "HTTPRoute"
)

// NamespaceSelector defines which namespaces are scanned.
// Exactly one of WatchAll, Names, or Selector applies; WatchAll takes priority.
type NamespaceSelector struct {
	// WatchAll, when true, scans every namespace (overrides Names/Selector).
	// +optional
	WatchAll bool `json:"watchAll,omitempty"`

	// Names is an explicit allow-list of namespaces.
	// +optional
	Names []string `json:"names,omitempty"`

	// Selector matches namespaces by label.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

// HealthCheckSpec configures optional active probing of discovered endpoints.
type HealthCheckSpec struct {
	// Enabled turns on active probing.
	Enabled bool `json:"enabled"`

	// AnnotationKey overrides the annotation read from discovered
	// Ingress/HTTPRoute to find the health path.
	// +kubebuilder:default="routemap.github.com/healthcheck"
	// +optional
	AnnotationKey string `json:"annotationKey,omitempty"`

	// Interval between probes.
	// +kubebuilder:default="30s"
	// +optional
	Interval metav1.Duration `json:"interval,omitempty"`

	// Timeout per probe.
	// +kubebuilder:default="5s"
	// +optional
	Timeout metav1.Duration `json:"timeout,omitempty"`

	// ExpectedStatus is the HTTP status treated as healthy (default 200).
	// +kubebuilder:default=200
	// +optional
	ExpectedStatus int32 `json:"expectedStatus,omitempty"`
}

// SecretKeyRef points to a key in a Secret.
type SecretKeyRef struct {
	// Name is the Secret name.
	Name string `json:"name"`

	// Key inside the Secret.
	Key string `json:"key"`
}

// AuthSpec optionally protects a Routemap dashboard with OIDC.
type AuthSpec struct {
	// IssuerURL is the OIDC issuer (discovery document base URL).
	IssuerURL string `json:"issuerURL"`

	// ClientID for the OIDC relying party.
	ClientID string `json:"clientID"`

	// ClientSecretRef points to a Secret key holding the client secret.
	ClientSecretRef SecretKeyRef `json:"clientSecretRef"`

	// RedirectURL registered with the IdP for this dashboard.
	RedirectURL string `json:"redirectURL"`
}

// RoutemapSpec defines the desired state of Routemap.
type RoutemapSpec struct {
	// Namespaces chooses which namespaces to scan for Ingress/HTTPRoute.
	// Exactly one strategy applies; see NamespaceSelector.
	// +optional
	Namespaces NamespaceSelector `json:"namespaces,omitempty"`

	// Sources limits which resource kinds are discovered.
	// Defaults to both Ingress and HTTPRoute.
	// +kubebuilder:default={"Ingress","HTTPRoute"}
	// +optional
	Sources []SourceKind `json:"sources,omitempty"`

	// HealthCheck configures optional active probing of discovered endpoints.
	// +optional
	HealthCheck *HealthCheckSpec `json:"healthCheck,omitempty"`

	// Auth optionally protects this Routemap's dashboard with OIDC.
	// +optional
	Auth *AuthSpec `json:"auth,omitempty"`

	// DisplayName is shown as the dashboard title; defaults to the CR name.
	// +optional
	DisplayName string `json:"displayName,omitempty"`
}

// RoutemapStatus defines the observed state of Routemap.
type RoutemapStatus struct {
	// ObservedGeneration is the .metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// DiscoveredEndpoints is the number of consolidated endpoints.
	// +optional
	DiscoveredEndpoints int32 `json:"discoveredEndpoints,omitempty"`

	// HealthyEndpoints is the count passing health checks (0 if disabled).
	// +optional
	HealthyEndpoints int32 `json:"healthyEndpoints,omitempty"`

	// DashboardURL is the in-cluster path serving this Routemap.
	// +optional
	DashboardURL string `json:"dashboardURL,omitempty"`

	// Conditions represent the current state of the Routemap resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Endpoints",type=integer,JSONPath=".status.discoveredEndpoints"
// +kubebuilder:printcolumn:name="Healthy",type=integer,JSONPath=".status.healthyEndpoints"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Routemap is the Schema for the routemaps API.
type Routemap struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Routemap.
	// +required
	Spec RoutemapSpec `json:"spec"`

	// status defines the observed state of Routemap.
	// +optional
	Status RoutemapStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// RoutemapList contains a list of Routemap.
type RoutemapList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Routemap `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Routemap{}, &RoutemapList{})
}
