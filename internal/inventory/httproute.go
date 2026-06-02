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
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	sourceKindHTTPRoute = "HTTPRoute"
	defaultPathType     = "Prefix"
)

// HTTPRouteToEndpoints maps a single HTTPRoute resource into zero or more Endpoints.
func HTTPRouteToEndpoints(route *gatewayv1.HTTPRoute, routemapKey types.NamespacedName, annotationKey string) []Endpoint {
	healthPath := route.Annotations[annotationKey]
	sourceRef := types.NamespacedName{Namespace: route.Namespace, Name: route.Name}

	var endpoints []Endpoint
	for _, rule := range route.Spec.Rules {
		backend := backendFromRule(rule)
		matches := rule.Matches
		if len(matches) == 0 {
			// Rule with no matches still selects all paths — emit one endpoint per hostname.
			for _, hostname := range route.Spec.Hostnames {
				endpoints = append(endpoints, Endpoint{
					RoutemapKey: routemapKey,
					SourceKind:  sourceKindHTTPRoute,
					SourceRef:   sourceRef,
					Host:        string(hostname),
					Path:        "/",
					PathType:    defaultPathType,
					Backend:     backend,
					HealthPath:  healthPath,
					Health:      HealthStateUnknown,
				})
			}
			continue
		}
		for _, match := range matches {
			path, pathType := matchPath(match)
			for _, hostname := range route.Spec.Hostnames {
				endpoints = append(endpoints, Endpoint{
					RoutemapKey: routemapKey,
					SourceKind:  sourceKindHTTPRoute,
					SourceRef:   sourceRef,
					Host:        string(hostname),
					Path:        path,
					PathType:    pathType,
					Backend:     backend,
					HealthPath:  healthPath,
					Health:      HealthStateUnknown,
				})
			}
		}
	}
	return endpoints
}

func matchPath(match gatewayv1.HTTPRouteMatch) (path, pathType string) {
	if match.Path == nil {
		return "/", defaultPathType
	}
	if match.Path.Value != nil {
		path = *match.Path.Value
	} else {
		path = "/"
	}
	if match.Path.Type != nil {
		pathType = string(*match.Path.Type)
	} else {
		pathType = defaultPathType
	}
	return path, pathType
}

func backendFromRule(rule gatewayv1.HTTPRouteRule) string {
	if len(rule.BackendRefs) == 0 {
		return ""
	}
	ref := rule.BackendRefs[0]
	name := string(ref.Name)
	if ref.Port != nil {
		return name + ":" + intToStr(int(*ref.Port))
	}
	return name
}
