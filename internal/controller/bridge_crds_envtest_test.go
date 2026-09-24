package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var _ = Describe("Migration bridge CRD check", func() {
	It("reports kinds of an absent group or version as missing through the dynamic RESTMapper", func() {
		httpClient, err := rest.HTTPClientFor(cfg)
		Expect(err).NotTo(HaveOccurred())
		mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
		Expect(err).NotTo(HaveOccurred())
		absentGroup := schema.GroupVersionKind{Group: "absent.example.com", Version: "v1", Kind: "Thing"}
		absentVersion := schema.GroupVersionKind{Group: "kyverno.io", Version: "v99", Kind: "PolicyException"}

		missing, err := missingKinds(mapper, []schema.GroupVersionKind{absentGroup, absentVersion, bridgeCRDs[0]})
		Expect(err).NotTo(HaveOccurred())
		Expect(missing).To(Equal([]string{absentGroup.String(), absentVersion.String()}))
	})

	It("finds every bridge CRD served", func() {
		Expect(MissingBridgeCRDs(cfg)).To(BeEmpty())
	})

	It("stops the watcher once every bridge CRD is served", func() {
		w := &BridgeCRDWatcher{
			Check:    func() ([]string, error) { return MissingBridgeCRDs(cfg) },
			Interval: 10 * time.Millisecond,
			Log:      logger,
		}
		Expect(w.Start(ctx)).To(MatchError(ErrBridgeCRDsAvailable))
	})

	It("stops the manager with an error that main recognises", func() {
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{Metrics: server.Options{BindAddress: "0"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.Add(&BridgeCRDWatcher{
			Check:    func() ([]string, error) { return MissingBridgeCRDs(cfg) },
			Interval: 10 * time.Millisecond,
			Log:      logger,
		})).To(Succeed())
		Expect(mgr.Start(ctx)).To(MatchError(ErrBridgeCRDsAvailable))
	})
})
