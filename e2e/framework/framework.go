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
	CAPINamespace = "openshift-cluster-api"
	MAPINamespace = "openshift-machine-api"

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
