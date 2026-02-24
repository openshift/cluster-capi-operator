/*
Copyright 2025 Red Hat, Inc.

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

package staticresourceinstaller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/cluster-capi-operator/pkg/controllers/crdcompatibility/bindata"
)

var _ = Describe("StaticResourceInstaller Controller", Ordered, ContinueOnFailure, func() {
	var (
		ctx        context.Context
		controller *staticResourceInstallerController
		startMgr   func()
	)

	BeforeAll(func() {
		ctx = context.Background()
		controller, startMgr = InitManager(ctx, bindata.Assets)
	})

	Describe("Asset Loading", func() {
		It("should read assets from bindata", func() {
			Expect(controller.assetNames).NotTo(BeEmpty(), "Controller should have loaded asset names")

			By("Verifying expected assets are loaded")
			expectedAssets := []string{
				"assets/compatibility-requirements-compatibility-requirement-webhook.yaml",
				"assets/compatibility-requirements-custom-resource-definition-webhook.yaml",
			}

			Expect(controller.assetNames).To(ConsistOf(expectedAssets))
		})
	})

	Describe("Manager Integration", Ordered, func() {
		BeforeAll(func() {
			startMgr()
		})

		It("should install webhook configurations when reconciled", func() {
			By("Verifying that ValidatingWebhookConfigurations are created")
			Eventually(kWithCtx(ctx).ObjectList(&admissionregistrationv1.ValidatingWebhookConfigurationList{}), 10*time.Second).WithContext(ctx).Should(HaveField("Items", SatisfyAll(
				ContainElement(HaveField("ObjectMeta.Name", Equal("openshift-compatibility-requirements-apiextensions-openshift-io-v1alpha1-compatibilityrequirement-validation"))),
				ContainElement(HaveField("ObjectMeta.Name", Equal("openshift-compatibility-requirements-apiextensions-k8s-io-v1-customresourcedefinition-validation"))),
			)))
		})

		It("should reset the resource when it is modified", func() {
			// Fetch a resource that is expected to exist in the previous test.
			vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "openshift-compatibility-requirements-apiextensions-openshift-io-v1alpha1-compatibilityrequirement-validation",
				},
			}
			Expect(kWithCtx(ctx).Get(vwc)()).To(Succeed())

			Eventually(kWithCtx(ctx).Update(vwc, func() {
				vwc.Webhooks[0].ClientConfig.Service.Name = "test"
			})).WithContext(ctx).Should(Succeed())

			By("Verifying that the resource is reset")
			Eventually(kWithCtx(ctx).Object(vwc), 10*time.Second).WithContext(ctx).Should(HaveField("Webhooks", ConsistOf(HaveField("ClientConfig.Service.Name", Not(Equal("test"))))))
		})

		It("should recreate the resource when it is deleted", func() {
			By("Deleting the resource")
			// Check that we can fetch the resource first, and then try to delete it.
			vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name: "openshift-compatibility-requirements-apiextensions-openshift-io-v1alpha1-compatibilityrequirement-validation",
				},
			}
			Expect(kWithCtx(ctx).Get(vwc)()).To(Succeed())
			originalUID := vwc.GetUID()

			Expect(cl.Delete(ctx, vwc)).To(Succeed())

			By("Verifying that the resource is recreated")
			Eventually(kWithCtx(ctx).Object(vwc), 10*time.Second).WithContext(ctx).Should(HaveField("ObjectMeta.UID", Not(Equal(originalUID))))
		})
	})
})
