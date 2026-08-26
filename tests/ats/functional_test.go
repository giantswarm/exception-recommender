//go:build functional

package ats

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	wgpolicyk8s "github.com/kyverno/kyverno/api/policyreport/v1alpha2"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"

	"github.com/giantswarm/exception-recommender/internal/controller"
	"github.com/giantswarm/exception-recommender/tests"
)

const (
	destinationNamespace = "policy-exceptions"
	targetKind           = "Deployment"
	targetCategory       = "Pod Security Standards (Restricted)"
	resourceNamespace    = corev1.NamespaceDefault

	excludedNamespace  = metav1.NamespaceSystem
	untargetedKind     = "Pod"
	untargetedCategory = "Some Other Category"
)

func TestFunctional(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Functional Suite")
}

var (
	ctx       context.Context
	k8sClient client.Client
)

var _ = BeforeSuite(func() {
	ctx = context.Background()

	Expect(policyAPI.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(wgpolicyk8s.Install(scheme.Scheme)).To(Succeed())

	cfg, err := ctrl.GetConfig()
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: destinationNamespace}}
	err = k8sClient.Create(ctx, ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
})

func newPolicyManifest(name, mode string) *policyAPI.PolicyManifest {
	return &policyAPI.PolicyManifest{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: policyAPI.PolicyManifestSpec{
			Mode:                mode,
			Args:                []string{},
			Exceptions:          []policyAPI.Target{},
			AutomatedExceptions: []policyAPI.Target{},
		},
	}
}

func newPolicyReport(name, namespace, kind, category, policy string, result wgpolicyk8s.PolicyResult, uid types.UID) *wgpolicyk8s.PolicyReport {
	return &wgpolicyk8s.PolicyReport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Scope: &corev1.ObjectReference{
			APIVersion: "apps/v1",
			Kind:       kind,
			Name:       name,
			Namespace:  namespace,
			UID:        uid,
		},
		Results: []wgpolicyk8s.PolicyReportResult{
			{
				Category: category,
				Message:  "validation rule failed",
				Policy:   policy,
				Result:   result,
				Rule:     "run-as-nonroot",
				Scored:   true,
				Severity: "medium",
				Source:   "kyverno",
			},
		},
	}
}

func automatedExceptionFor(uid types.UID) (types.NamespacedName, *policyAPI.AutomatedException) {
	return types.NamespacedName{Name: string(uid), Namespace: destinationNamespace}, &policyAPI.AutomatedException{}
}

var _ = Describe("exception-recommender", func() {

	Describe("a failing policy with a warming PolicyManifest", Ordered, func() {
		policyName := tests.GenerateGUID("policy")
		reportName := tests.GenerateGUID("report")
		uid := types.UID(tests.GenerateGUID("uid"))

		BeforeAll(func() {
			Expect(k8sClient.Create(ctx, newPolicyManifest(policyName, controller.ManifestExpectedMode))).To(Succeed())
			Expect(k8sClient.Create(ctx, newPolicyReport(reportName, resourceNamespace, targetKind, targetCategory, policyName, wgpolicyk8s.StatusFail, uid))).To(Succeed())
		})

		AfterAll(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &wgpolicyk8s.PolicyReport{ObjectMeta: metav1.ObjectMeta{Name: reportName, Namespace: resourceNamespace}}))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &policyAPI.PolicyManifest{ObjectMeta: metav1.ObjectMeta{Name: policyName}}))).To(Succeed())
		})

		It("creates a matching AutomatedException", func() {
			key, automatedException := automatedExceptionFor(uid)
			Eventually(func() error {
				return k8sClient.Get(ctx, key, automatedException)
			}, "2m", "5s").Should(Succeed())

			Expect(automatedException.Spec.Policies).To(ContainElement(policyName))
			Expect(automatedException.Spec.Targets).To(ContainElement(policyAPI.Target{
				Namespaces: []string{resourceNamespace},
				Names:      []string{reportName},
				Kind:       targetKind,
			}))
		})

		It("deletes the AutomatedException once the report stops failing", func() {
			report := &wgpolicyk8s.PolicyReport{}
			key := types.NamespacedName{Name: reportName, Namespace: resourceNamespace}
			Expect(k8sClient.Get(ctx, key, report)).To(Succeed())

			report.Results = []wgpolicyk8s.PolicyReportResult{}
			Expect(k8sClient.Update(ctx, report)).To(Succeed())

			exceptionKey, automatedException := automatedExceptionFor(uid)
			Eventually(func() bool {
				err := k8sClient.Get(ctx, exceptionKey, automatedException)
				return apierrors.IsNotFound(err)
			}, "2m", "5s").Should(BeTrue())
		})
	})

	DescribeTable("reports the reconciler should ignore",
		func(namespace, kind, category, policy string) {
			reportName := tests.GenerateGUID("report")
			uid := types.UID(tests.GenerateGUID("uid"))

			report := newPolicyReport(reportName, namespace, kind, category, policy, wgpolicyk8s.StatusFail, uid)
			Expect(k8sClient.Create(ctx, report)).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, report))).To(Succeed())
			})

			key, automatedException := automatedExceptionFor(uid)
			Consistently(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, key, automatedException))
			}, "30s", "5s").Should(BeTrue())
		},
		Entry("excluded namespace", excludedNamespace, targetKind, targetCategory, tests.GenerateGUID("policy")),
		Entry("non-target workload kind", resourceNamespace, untargetedKind, targetCategory, tests.GenerateGUID("policy")),
		Entry("non-target category", resourceNamespace, targetKind, untargetedCategory, tests.GenerateGUID("policy")),
		Entry("policy without a registered PolicyManifest", resourceNamespace, targetKind, targetCategory, tests.GenerateGUID("policy")),
	)
})
