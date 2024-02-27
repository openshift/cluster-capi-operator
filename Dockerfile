FROM registry.ci.openshift.org/openshift/release:golang-1.21 AS builder
WORKDIR /go/src/github.com/openshift/cluster-capi-operator
COPY . .
RUN make build

FROM registry.ci.openshift.org/ocp/4.16:base
COPY --from=builder /go/src/github.com/openshift/cluster-capi-operator/bin/cluster-capi-operator .
COPY --from=builder /go/src/github.com/openshift/cluster-capi-operator/manifests /manifests

LABEL io.openshift.release.operator true
