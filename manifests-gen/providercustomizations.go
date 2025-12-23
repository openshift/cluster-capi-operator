package main

import (
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func providerCustomizations(obj client.Object, providerName string) client.Object {
	switch providerName {
	case "azure":
		return azureCustomizations(obj)
	case "gcp":
		return gcpCustomizations(obj)
	}

	return obj
}

func azureCustomizations(obj client.Object) client.Object {
	switch getKind(obj) {
	case "Deployment":
		deployment := &appsv1.Deployment{}
		mustConvert(obj, deployment)

		// Modify bootstrap secret keys as they don't match with what is created by CCO.
		for i := range deployment.Spec.Template.Spec.Containers {
			container := &deployment.Spec.Template.Spec.Containers[i]
			if container.Name == "manager" {
				for j := range container.Env {
					env := &container.Env[j]
					switch env.Name {
					case "AZURE_SUBSCRIPTION_ID":
						env.ValueFrom.SecretKeyRef.Key = "azure_subscription_id"
					case "AZURE_TENANT_ID":
						env.ValueFrom.SecretKeyRef.Key = "azure_tenant_id"
					case "AZURE_CLIENT_ID":
						env.ValueFrom.SecretKeyRef.Key = "azure_client_id"
					case "AZURE_CLIENT_SECRET":
						env.ValueFrom.SecretKeyRef.Key = "azure_client_secret"
					}
				}
			}
		}

		return deployment
	}

	return obj
}

func gcpCustomizations(obj client.Object) client.Object {
	switch getKind(obj) {
	case "Deployment":
		deployment := &appsv1.Deployment{}
		mustConvert(obj, deployment)

		// Modify bootstrap secret keys as they don't match with what is created by CCO.
		for i := range deployment.Spec.Template.Spec.Containers {
			container := &deployment.Spec.Template.Spec.Containers[i]
			if container.Name == "manager" {
				for j := range container.Env {
					env := &container.Env[j]
					switch env.Name {
					case "GOOGLE_APPLICATION_CREDENTIALS":
						env.Value = "/home/.gcp/service_account.json"
					}
				}
			}
		}

		return deployment
	}

	return obj
}
