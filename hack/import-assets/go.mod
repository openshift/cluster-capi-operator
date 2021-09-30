module github.com/openshift/cluster-capi-operator/hack/import-assets

go 1.16

require (
	github.com/pkg/errors v0.9.1
	k8s.io/api v0.22.2
	k8s.io/apiextensions-apiserver v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/client-go v0.22.2
	sigs.k8s.io/cluster-api v1.0.0
	sigs.k8s.io/yaml v1.2.0
)

replace sigs.k8s.io/cluster-api => github.com/asalkeld/cluster-api v0.4.1-0.20210923065712-6ed39b7ef8f9
