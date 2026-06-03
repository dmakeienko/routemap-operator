//go:build e2e
// +build e2e

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

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dmakeienko/routemap-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "routemap-operator-system"

// serviceAccountName created for the project
const serviceAccountName = "routemap-operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "routemap-operator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "routemap-operator-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// BeforeSuite already creates the namespace, installs CRDs, and deploys the controller.
	// Nothing additional needed here before the Manager tests run.
	BeforeAll(func() {})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				By("getting the name of the controller-manager pod")
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				By("validating the pod's status")
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			err := kubectlApply(fmt.Sprintf(`
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: %s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: routemap-operator-metrics-reader
subjects:
- kind: ServiceAccount
  name: %s
  namespace: %s
`, metricsRoleBindingName, serviceAccountName, namespace))
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd := exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			curlCmd := exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": [
								"for i in $(seq 1 30); do curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics && exit 0 || sleep 2; done; exit 1"
							],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(curlCmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
				g.Expect(metricsOutput).To(ContainSubstring("controller_runtime_reconcile_total"),
					"Prometheus metrics should include controller_runtime_reconcile_total")
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		// TODO: Customize the e2e test suite with scenarios specific to your project.
		// Consider applying sample/CR(s) and check their status and/or verifying
		// the reconciliation by using the metrics, i.e.:
		// metricsOutput, err := getMetricsOutput()
		// Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
		// Expect(metricsOutput).To(ContainSubstring(
		//    fmt.Sprintf(`controller_runtime_reconcile_total{controller="%s",result="success"} 1`,
		//    strings.ToLower(<Kind>),
		// ))
	})
})

// routemapTestNamespace is a dedicated namespace used by Routemap CR tests so they
// are isolated from the controller's own namespace and can be deleted cleanly.
const routemapTestNamespace = "routemap-e2e-test"

var _ = Describe("Routemap", Ordered, func() {
	BeforeAll(func() {
		By("creating test namespace for Routemap CRs")
		cmd := exec.Command("kubectl", "create", "ns", routemapTestNamespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create test namespace")
	})

	AfterAll(func() {
		By("deleting test namespace for Routemap CRs")
		cmd := exec.Command("kubectl", "delete", "ns", routemapTestNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("CR lifecycle", func() {
		const routemapName = "e2e-routemap"

		AfterEach(func() {
			// Best-effort removal so subsequent tests start clean.
			cmd := exec.Command("kubectl", "delete", "routemap", routemapName,
				"-n", routemapTestNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should reconcile and report Ready condition", func() {
			By("creating a Routemap CR that watches its own namespace")
			err := kubectlApply(fmt.Sprintf(`
apiVersion: routemaps.github.com/v1alpha1
kind: Routemap
metadata:
  name: %s
  namespace: %s
spec:
  displayName: "E2E Test Routemap"
  namespaces:
    names:
      - %s
  sources:
    - Ingress
`, routemapName, routemapTestNamespace, routemapTestNamespace))
			Expect(err).NotTo(HaveOccurred(), "Failed to create Routemap CR")

			By("waiting for the Routemap to have Ready=True condition")
			verifyReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "routemap", routemapName,
					"-n", routemapTestNamespace,
					"-o", `jsonpath={.status.conditions[?(@.type=='Ready')].status}`)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Routemap Ready condition not True")
			}
			Eventually(verifyReady).Should(Succeed())

			By("verifying ObservedGeneration is set on the status")
			genCmd := exec.Command("kubectl", "get", "routemap", routemapName,
				"-n", routemapTestNamespace,
				"-o", "jsonpath={.status.observedGeneration}")
			output, err := utils.Run(genCmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty(), "ObservedGeneration should be set")
			Expect(output).NotTo(Equal("0"), "ObservedGeneration should be non-zero")
		})

		It("should discover Ingresses and increment discoveredEndpoints", func() {
			By("creating a Routemap CR scoped to the test namespace")
			err := kubectlApply(fmt.Sprintf(`
apiVersion: routemaps.github.com/v1alpha1
kind: Routemap
metadata:
  name: %s
  namespace: %s
spec:
  namespaces:
    names:
      - %s
  sources:
    - Ingress
`, routemapName, routemapTestNamespace, routemapTestNamespace))
			Expect(err).NotTo(HaveOccurred(), "Failed to create Routemap CR")

			By("waiting for initial reconciliation")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "routemap", routemapName,
					"-n", routemapTestNamespace,
					"-o", `jsonpath={.status.conditions[?(@.type=='Ready')].status}`)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}).Should(Succeed())

			By("creating an Ingress in the watched namespace")
			err = kubectlApply(fmt.Sprintf(`
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: e2e-test-ingress
  namespace: %s
spec:
  rules:
    - host: e2e.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: fake-svc
                port:
                  number: 80
`, routemapTestNamespace))
			Expect(err).NotTo(HaveOccurred(), "Failed to create test Ingress")

			By("waiting for discoveredEndpoints to become >= 1")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "routemap", routemapName,
					"-n", routemapTestNamespace,
					"-o", "jsonpath={.status.discoveredEndpoints}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).NotTo(BeEmpty())
				g.Expect(output).NotTo(Equal("0"), "Expected at least one discovered endpoint")
			}).Should(Succeed())

			By("cleaning up the test Ingress")
			delCmd := exec.Command("kubectl", "delete", "ingress", "e2e-test-ingress",
				"-n", routemapTestNamespace, "--ignore-not-found")
			_, _ = utils.Run(delCmd)
		})

		It("should remove the finalizer and delete cleanly", func() {
			By("creating a Routemap CR")
			err := kubectlApply(fmt.Sprintf(`
apiVersion: routemaps.github.com/v1alpha1
kind: Routemap
metadata:
  name: %s
  namespace: %s
spec:
  namespaces:
    names:
      - %s
  sources:
    - Ingress
`, routemapName, routemapTestNamespace, routemapTestNamespace))
			Expect(err).NotTo(HaveOccurred(), "Failed to create Routemap CR")

			By("waiting for the finalizer to be set")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "routemap", routemapName,
					"-n", routemapTestNamespace,
					"-o", "jsonpath={.metadata.finalizers}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("routemaps.github.com/finalizer"))
			}).Should(Succeed())

			By("deleting the Routemap CR")
			delCmd := exec.Command("kubectl", "delete", "routemap", routemapName,
				"-n", routemapTestNamespace)
			_, err = utils.Run(delCmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to delete Routemap CR")

			By("verifying the Routemap CR is fully removed")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "routemap", routemapName,
					"-n", routemapTestNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "Routemap should be gone after deletion")
				g.Expect(err.Error()).To(ContainSubstring("NotFound"),
					"Expected NotFound error, got: %v", err)
			}).Should(Succeed())
		})
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	By("creating temporary file to store the token request")
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		By("executing kubectl command to create the token")
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		By("parsing the JSON output to extract the token")
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// kubectlApply writes yaml to a temp file and runs kubectl apply -f <file>.
// Using a file avoids stdin interaction issues with utils.Run's CombinedOutput.
func kubectlApply(yaml string) error {
	f, err := os.CreateTemp("", "e2e-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(yaml); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	cmd := exec.Command("kubectl", "apply", "-f", f.Name())
	_, err = utils.Run(cmd)
	return err
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
