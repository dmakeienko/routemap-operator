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
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// HealthState represents the last-known health of an endpoint.
type HealthState string

const (
	HealthStateUnknown   HealthState = "Unknown"
	HealthStateHealthy   HealthState = "Healthy"
	HealthStateUnhealthy HealthState = "Unhealthy"
)

// Endpoint is the normalized internal representation of a single discovered route.
type Endpoint struct {
	RoutemapKey types.NamespacedName
	SourceKind  string // "Ingress" | "HTTPRoute"
	SourceRef   types.NamespacedName
	Host        string
	Path        string
	PathType    string // Prefix | Exact | RegularExpression
	Backend     string // "service:port" best-effort
	TLS         bool
	HealthPath  string // from annotation, "" if absent
	Health      HealthState
	LastProbe   time.Time
}
