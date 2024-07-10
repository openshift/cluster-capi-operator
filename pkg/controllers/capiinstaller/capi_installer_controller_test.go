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
package capiinstaller

import (
	"encoding/base64"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("CAPI installer", func() {
})

var testManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-deployment
  labels:
    app: nginx
spec:
  replicas: 3
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:1.14.2
        ports:
        - containerPort: 80
`

var compressedTestManifest, _ = base64.StdEncoding.DecodeString(`KLUv/WRVAKUFAMJJHRpwS9sI3ybpZYS0vG6WI53uKS2q/sjfqn/fGiZQ+DVbiGaLBQnhJOcWVPoj
P8tV7F3oz8bEKvs/LFCj+tlqlCnbbwa/hkgTJ0PJzbylVGD6FcSxuuwvFe5vDhsV7FSmN9VjJU7D
T1llmNMANGb87dLGMqGMARgAWBQQEYAQCvkpJv1I3WSA+nysTB5YQDAEenUBAfCBAbcQMQZEzbCI
A4ZgXM9oWwU9mtEEu8ByN5uCApSQX14=
`)

var _ = Describe("extractManifests", func() {
	testCases := []struct {
		name              string
		configMap         corev1.ConfigMap
		expectedManifests []string
		expectedError     error
	}{
		{
			name: "ConfigMap with components data",
			configMap: corev1.ConfigMap{
				Data: map[string]string{
					"components": testManifest,
				},
			},
			expectedManifests: []string{testManifest},
			expectedError:     nil,
		},
		{
			name: "ConfigMap with compressed components data",
			configMap: corev1.ConfigMap{
				BinaryData: map[string][]byte{
					"components-zstd": compressedTestManifest,
				},
			},
			expectedManifests: []string{testManifest},
			expectedError:     nil,
		},
		{
			name:      "ConfigMap without components data",
			configMap: corev1.ConfigMap{
				// No components data
			},
			expectedManifests: nil,
			expectedError:     errors.New("provider configmap has no components data"),
		},
	}

	for _, tc := range testCases {
		tc := tc
		It(tc.name, func() {
			manifests, err := extractManifests(tc.configMap)

			if tc.expectedError != nil {
				Expect(err).To(MatchError(tc.expectedError))
			} else {
				Expect(err).To(BeNil())
			}

			Expect(manifests).To(Equal(tc.expectedManifests))
		})
	}
})
