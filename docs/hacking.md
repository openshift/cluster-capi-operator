# Hacking

## image building

Setup your environment
```sh
REGISTRY=quay.io/asalkeld
CLUSTER_CAPI_IMAGE=quay.io/asalkeld/cluster-capi-operator:latest
```

### build upstream operator image

Note: This results in $REGISTRY/cluster-api-operator-amd64:dev

```sh
cd ${GOPATH}/src/sigs.k8s.io/cluster-api/exp/operator
```
I had to locally edit the Dockerfile to remove "--mount=type=cache" as podman doesn't support it.

```sh
make docker-build && make docker-push
```

### build downstream operator image

```sh
cd ${GOPATH}/src/github.com/openshift/cluster-capi-operator
podman build . -t ${CLUSTER_CAPI_IMAGE} && podman push ${CLUSTER_CAPI_IMAGE}
```

## running

```sh
cd ${GOPATH}/src/github.com/openshift/cluster-capi-operator
```

make sure your image references in:

- manifests/0000_30_capi-operator_01_images.configmap.yaml
- manifests/0000_30_capi-operator_11_deployment.yaml

point to the correct locations.

```sh
oc apply -f manifests
```

Note: all resources are created in "openshift-cluster-api" namespace.

### enable cluster-api in the featuregate

At this point only the downstream operator will be running, we need to enable
CAPI in the featuregate.

```sh
oc edit featuregate
```

set the spec to the following

```yaml
spec:
  featureSet: CustomNoUpgrade
  customNoUpgrade:
    enabled:
    - ClusterAPIEnabled
```

After this you should notice "oc get all,cm -n openshift-cluster-api" the upstream
operator and provider configmaps getting installed.

If the current platform is one of "aws,azure,gcp,metal3,openstack" then the
InfrastructureProvider CR will also be created. This will cause the upstream operator
to install the relevant provider.

On a metal3 platform the result should be:

```sh
oc get all,cm
NAME                                                    READY   STATUS    RESTARTS   AGE
pod/capi-controller-manager-6c8b76f9f4-4sb4r            1/1     Running   0          11m
pod/capi-operator-controller-manager-6bb45454c9-pj7sv   2/2     Running   0          16m
pod/capm3-controller-manager-656795c95-42xb2            1/1     Running   0          16m
pod/cluster-capi-operator-5b95fdd459-r85j8              1/1     Running   0          16m

NAME                                                       TYPE        CLUSTER-IP       EXTERNAL-IP   PORT(S)    AGE
service/capi-operator-controller-manager-metrics-service   ClusterIP   172.30.108.132   <none>        8443/TCP   19m
service/capi-webhook-service                               ClusterIP   172.30.38.112    <none>        443/TCP    19m
service/capm3-webhook-service                              ClusterIP   172.30.242.96    <none>        443/TCP    18m

NAME                                               READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/capi-controller-manager            1/1     1            1           19m
deployment.apps/capi-operator-controller-manager   1/1     1            1           19m
deployment.apps/capm3-controller-manager           1/1     1            1           18m
deployment.apps/cluster-capi-operator              1/1     1            1           30m

NAME                                                          DESIRED   CURRENT   READY   AGE
replicaset.apps/capi-controller-manager-6c8b76f9f4            1         1         1       19m
replicaset.apps/capi-operator-controller-manager-6bb45454c9   1         1         1       19m
replicaset.apps/capm3-controller-manager-656795c95            1         1         1       18m
replicaset.apps/cluster-capi-operator-5b95fdd459              1         1         1       30m

NAME                                                 DATA   AGE
configmap/aws-v0.7.0                                 2      19m
configmap/azure-v0.5.2                               2      19m
configmap/cluster-api-v1.0.0                         2      19m
configmap/cluster-capi-operator-images               1      30m
configmap/cluster-capi-operator-leader               0      30m
configmap/controller-leader-election-capi-operator   0      19m
configmap/gcp-v0.4.0                                 2      19m
configmap/kube-root-ca.crt                           1      30m
configmap/metal3-v0.5.2                              2      19m
configmap/openshift-service-ca.crt                   1      30m
configmap/openstack-v0.4.0                           2      19m
```
