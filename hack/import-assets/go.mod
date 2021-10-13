module github.com/openshift/cluster-capi-operator/hack/import-assets

go 1.16

require (
	github.com/jetstack/cert-manager v1.5.4
	github.com/pkg/errors v0.9.1
	k8s.io/api v0.22.2
	k8s.io/apiextensions-apiserver v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/client-go v0.22.2
	sigs.k8s.io/cluster-api v1.0.0
	sigs.k8s.io/cluster-api/exp/operator v0.0.0-00010101000000-000000000000
	sigs.k8s.io/controller-runtime v0.10.1
	sigs.k8s.io/yaml v1.2.0
)

replace (
	sigs.k8s.io/cluster-api => github.com/asalkeld/cluster-api v0.4.1-0.20210923065712-6ed39b7ef8f9
	sigs.k8s.io/cluster-api/exp/operator => github.com/asalkeld/cluster-api/exp/operator v0.0.0-20211005030408-a8791e47e147
)
