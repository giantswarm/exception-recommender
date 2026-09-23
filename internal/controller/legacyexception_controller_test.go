package controller

import (
	"context"
	"time"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/exception-recommender/internal/migration"
)

var _ = Describe("LegacyException controller", Ordered, func() {
	const (
		timeout         = 10 * time.Second
		interval        = 250 * time.Millisecond
		policyName      = "envtest-policy"
		sourceNamespace = "default"
	)
	bridgeKey := types.NamespacedName{Namespace: bridgeNamespace, Name: "envtest-app-migrated"}
	var src *kyvernov2.PolicyException

	BeforeAll(func() {
		policy := &kyvernov1.ClusterPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: policyName},
			Spec: kyvernov1.Spec{Rules: []kyvernov1.Rule{{
				Name: "check",
				MatchResources: kyvernov1.MatchResources{Any: kyvernov1.ResourceFilters{
					{ResourceDescription: kyvernov1.ResourceDescription{Kinds: []string{kindPod}}},
				}},
				Validation: &kyvernov1.Validation{
					Message:    "containers must be named",
					RawPattern: &apiextensionsv1.JSON{Raw: []byte(`{"spec":{"containers":[{"name":"?*"}]}}`)},
				},
			}}},
		}
		Expect(k8sClient.Create(context.Background(), policy)).To(Succeed())

		src = &kyvernov2.PolicyException{
			ObjectMeta: metav1.ObjectMeta{Name: "envtest-app", Namespace: sourceNamespace},
			Spec: kyvernov2.PolicyExceptionSpec{
				Exceptions: []kyvernov2.Exception{{PolicyName: policyName, RuleNames: []string{"check"}}},
			},
		}
		src.Spec.Match.Any = kyvernov1.ResourceFilters{{ResourceDescription: kyvernov1.ResourceDescription{
			Kinds: []string{"Deployment", kindPod}, Namespaces: []string{sourceNamespace}, Names: []string{"envtest-app*"},
		}}}
		Expect(k8sClient.Create(context.Background(), src)).To(Succeed())
	})

	It("writes a labelled and annotated bridge without owner references", func() {
		Eventually(func(g Gomega) {
			var bridge policyAPI.PolicyException
			g.Expect(k8sClient.Get(context.Background(), bridgeKey, &bridge)).To(Succeed())
			g.Expect(bridge.Labels).To(HaveKeyWithValue(migration.ManagedByLabel, migration.ComponentName))
			g.Expect(bridge.Annotations).To(HaveKeyWithValue(migration.AnnotationMigratedFrom, "default/envtest-app"))
			g.Expect(bridge.OwnerReferences).To(BeEmpty())
			g.Expect(bridge.Spec.Policies).To(Equal([]string{policyName}))
			g.Expect(bridge.Spec.Targets).To(HaveLen(2))
		}, timeout, interval).Should(Succeed())
	})

	It("restores a bridge deleted by hand", func() {
		bridge := &policyAPI.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: bridgeKey.Name, Namespace: bridgeKey.Namespace}}
		Expect(k8sClient.Delete(context.Background(), bridge)).To(Succeed())
		Eventually(func() error {
			return k8sClient.Get(context.Background(), bridgeKey, &policyAPI.PolicyException{})
		}, timeout, interval).Should(Succeed())
	})

	It("keeps the bridge unchanged when the source turns lossy", func() {
		var before policyAPI.PolicyException
		Expect(k8sClient.Get(context.Background(), bridgeKey, &before)).To(Succeed())
		Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(src), src)).To(Succeed())
		src.Spec.Exceptions[0].RuleNames = []string{"not-check"}
		Expect(k8sClient.Update(context.Background(), src)).To(Succeed())
		Consistently(func(g Gomega) {
			var bridge policyAPI.PolicyException
			g.Expect(k8sClient.Get(context.Background(), bridgeKey, &bridge)).To(Succeed())
			g.Expect(bridge.ResourceVersion).To(Equal(before.ResourceVersion))
		}, 2*time.Second, interval).Should(Succeed())
	})

	It("deletes the bridge when the source is deleted", func() {
		Expect(k8sClient.Delete(context.Background(), src)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(context.Background(), bridgeKey, &policyAPI.PolicyException{})
			return apierrors.IsNotFound(err)
		}, timeout, interval).Should(BeTrue())
	})

	It("removes a bridge whose source is already gone, as after a restart", func() {
		orphan := &policyAPI.PolicyException{
			ObjectMeta: metav1.ObjectMeta{
				Name: "gone-migrated", Namespace: bridgeNamespace,
				Labels:      map[string]string{migration.ManagedByLabel: migration.ComponentName},
				Annotations: map[string]string{migration.AnnotationMigratedFrom: "default/gone"},
			},
			Spec: policyAPI.PolicyExceptionSpec{Policies: []string{policyName}, Targets: []policyAPI.Target{}},
		}
		Expect(k8sClient.Create(context.Background(), orphan)).To(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(orphan), &policyAPI.PolicyException{})
			return apierrors.IsNotFound(err)
		}, timeout, interval).Should(BeTrue())
	})

	It("leaves a hand-written gspolex alone", func() {
		handWritten := &policyAPI.PolicyException{
			ObjectMeta: metav1.ObjectMeta{Name: "hand-written-migrated", Namespace: bridgeNamespace,
				Annotations: map[string]string{migration.AnnotationMigratedFrom: "default/hand-written"}},
			Spec: policyAPI.PolicyExceptionSpec{Policies: []string{policyName}, Targets: []policyAPI.Target{}},
		}
		Expect(k8sClient.Create(context.Background(), handWritten)).To(Succeed())
		Consistently(func() error {
			return k8sClient.Get(context.Background(), client.ObjectKeyFromObject(handWritten), &policyAPI.PolicyException{})
		}, 2*time.Second, interval).Should(Succeed())
	})
})
