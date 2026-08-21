package utils

import (
	policyreport "github.com/kyverno/kyverno/api/policyreport/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
)

var _ = Describe("TemplateAutomatedException", func() {
	var (
		scope                *corev1.ObjectReference
		report               policyreport.PolicyReport
		failedPolicies       []string
		destinationNamespace string
		got                  policyAPI.AutomatedException
	)

	BeforeEach(func() {
		scope = &corev1.ObjectReference{
			Kind:      "Deployment",
			Name:      "app-deployment",
			Namespace: "default",
			UID:       types.UID("e6d75155-e7bd-4df0-84d5-e1b2416cb2b9"),
		}

		report = policyreport.PolicyReport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e29eb7f4-6335-412c-b985-3fbbeb512bfb",
				Namespace: "default",
			},
			Scope: scope,
		}

		failedPolicies = []string{"require-run-as-nonroot"}
		destinationNamespace = "exceptions"

		got = TemplateAutomatedException(report, failedPolicies, destinationNamespace)
	})

	It("sets the resource UID as the Name", func() {
		Expect(got.Name).To(Equal(string(scope.UID)))
	})

	It("sets the destination namespace", func() {
		Expect(got.Namespace).To(Equal(destinationNamespace))
	})

	It("generates labels from the scope", func() {
		Expect(got.Labels).To(Equal(map[string]string{
			AppLabelName:       ComponentName,
			NameLabelName:      scope.Name,
			NamespaceLabelName: scope.Namespace,
			KindLabelName:      scope.Kind,
		}))
	})

	It("generates a target from the scope", func() {
		Expect(got.Spec.Targets).To(Equal([]policyAPI.Target{
			{
				Namespaces: []string{scope.Namespace},
				Names:      []string{scope.Name},
				Kind:       scope.Kind,
			},
		}))
	})

	It("sets the failed policies", func() {
		Expect(got.Spec.Policies).To(Equal(failedPolicies))
	})
})
