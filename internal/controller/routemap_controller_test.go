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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	routemapsv1alpha1 "github.com/dmakeienko/routemap-operator/api/v1alpha1"
	"github.com/dmakeienko/routemap-operator/internal/inventory"
	"github.com/dmakeienko/routemap-operator/internal/store"
)

const dashboardAddr = "http://localhost:9090"

var _ = Describe("Routemap Controller", func() {
	const (
		routemapName = "test-routemap"
		testNS       = "default"
		timeout      = 5 * time.Second
		interval     = 100 * time.Millisecond
	)

	ctx := context.Background()
	key := types.NamespacedName{Name: routemapName, Namespace: testNS}

	newReconciler := func() *RoutemapReconciler {
		return &RoutemapReconciler{
			Client:             k8sClient,
			Scheme:             k8sClient.Scheme(),
			Store:              store.New(),
			DashboardBindAddr:  dashboardAddr,
			HTTPRouteAvailable: false,
		}
	}

	Context("When reconciling a Routemap with no sources", func() {
		BeforeEach(func() {
			By("Creating the Routemap CR")
			rm := &routemapsv1alpha1.Routemap{
				ObjectMeta: metav1.ObjectMeta{Name: routemapName, Namespace: testNS},
				Spec:       routemapsv1alpha1.RoutemapSpec{},
			}
			err := k8sClient.Get(ctx, key, &routemapsv1alpha1.Routemap{})
			if apierrors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, rm)).To(Succeed())
			}
		})

		AfterEach(func() {
			rm := &routemapsv1alpha1.Routemap{}
			if err := k8sClient.Get(ctx, key, rm); err == nil {
				rm.Finalizers = nil
				_ = k8sClient.Update(ctx, rm)
				_ = k8sClient.Delete(ctx, rm)
			}
		})

		It("should reconcile without error and set status", func() {
			r := newReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile (adds finalizer on first, reconciles on second).
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				var rm routemapsv1alpha1.Routemap
				if err := k8sClient.Get(ctx, key, &rm); err != nil {
					return false
				}
				return rm.Status.DashboardURL != ""
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("When reconciling a Routemap with an Ingress in scope", func() {
		const ingressName = "test-ingress"
		prefixType := networkingv1.PathTypePrefix

		BeforeEach(func() {
			By("Creating the Ingress")
			ing := &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: ingressName, Namespace: testNS},
				Spec: networkingv1.IngressSpec{
					Rules: []networkingv1.IngressRule{
						{
							Host: "app.example.com",
							IngressRuleValue: networkingv1.IngressRuleValue{
								HTTP: &networkingv1.HTTPIngressRuleValue{
									Paths: []networkingv1.HTTPIngressPath{
										{
											Path:     "/",
											PathType: &prefixType,
											Backend: networkingv1.IngressBackend{
												Service: &networkingv1.IngressServiceBackend{
													Name: "app-svc",
													Port: networkingv1.ServiceBackendPort{Number: 80},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}
			existing := &networkingv1.Ingress{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: ingressName, Namespace: testNS}, existing); apierrors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, ing)).To(Succeed())
			}

			By("Creating the Routemap CR")
			rm := &routemapsv1alpha1.Routemap{
				ObjectMeta: metav1.ObjectMeta{Name: routemapName, Namespace: testNS},
				Spec: routemapsv1alpha1.RoutemapSpec{
					Sources: []routemapsv1alpha1.SourceKind{routemapsv1alpha1.SourceKindIngress},
				},
			}
			if err := k8sClient.Get(ctx, key, &routemapsv1alpha1.Routemap{}); apierrors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, rm)).To(Succeed())
			}
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: ingressName, Namespace: testNS},
			})
			rm := &routemapsv1alpha1.Routemap{}
			if err := k8sClient.Get(ctx, key, rm); err == nil {
				rm.Finalizers = nil
				_ = k8sClient.Update(ctx, rm)
				_ = k8sClient.Delete(ctx, rm)
			}
		})

		It("should discover the Ingress and populate Store", func() {
			s := store.New()
			r := &RoutemapReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				Store:              s,
				DashboardBindAddr:  dashboardAddr,
				HTTPRouteAvailable: false,
			}

			// First reconcile adds finalizer.
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile does discovery.
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			view, ok := s.Get(key)
			Expect(ok).To(BeTrue())
			Expect(view.Endpoints).To(HaveLen(1))
			Expect(view.Endpoints[0].Host).To(Equal("app.example.com"))
			Expect(view.Endpoints[0].Backend).To(Equal("app-svc:80"))
		})

		It("should set DiscoveredEndpoints in status", func() {
			s := store.New()
			r := &RoutemapReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				Store:              s,
				DashboardBindAddr:  dashboardAddr,
				HTTPRouteAvailable: false,
			}
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})

			Eventually(func() int32 {
				var rm routemapsv1alpha1.Routemap
				if err := k8sClient.Get(ctx, key, &rm); err != nil {
					return -1
				}
				return rm.Status.DiscoveredEndpoints
			}, timeout, interval).Should(BeNumerically(">=", int32(1)))
		})
	})

	Context("When a Routemap is deleted", func() {
		It("should remove the view from Store on not-found", func() {
			s := store.New()
			notFoundKey := types.NamespacedName{Name: "gone", Namespace: testNS}
			s.Set(notFoundKey, store.RoutemapView{})

			r := &RoutemapReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				Store:              s,
				DashboardBindAddr:  dashboardAddr,
				HTTPRouteAvailable: false,
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: notFoundKey})
			Expect(err).NotTo(HaveOccurred())
			_, ok := s.Get(notFoundKey)
			Expect(ok).To(BeFalse())
		})
	})

	Context("Helper functions", func() {
		It("effectiveSources returns defaults when nil", func() {
			sources := effectiveSources(nil)
			Expect(sources).To(ContainElements(
				routemapsv1alpha1.SourceKindIngress,
				routemapsv1alpha1.SourceKindHTTPRoute,
			))
		})

		It("defaultAnnotationKey returns custom key when set", func() {
			key := defaultAnnotationKey(&routemapsv1alpha1.HealthCheckSpec{AnnotationKey: "custom/key"})
			Expect(key).To(Equal("custom/key"))
		})

		It("defaultAnnotationKey returns default when empty", func() {
			key := defaultAnnotationKey(nil)
			Expect(key).To(Equal("routemap.github.com/healthcheck"))
		})

		It("containsString works correctly", func() {
			Expect(containsString([]string{"a", "b"}, "a")).To(BeTrue())
			Expect(containsString([]string{"a", "b"}, "c")).To(BeFalse())
		})

		It("removeString removes the target", func() {
			result := removeString([]string{"a", "b", "c"}, "b")
			Expect(result).To(Equal([]string{"a", "c"}))
		})

		It("namespaceMatchesRoutemap covers watchAll", func() {
			rm := routemapsv1alpha1.Routemap{
				Spec: routemapsv1alpha1.RoutemapSpec{
					Namespaces: routemapsv1alpha1.NamespaceSelector{WatchAll: true},
				},
			}
			Expect(namespaceMatchesRoutemap("any-ns", rm)).To(BeTrue())
		})

		It("namespaceMatchesRoutemap covers explicit Names", func() {
			rm := routemapsv1alpha1.Routemap{
				Spec: routemapsv1alpha1.RoutemapSpec{
					Namespaces: routemapsv1alpha1.NamespaceSelector{Names: []string{"prod"}},
				},
			}
			Expect(namespaceMatchesRoutemap("prod", rm)).To(BeTrue())
			Expect(namespaceMatchesRoutemap("staging", rm)).To(BeFalse())
		})

		It("namespaceMatchesRoutemap defaults to own namespace", func() {
			rm := routemapsv1alpha1.Routemap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "myns"},
			}
			Expect(namespaceMatchesRoutemap("myns", rm)).To(BeTrue())
			Expect(namespaceMatchesRoutemap("other", rm)).To(BeFalse())
		})

		It("countHealthy counts only Healthy endpoints", func() {
			eps := []inventory.Endpoint{
				{Health: inventory.HealthStateHealthy},
				{Health: inventory.HealthStateUnhealthy},
				{Health: inventory.HealthStateUnknown},
			}
			Expect(countHealthy(eps)).To(Equal(1))
		})
	})
})
