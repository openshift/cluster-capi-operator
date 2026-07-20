// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package framework

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	CAPINamespace                      = "openshift-cluster-api"
	CAPIOperatorNamespace              = "openshift-cluster-api-operator"
	CompatibilityRequirementsNamespace = "openshift-compatibility-requirements-operator"
	MAPINamespace                      = "openshift-machine-api"

	RetryShort  = 1 * time.Second
	RetryMedium = 5 * time.Second
	RetryLong   = 10 * time.Second
)

var (
	WaitShort    = 1 * time.Minute
	WaitMedium   = 3 * time.Minute
	WaitLong     = 15 * time.Minute
	WaitOverLong = 30 * time.Minute
)

func resolveTimeout(defaultTimeout time.Duration, override []time.Duration) time.Duration {
	if len(override) > 0 {
		return override[0]
	}

	return defaultTimeout
}

// CreateAndCleanup creates an object and schedules its cleanup via DeferCleanup.
// The cleanup deletes the object and waits for it to be fully removed.
func CreateAndCleanup(ctx context.Context, cl client.Client, obj client.Object) {
	GinkgoHelper()

	Expect(cl.Create(ctx, obj)).To(Succeed(), "failed to create %T %s", obj, obj.GetName())

	DeferCleanup(func() {
		DeleteAndWait(ctx, cl, obj)
	})
}

// DeleteAndWait deletes an object and waits for it to be fully removed.
func DeleteAndWait(ctx context.Context, cl client.Client, obj client.Object, timeout ...time.Duration) {
	GinkgoHelper()

	key := client.ObjectKeyFromObject(obj)
	gvk := obj.GetObjectKind().GroupVersionKind()

	DeleteObjects(ctx, cl, obj)

	By(fmt.Sprintf("Waiting for %T %s to be fully deleted", obj, key.Name))

	Eventually(func() bool {
		probe := obj.DeepCopyObject().(client.Object)
		err := cl.Get(ctx, key, probe)

		return apierrors.IsNotFound(err)
	}).WithTimeout(resolveTimeout(WaitMedium, timeout)).WithPolling(RetryMedium).Should(BeTrue(),
		"expected %s %s to be deleted", gvk.Kind, key.Name)
}

// DeleteObjects deletes the objects in the given list, tolerating NotFound errors.
func DeleteObjects(ctx context.Context, cl client.Client, objs ...client.Object) {
	GinkgoHelper()

	for _, o := range objs {
		if o == nil {
			continue
		}

		By(fmt.Sprintf("Deleting %T %s", o, o.GetName()))
		Eventually(func() error {
			return cl.Delete(ctx, o)
		}, time.Minute, RetryShort).Should(SatisfyAny(
			Succeed(),
			WithTransform(apierrors.IsNotFound, BeTrue()),
		), "Should have successfully deleted %T %s, or it should not be found",
			o, o.GetName())
	}
}
