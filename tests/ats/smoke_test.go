//go:build smoke

package ats

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSmoke(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Smoke Suite")
}

var k8sClient client.Client

var _ = BeforeSuite(func() {
	cfg, err := ctrl.GetConfig()
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
})

var _ = Describe("exception-recommender", func() {
	It("becomes available after installation", func() {
		Eventually(func(g Gomega) int32 {
			var deployments appsv1.DeploymentList

			g.Expect(k8sClient.List(context.Background(), &deployments, client.MatchingLabels{
				"app.kubernetes.io/name": "exception-recommender",
			})).To(Succeed())
			g.Expect(deployments.Items).To(HaveLen(1))

			return deployments.Items[0].Status.AvailableReplicas
		}, "3m", "5s").Should(BeNumerically(">=", 1))
	})
})
