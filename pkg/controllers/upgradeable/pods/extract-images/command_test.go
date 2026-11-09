/*
Copyright 2026 Red Hat, Inc.

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
package extractimages

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-capi-operator/pkg/controllers/upgradeable/pods"
)

func TestExtractImages(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upgradeable Payload Images")
}

var _ = Describe("Payload image extraction", func() {
	ctx := context.Background()

	It("should fail terminally when the payload manifest is missing", func() {
		manifest := filepath.Join(GinkgoT().TempDir(), "absent.yaml")

		// The payload mount is immutable, so a missing manifest can never be repaired in-process.
		err := Step(ctx, nil, "4.16.0", "4.17.0", manifest)
		Expect(err).To(MatchError(ContainSubstring("reading payload manifest")))
		Expect(pods.IsTerminal(err)).To(BeTrue())
	})

	It("should fail terminally when a target payload contains no provider images", func() {
		manifest := filepath.Join(GinkgoT().TempDir(), "images.yaml")
		Expect(os.WriteFile(manifest, []byte("apiVersion: v1\nkind: ConfigMap\ndata: {}\n"), 0600)).To(Succeed())

		// An empty payload is valid only when there is no target; a real target must
		// never silently assess an empty provider image set.
		err := Step(ctx, nil, "4.16.0", "4.17.0", manifest)
		Expect(err).To(MatchError(errInvalidManifest))
		Expect(pods.IsTerminal(err)).To(BeTrue())
	})
})
