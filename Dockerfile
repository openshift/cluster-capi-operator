FROM registry.ci.openshift.org/openshift/release:golang-1.16 AS builder
WORKDIR /go/src/github.com/openshift/cluster-capi-operator
COPY . .
RUN make build

FROM registry.ci.openshift.org/openshift/origin-v4.8:base
COPY --from=builder /go/src/github.com/openshift/cluster-capi-operator/bin/meta-cluster-api-operator .

LABEL io.openshift.release.operator true
