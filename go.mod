module github.com/openshift/cluster-capi-operator

go 1.16

require (
	github.com/go-logr/logr v1.0.0
	github.com/google/go-cmp v0.5.6
	github.com/metal3-io/cluster-api-provider-metal3/api v0.0.0-20211109111512-82c0c0c7fbf5
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
	k8s.io/utils v0.0.0-20210930125809-cb0fa318a74b
	sigs.k8s.io/cluster-api v1.0.0
	sigs.k8s.io/cluster-api-provider-aws v1.0.0
	sigs.k8s.io/cluster-api-provider-azure v1.0.0
	sigs.k8s.io/cluster-api-provider-gcp v0.4.0
	sigs.k8s.io/cluster-api-provider-openstack v0.4.0
	sigs.k8s.io/cluster-api/exp/operator v0.0.0-00010101000000-000000000000
	sigs.k8s.io/controller-runtime v0.10.3-0.20211011182302-43ea648ec318
)

replace (
	github.com/go-logr/logr => github.com/go-logr/logr v0.4.0
	sigs.k8s.io/cluster-api => github.com/asalkeld/cluster-api v0.4.1-0.20210923065712-6ed39b7ef8f9
	sigs.k8s.io/cluster-api/exp/operator => github.com/asalkeld/cluster-api/exp/operator v0.0.0-20211005030408-a8791e47e147
)
