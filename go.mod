module github.com/openshift/cluster-capi-operator

go 1.16

require (
	github.com/go-logr/logr v1.0.0 // indirect
	github.com/google/go-cmp v0.5.6 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/openshift/api v0.0.0-20210831091943-07e756545ac1
	github.com/openshift/library-go v0.0.0-20210914071953-94a0fd1d5849
	github.com/spf13/pflag v1.0.5
	golang.org/x/net v0.0.0-20210716203947-853a461950ff // indirect
	k8s.io/api v0.22.1
	k8s.io/apiextensions-apiserver v0.22.1
	k8s.io/apimachinery v0.22.1
	k8s.io/client-go v0.22.1
	k8s.io/component-base v0.22.1
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.10.0
	k8s.io/utils v0.0.0-20210802155522-efc7438f0176 // indirect
	sigs.k8s.io/controller-runtime v0.9.6
)

replace github.com/go-logr/logr => github.com/go-logr/logr v0.4.0
