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
package metadata

const (
	// CAPIOperatorPrefix is used wherever CAPI Operator needs a qualified name.
	CAPIOperatorPrefix = "cluster-api.openshift.io"

	//
	// Annotation keys.
	//

	// CAPIOperatorProviderNameKey identifies the name of a provider in a transport configmap.
	CAPIOperatorProviderNameKey = CAPIOperatorPrefix + "/provider-name"

	// CAPIOperatorProviderVersionKey identifies the version of a provider in a transport configmap.
	CAPIOperatorProviderVersionKey = CAPIOperatorPrefix + "/provider-version"

	// CAPIOperatorContentIDKey uniquely identifies the content of a provider manifest bundle.
	CAPIOperatorContentIDKey = CAPIOperatorPrefix + "/content-id"

	// CAPIOperatorBundleSizeKey identifies the number of transport configmaps in a provider manifest bundle.
	CAPIOperatorBundleSizeKey = CAPIOperatorPrefix + "/bundle-size"

	// CAPIOperatorBundleIndexKey identifies the index of a transport configmap in a provider manifest bundle.
	CAPIOperatorBundleIndexKey = CAPIOperatorPrefix + "/bundle-index"

	//
	// Label keys.
	//

	// CAPIOperatorPlatformKey identifies the OpenShift platform of a provider manifest bundle is relevant to.
	CAPIOperatorPlatformKey = CAPIOperatorPrefix + "/platform"

	// CAPIOperatorProviderTypeKey identifies the type of provider in a provider manifest bundle.
	CAPIOperatorProviderTypeKey = CAPIOperatorPrefix + "/provider-type"

	// CAPIOperatorOpenshiftReleaseKey identifies the OpenShift release a provider manifest bundle is relevant to.
	CAPIOperatorOpenshiftReleaseKey = CAPIOperatorPrefix + "/openshift-release"
)
