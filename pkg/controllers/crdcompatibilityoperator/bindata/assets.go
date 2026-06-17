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
	"embed"
	"fmt"
	"io/fs"
)

//go:embed assets/*
var f embed.FS

// Assets is a singleton instance of the assets struct.
var Assets = &assets{} //nolint:gochecknoglobals

type assets struct{}

// Asset reads and returns the content of the named file.
func (a *assets) Asset(name string) ([]byte, error) {
	data, err := f.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", name, err)
	}

	return data, nil
}

// ReadAssets reads and returns the contents of the assets directory.
func (a *assets) ReadAssets() ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(f, "assets")
	if err != nil {
		return nil, fmt.Errorf("failed to read assets directory: %w", err)
	}

	return entries, nil
}
