package main

import "sigs.k8s.io/controller-runtime/pkg/client"

func getKind(obj client.Object) string {
	return obj.GetObjectKind().GroupVersionKind().Kind
}

func getGroup(obj client.Object) string {
	return obj.GetObjectKind().GroupVersionKind().Group
}

func convert[T client.Object](from client.Object, to T) error {
	if err := scheme.Convert(from, to, nil); err != nil {
		return err
	}
	to.GetObjectKind().SetGroupVersionKind(from.GetObjectKind().GroupVersionKind())
	return nil
}

func mustConvert[T client.Object](from client.Object, to T) {
	if err := convert(from, to); err != nil {
		panic(err)
	}
}
