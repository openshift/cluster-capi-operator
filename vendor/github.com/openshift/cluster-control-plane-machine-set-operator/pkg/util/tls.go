/*
Copyright 2023 Red Hat, Inc.

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

package util

import (
	"crypto/tls"
)

// GetAllowedTLSCipherSuites returns a slice of security vetted TLS CipherSuites.
func GetAllowedTLSCipherSuites() []uint16 {
	defaultTLSSuites := tls.CipherSuites()

	insecure := map[uint16]interface{}{
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA: nil,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA: nil,
		tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA:   nil,
		tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA:   nil,
		tls.TLS_RSA_WITH_AES_128_CBC_SHA:         nil,
		tls.TLS_RSA_WITH_AES_128_GCM_SHA256:      nil,
		tls.TLS_RSA_WITH_AES_256_CBC_SHA:         nil,
	}

	included := make([]uint16, 0, len(defaultTLSSuites)-len(insecure))

	for _, s := range defaultTLSSuites {
		if _, contains := insecure[s.ID]; contains {
			// The processed suite is insecure, don't include it.
			continue
		}

		included = append(included, s.ID)
	}

	return included
}
