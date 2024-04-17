/*

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
// +kubebuilder:docs-gen:collapse=Apache License

package controller

import (
	"context"

	"time"

	wgpolicyk8s "github.com/kyverno/kyverno/api/policyreport/v1alpha2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
)

var _ = Describe("PolicyReport controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		PolicyReportName       = "e29eb7f4-6335-412c-b985-3fbbeb512bfb"
		PolicyReportNamespace  = "default"
		PolicyCategory         = "Pod Security Standards (Restricted)"
		PolicyName             = "require-run-as-nonroot"
		PolicyRuleName         = "run-as-nonroot"
		PolicyManifestMode     = "warming"
		AutomatedExceptionName = "app-deployment-deployment"
		ResourceName           = "app-deployment"
		ResourceNamespace      = "default"
		ResourceKind           = "Deployment"
		ResourveAPIVersion     = "apps/v1"
		ResourceUID            = "e6d75155-e7bd-4df0-84d5-e1b2416cb2b9"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Describe("reconciling a PolicyReport", Ordered, func() {
		BeforeAll(func() {
			logger := zap.New(zap.WriteTo(GinkgoWriter))
			ctx = log.IntoContext(context.Background(), logger)

			// Create PolicyReport
			policyReport := &wgpolicyk8s.PolicyReport{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "wgpolicyk8s.io/v1alpha2",
					Kind:       "PolicyReport",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      PolicyReportName,
					Namespace: PolicyReportNamespace,
				},
				Scope: &corev1.ObjectReference{
					APIVersion: ResourveAPIVersion,
					Kind:       ResourceKind,
					Name:       ResourceName,
					Namespace:  ResourceNamespace,
					UID:        ResourceUID,
				},
				Results: []wgpolicyk8s.PolicyReportResult{
					{
						Category: PolicyCategory,
						Message:  "validation rule 'run-as-nonroot' failed",
						Policy:   PolicyName,
						Result:   "fail",
						Rule:     PolicyRuleName,
						Scored:   true,
						Severity: "medium",
						Source:   "kyverno",
						Timestamp: metav1.Timestamp{
							Nanos:   0,
							Seconds: 0,
						},
					},
				},
			}

			// Create Giant Swarm PolicyManifest
			policyManifest := &policyAPI.PolicyManifest{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "policy.giantswarm.io/v1alpha1",
					Kind:       "PolicyManifest",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: PolicyName,
				},
				Spec: policyAPI.PolicyManifestSpec{
					Mode: PolicyManifestMode,
					// The following fields are not necessary for this test, so they are omitted
					Args:                []string{},
					Exceptions:          []policyAPI.Target{},
					AutomatedExceptions: []policyAPI.Target{},
				},
			}

			Expect(k8sClient.Create(ctx, policyManifest)).Should(Succeed())
			Expect(k8sClient.Create(ctx, policyReport)).Should(Succeed())
		})

		// TODO: Replace name with UID
		automatedExceptionLookupKey := types.NamespacedName{Name: AutomatedExceptionName, Namespace: destinationNamespace}
		automatedException := policyAPI.AutomatedException{}

		When("a PolicyReport is created", func() {
			It("must create a Giant Swarm AutomatedException", func() {
				Eventually(func() bool {
					err := k8sClient.Get(ctx, automatedExceptionLookupKey, &automatedException)
					return err == nil
				}, timeout, interval).Should(BeTrue())
			})
		})
	})

})
