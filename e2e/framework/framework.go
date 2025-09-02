// Copyright 2024 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// CAPINamespace defines the namespace for cluster API resources.
	CAPINamespace = "openshift-cluster-api"
	// MAPINamespace defines the namespace for Machine API resources.
	MAPINamespace = "openshift-machine-api"
	// RetryShort defines a short rety duration for test operations.
	RetryShort = 1 * time.Second
	// RetryMedium defines a medium rety duration for test operations.
	RetryMedium = 5 * time.Second
	// RetryLong defines a long rety duration for test operations.
	RetryLong = 10 * time.Second
)

var (
	// WaitShort defines a short wait duration for test operations.
	WaitShort = 1 * time.Minute
	// WaitMedium defines a medium wait duration for test operations.
	WaitMedium = 3 * time.Minute
	// WaitLong defines a long wait duration for test operations.
	WaitLong = 15 * time.Minute
	// WaitOverLong defines an over long wait duration for test operations.
	WaitOverLong = 30 * time.Minute

	ctx = context.Background()
)

// DeleteObjects deletes the objects in the given list.
func DeleteObjects(cl client.Client, objs ...client.Object) {
	for _, o := range objs {
		By(fmt.Sprintf("Deleting %s/%s", o.GetObjectKind().GroupVersionKind().Kind, o.GetName()))
		Expect(cl.Delete(ctx, o)).To(Succeed())
	}
}

// GetContext returns a context.
func GetContext() context.Context {
	return context.Background()
}
