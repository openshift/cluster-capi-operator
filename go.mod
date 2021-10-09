module github.com/openshift/cluster-capi-operator

go 1.16

require (
	github.com/go-logr/logr v1.0.0 // indirect
	github.com/google/go-cmp v0.5.6
	github.com/openshift/api v0.0.0-20210831091943-07e756545ac1
	github.com/openshift/library-go v0.0.0-20210914071953-94a0fd1d5849
	github.com/spf13/pflag v1.0.5
	k8s.io/api v0.22.2
	k8s.io/apiextensions-apiserver v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/client-go v0.22.2
	k8s.io/component-base v0.22.2
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.10.0
	k8s.io/utils v0.0.0-20210819203725-bdf08cb9a70a
	sigs.k8s.io/cluster-api v0.4.3 // indirect
	sigs.k8s.io/cluster-api/exp/operator v0.0.0-00010101000000-000000000000
	sigs.k8s.io/controller-runtime v0.10.1
)

replace (
	github.com/go-logr/logr => github.com/go-logr/logr v0.4.0
	sigs.k8s.io/cluster-api => github.com/asalkeld/cluster-api v0.4.1-0.20210923065712-6ed39b7ef8f9
	sigs.k8s.io/cluster-api/exp/operator => github.com/asalkeld/cluster-api/exp/operator v0.0.0-20211005030408-a8791e47e147
)
