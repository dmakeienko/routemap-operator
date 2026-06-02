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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
)

// IngressToEndpoints maps a single Ingress resource into zero or more Endpoints.
// routemapKey identifies the owning Routemap. annotationKey is the annotation
// consulted for the health-check path (e.g. "routemap.github.com/healthcheck").
func IngressToEndpoints(ing *networkingv1.Ingress, routemapKey types.NamespacedName, annotationKey string) []Endpoint {
	tlsHosts := buildTLSHostSet(ing)
	healthPath := ing.Annotations[annotationKey]
	sourceRef := types.NamespacedName{Namespace: ing.Namespace, Name: ing.Name}

	var endpoints []Endpoint
	for _, rule := range ing.Spec.Rules {
		host := rule.Host
		if rule.HTTP == nil {
			continue
		}
		for _, p := range rule.HTTP.Paths {
			ep := Endpoint{
				RoutemapKey: routemapKey,
				SourceKind:  "Ingress",
				SourceRef:   sourceRef,
				Host:        host,
				Path:        p.Path,
				PathType:    pathTypeString(p.PathType),
				TLS:         tlsHosts[host],
				HealthPath:  healthPath,
				Health:      HealthStateUnknown,
			}
			if p.Backend.Service != nil {
				ep.Backend = p.Backend.Service.Name + ":" + portString(p.Backend.Service.Port)
			}
			endpoints = append(endpoints, ep)
		}
	}
	return endpoints
}

func buildTLSHostSet(ing *networkingv1.Ingress) map[string]bool {
	hosts := make(map[string]bool)
	for _, t := range ing.Spec.TLS {
		for _, h := range t.Hosts {
			hosts[h] = true
		}
	}
	return hosts
}

func pathTypeString(pt *networkingv1.PathType) string {
	if pt == nil {
		return ""
	}
	return string(*pt)
}

func portString(port networkingv1.ServiceBackendPort) string {
	if port.Name != "" {
		return port.Name
	}
	if port.Number != 0 {
		return intToStr(int(port.Number))
	}
	return ""
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
