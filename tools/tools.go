//go:build tools
// +build tools

// Official workaround to track tool dependencies with go modules:
// https://github.com/golang/go/wiki/Modules#how-can-i-track-tool-dependencies-for-a-module

package tools

import (
	_ "github.com/openshift/api/config/v1/zz_generated.crd-manifests"
	_ "github.com/openshift/api/machine/v1/zz_generated.crd-manifests"
	_ "github.com/openshift/api/machine/v1beta1/zz_generated.crd-manifests"
)
