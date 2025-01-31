/*
Copyright 2022 Red Hat, Inc.

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

package capiinstaller

func providerNameToImageKey(name string) string {
	switch name {
	case "aws":
		return "aws-cluster-api-controllers"
	case "azure":
		return "azure-cluster-api-controllers"
	case "gcp":
		return "gcp-cluster-api-controllers"
	case "ibmcloud":
		return "ibmcloud-cluster-api-controllers"
	case "openstack":
		return "openstack-cluster-api-controllers"
	case "vsphere":
		return "vsphere-cluster-api-controllers"
	case "cluster-api":
		return "cluster-capi-controllers"
	default:
		return "none"
	}
}

func providerNameToCommand(name string) string {
	switch name {
	case "aws", "gcp", "ibmcloud":
		return "./bin/cluster-api-provider-" + name + "-controller-manager"
	case "cluster-api":
		return "./bin/cluster-api-controller-manager"
	default:
		return "/manager"
	}
}
