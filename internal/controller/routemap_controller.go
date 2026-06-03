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

package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"

	routemapsv1alpha1 "github.com/dmakeienko/routemap-operator/api/v1alpha1"
	"github.com/dmakeienko/routemap-operator/internal/inventory"
	"github.com/dmakeienko/routemap-operator/internal/store"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	requeueSafetyInterval = 5 * time.Minute
	finalizerName         = "routemaps.github.com/finalizer"

	conditionReady       = "Ready"
	conditionProgressing = "Progressing"
	conditionDegraded    = "Degraded"
)

// RoutemapReconciler reconciles a Routemap object.
type RoutemapReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	Store              *store.Store
	DashboardBindAddr  string
	HTTPRouteAvailable bool // set by SetupWithManager after RESTMapper probe
}

// +kubebuilder:rbac:groups=routemaps.github.com,resources=routemaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=routemaps.github.com,resources=routemaps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=routemaps.github.com,resources=routemaps/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *RoutemapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var rm routemapsv1alpha1.Routemap
	if err := r.Get(ctx, req.NamespacedName, &rm); err != nil {
		if apierrors.IsNotFound(err) {
			r.Store.Delete(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion.
	if !rm.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &rm)
	}

	// Ensure finalizer.
	if !containsString(rm.Finalizers, finalizerName) {
		rm.Finalizers = append(rm.Finalizers, finalizerName)
		if err := r.Update(ctx, &rm); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Resolve namespace set.
	namespaces, err := inventory.ResolveNamespaces(ctx, r.Client, rm.Spec.Namespaces)
	if err != nil {
		log.Error(err, "Failed to resolve namespace selector")
		return ctrl.Result{}, err
	}
	// Empty namespaces list (not watchAll) → default to own namespace.
	if !rm.Spec.Namespaces.WatchAll && len(namespaces) == 0 && rm.Spec.Namespaces.Selector == nil {
		namespaces = []string{rm.Namespace}
	}

	annotationKey := defaultAnnotationKey(rm.Spec.HealthCheck)
	sources := effectiveSources(rm.Spec.Sources)

	var endpoints []inventory.Endpoint

	// Discover Ingress resources.
	if containsSource(sources, routemapsv1alpha1.SourceKindIngress) {
		eps, err := r.collectIngresses(ctx, req.NamespacedName, namespaces, annotationKey, rm.Spec.Namespaces.WatchAll)
		if err != nil {
			log.Error(err, "Failed to collect Ingresses")
			return ctrl.Result{}, err
		}
		endpoints = append(endpoints, eps...)
	}

	// Discover HTTPRoute resources.
	if containsSource(sources, routemapsv1alpha1.SourceKindHTTPRoute) && r.HTTPRouteAvailable {
		eps, err := r.collectHTTPRoutes(ctx, req.NamespacedName, namespaces, annotationKey, rm.Spec.Namespaces.WatchAll)
		if err != nil {
			log.Error(err, "Failed to collect HTTPRoutes")
			return ctrl.Result{}, err
		}
		endpoints = append(endpoints, eps...)
	}

	// Merge last-known health states from Store so async probes are not lost.
	mergeHealth(r.Store, req.NamespacedName, endpoints)

	// Publish to Store.
	r.Store.Set(req.NamespacedName, store.RoutemapView{
		Spec:      rm.Spec,
		Endpoints: endpoints,
	})

	// Build status.
	healthy := countHealthy(endpoints)
	dashURL := fmt.Sprintf("%s/%s/%s", r.DashboardBindAddr, rm.Namespace, rm.Name)

	httprouteUnavailable := containsSource(sources, routemapsv1alpha1.SourceKindHTTPRoute) && !r.HTTPRouteAvailable

	// Re-fetch before status update to avoid conflicts.
	var fresh routemapsv1alpha1.Routemap
	if err := r.Get(ctx, req.NamespacedName, &fresh); err != nil {
		return ctrl.Result{}, err
	}
	fresh.Status.ObservedGeneration = fresh.Generation
	fresh.Status.DiscoveredEndpoints = int32(len(endpoints))
	fresh.Status.HealthyEndpoints = int32(healthy)
	fresh.Status.DashboardURL = dashURL

	setReadyCondition(&fresh, httprouteUnavailable)

	if err := r.Status().Update(ctx, &fresh); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Reconciled Routemap", "endpoints", len(endpoints), "httprouteAvailable", r.HTTPRouteAvailable)
	return ctrl.Result{RequeueAfter: requeueSafetyInterval}, nil
}

func (r *RoutemapReconciler) handleDeletion(ctx context.Context, rm *routemapsv1alpha1.Routemap) (ctrl.Result, error) {
	key := types.NamespacedName{Namespace: rm.Namespace, Name: rm.Name}
	r.Store.Delete(key)

	rm.Finalizers = removeString(rm.Finalizers, finalizerName)
	if err := r.Update(ctx, rm); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *RoutemapReconciler) collectIngresses(
	ctx context.Context,
	routemapKey types.NamespacedName,
	namespaces []string,
	annotationKey string,
	watchAll bool,
) ([]inventory.Endpoint, error) {
	if watchAll {
		var list networkingv1.IngressList
		if err := r.List(ctx, &list); err != nil {
			return nil, err
		}
		return mapIngresses(list.Items, routemapKey, annotationKey), nil
	}
	var endpoints []inventory.Endpoint
	for _, ns := range namespaces {
		var list networkingv1.IngressList
		if err := r.List(ctx, &list, client.InNamespace(ns)); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, mapIngresses(list.Items, routemapKey, annotationKey)...)
	}
	return endpoints, nil
}

func (r *RoutemapReconciler) collectHTTPRoutes(
	ctx context.Context,
	routemapKey types.NamespacedName,
	namespaces []string,
	annotationKey string,
	watchAll bool,
) ([]inventory.Endpoint, error) {
	if watchAll {
		var list gatewayv1.HTTPRouteList
		if err := r.List(ctx, &list); err != nil {
			return nil, err
		}
		return mapHTTPRoutes(list.Items, routemapKey, annotationKey), nil
	}
	var endpoints []inventory.Endpoint
	for _, ns := range namespaces {
		var list gatewayv1.HTTPRouteList
		if err := r.List(ctx, &list, client.InNamespace(ns)); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, mapHTTPRoutes(list.Items, routemapKey, annotationKey)...)
	}
	return endpoints, nil
}

func mapIngresses(items []networkingv1.Ingress, key types.NamespacedName, annotKey string) []inventory.Endpoint {
	out := make([]inventory.Endpoint, 0, len(items))
	for i := range items {
		out = append(out, inventory.IngressToEndpoints(&items[i], key, annotKey)...)
	}
	return out
}

func mapHTTPRoutes(items []gatewayv1.HTTPRoute, key types.NamespacedName, annotKey string) []inventory.Endpoint {
	out := make([]inventory.Endpoint, 0, len(items))
	for i := range items {
		out = append(out, inventory.HTTPRouteToEndpoints(&items[i], key, annotKey)...)
	}
	return out
}

// mergeHealth copies last-known health states from the store into the newly built endpoint list.
func mergeHealth(s *store.Store, key types.NamespacedName, endpoints []inventory.Endpoint) {
	view, ok := s.Get(key)
	if !ok {
		return
	}
	prev := make(map[types.NamespacedName]inventory.HealthState, len(view.Endpoints))
	for _, ep := range view.Endpoints {
		prev[ep.SourceRef] = ep.Health
	}
	for i := range endpoints {
		if h, ok := prev[endpoints[i].SourceRef]; ok {
			endpoints[i].Health = h
		}
	}
}

func countHealthy(endpoints []inventory.Endpoint) int {
	n := 0
	for _, ep := range endpoints {
		if ep.Health == inventory.HealthStateHealthy {
			n++
		}
	}
	return n
}

func setReadyCondition(rm *routemapsv1alpha1.Routemap, httprouteUnavailable bool) {
	now := metav1.Now()
	if httprouteUnavailable {
		meta.SetStatusCondition(&rm.Status.Conditions, metav1.Condition{
			Type:               conditionDegraded,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: rm.Generation,
			LastTransitionTime: now,
			Reason:             "HTTPRouteCRDAbsent",
			Message:            "HTTPRoute CRD is not installed; HTTPRoute discovery is disabled",
		})
		meta.SetStatusCondition(&rm.Status.Conditions, metav1.Condition{
			Type:               conditionReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: rm.Generation,
			LastTransitionTime: now,
			Reason:             "Degraded",
			Message:            "HTTPRoute CRD is not installed",
		})
		return
	}
	meta.SetStatusCondition(&rm.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: rm.Generation,
		LastTransitionTime: now,
		Reason:             "Reconciled",
		Message:            "Routemap reconciled successfully",
	})
	meta.RemoveStatusCondition(&rm.Status.Conditions, conditionDegraded)
}

// SetupWithManager configures the controller, probes for HTTPRoute CRD availability,
// and registers watches.
func (r *RoutemapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Probe HTTPRoute CRD via RESTMapper.
	mapper := mgr.GetRESTMapper()
	_, err := mapper.RESTMapping(schema.GroupKind{Group: gatewayv1.GroupName, Kind: "HTTPRoute"})
	r.HTTPRouteAvailable = err == nil

	log := mgr.GetLogger().WithName("routemap-setup")
	if !r.HTTPRouteAvailable {
		log.Info("HTTPRoute CRD not found; HTTPRoute discovery will be disabled")
	}

	b := ctrl.NewControllerManagedBy(mgr).
		For(&routemapsv1alpha1.Routemap{}).
		Watches(
			&networkingv1.Ingress{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueAffectedRoutemaps),
			builder.WithPredicates(),
		).
		Watches(
			&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueAllRoutemaps),
			builder.WithPredicates(),
		)

	if r.HTTPRouteAvailable {
		b = b.Watches(
			&gatewayv1.HTTPRoute{},
			handler.EnqueueRequestsFromMapFunc(r.enqueueAffectedRoutemaps),
			builder.WithPredicates(),
		)
	}

	return b.Named("routemap").Complete(r)
}

// enqueueAffectedRoutemaps maps a changed Ingress or HTTPRoute back to every
// Routemap whose namespace selection covers the object's namespace.
func (r *RoutemapReconciler) enqueueAffectedRoutemaps(ctx context.Context, obj client.Object) []reconcile.Request {
	var rmList routemapsv1alpha1.RoutemapList
	if err := r.List(ctx, &rmList); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, rm := range rmList.Items {
		if namespaceMatchesRoutemap(obj.GetNamespace(), rm) {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: rm.Namespace, Name: rm.Name},
			})
		}
	}
	return requests
}

// enqueueAllRoutemaps requeues all Routemaps when a Namespace changes (label
// changes affect selector-based namespace resolution).
func (r *RoutemapReconciler) enqueueAllRoutemaps(ctx context.Context, _ client.Object) []reconcile.Request {
	var rmList routemapsv1alpha1.RoutemapList
	if err := r.List(ctx, &rmList); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(rmList.Items))
	for _, rm := range rmList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: rm.Namespace, Name: rm.Name},
		})
	}
	return requests
}

// namespaceMatchesRoutemap returns true if the given namespace is covered by the Routemap's selector.
func namespaceMatchesRoutemap(ns string, rm routemapsv1alpha1.Routemap) bool {
	if rm.Spec.Namespaces.WatchAll {
		return true
	}
	if slices.Contains(rm.Spec.Namespaces.Names, ns) {
		return true
	}
	// Selector-based matching: if a selector is set we optimistically requeue
	// rather than performing a full label lookup inside a map function.
	if rm.Spec.Namespaces.Selector != nil {
		return true
	}
	// Default: own namespace.
	return ns == rm.Namespace
}

func defaultAnnotationKey(hc *routemapsv1alpha1.HealthCheckSpec) string {
	if hc != nil && hc.AnnotationKey != "" {
		return hc.AnnotationKey
	}
	return "routemap.github.com/healthcheck"
}

func effectiveSources(sources []routemapsv1alpha1.SourceKind) []routemapsv1alpha1.SourceKind {
	if len(sources) == 0 {
		return []routemapsv1alpha1.SourceKind{
			routemapsv1alpha1.SourceKindIngress,
			routemapsv1alpha1.SourceKindHTTPRoute,
		}
	}
	return sources
}

func containsSource(sources []routemapsv1alpha1.SourceKind, kind routemapsv1alpha1.SourceKind) bool {
	return slices.Contains(sources, kind)
}

func containsString(slice []string, s string) bool {
	return slices.Contains(slice, s)
}

func removeString(slice []string, s string) []string {
	out := slice[:0]
	for _, v := range slice {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}
