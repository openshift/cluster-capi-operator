// Copyright 2026 Red Hat, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bindata

import (
	"path/filepath"
	"testing"

	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
)

func TestAssets(t *testing.T) {
	assets, err := Assets.ReadAssets()
	if err != nil {
		t.Fatalf("failed to read assets: %v", err)
	}

	for _, asset := range assets {
		t.Run(asset.Name(), func(t *testing.T) {
			asset, err := Assets.Asset(filepath.Join("assets", asset.Name()))
			if err != nil {
				t.Fatalf("failed to read asset: %v", err)
			}

			// Check that each asset can be read as a runtime.Object
			_, err = resourceread.ReadGenericWithUnstructured(asset)
			if err != nil {
				t.Fatalf("failed to read asset: %v", err)
			}
		})
	}
}
