package assets

import (
	"embed"
)

//go:embed capi-operator/*.yaml
var FS embed.FS
