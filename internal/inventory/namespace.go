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
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	routemapsv1alpha1 "github.com/dmakeienko/routemap-operator/api/v1alpha1"
)

// ResolveNamespaces returns the namespace names that match spec.
// A nil return means "all namespaces" (WatchAll was set).
func ResolveNamespaces(ctx context.Context, c client.Client, spec routemapsv1alpha1.NamespaceSelector) ([]string, error) {
	if spec.WatchAll {
		return nil, nil
	}
	if len(spec.Names) > 0 {
		return spec.Names, nil
	}
	if spec.Selector != nil {
		sel, err := selectorFromMeta(spec.Selector)
		if err != nil {
			return nil, err
		}
		var nsList corev1.NamespaceList
		if err := c.List(ctx, &nsList, &client.ListOptions{LabelSelector: sel}); err != nil {
			return nil, err
		}
		names := make([]string, 0, len(nsList.Items))
		for _, ns := range nsList.Items {
			names = append(names, ns.Name)
		}
		return names, nil
	}
	// No selector: caller should default to the Routemap's own namespace.
	return []string{}, nil
}

func selectorFromMeta(ls *metav1.LabelSelector) (labels.Selector, error) {
	return metav1.LabelSelectorAsSelector(ls)
}
