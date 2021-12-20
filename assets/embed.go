package assets

import (
	"embed"
	"path"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed capi-operator/*.yaml core-capi/*.yaml
var fs embed.FS

func FromDir(dir string, scheme *runtime.Scheme) ([]client.Object, error) {
	assetNames, err := fs.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	objs := []client.Object{}
	for _, assetName := range assetNames {
		b, err := fs.ReadFile(path.Join(dir, assetName.Name()))
		if err != nil {
			return nil, err
		}
		codecs := serializer.NewCodecFactory(scheme)
		obj, _, err := codecs.UniversalDeserializer().Decode(b, nil, nil)
		if err != nil {
			return nil, err
		}
		objs = append(objs, obj.(client.Object))
	}
	return objs, nil
}
