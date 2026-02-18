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

package crdchecker

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/crdify/pkg/config"
	"sigs.k8s.io/crdify/pkg/validations"
)

/*
This validation extends crdify functionality to allow it to check for the removal of _served_ versions.

Traditionally, crdify wouldn't care about the removal of served versions, this is a normal part of the lifecycle of a CRD.

Over time, as APIs evolve, it's expected that new versions are introduced, eventually switch to storage, and that the old
versions are removed. There is nothing wrong with removing a version while it was served provided it was not the storage version.
Therefore in most use cases where crdify is expected to pickup breaking changes, a removed served version is not a breaking change.

But for us, we need to be able to assert against all possible served versions that are being specified in the CompatibilityRequirement.
This is configurable by end users whether they want AllServed, StorageOnly or some combination of Storage and additional versions.
So this copies the version that checks for storage versions being removed in crdify core and adapts it to our needs.
*/

var (
	_ validations.Validation                                           = (*servedVersionRemoval)(nil)
	_ validations.Comparator[apiextensionsv1.CustomResourceDefinition] = (*servedVersionRemoval)(nil)
)

const servedVersionComparatorName = "servedVersionRemoval"

// factory is a function used to initialize a ServedVersionRemoval validation
// implementation based on the provided configuration.
func servedVersionComparatorFactory(_ map[string]interface{}) (validations.Validation, error) {
	return &servedVersionRemoval{}, nil
}

// servedVersionRemoval is a validations.Validation implementation
// used to check if any versions existing in the set of served versions
// has been removed in the new instance of the CustomResourceDefinition.
type servedVersionRemoval struct {
	// enforcement is the EnforcementPolicy that this validation
	// should use when performing its validation logic
	enforcement config.EnforcementPolicy
}

// Name returns the name of the ServedVersionRemoval validation.
func (svr *servedVersionRemoval) Name() string {
	return servedVersionComparatorName
}

// SetEnforcement sets the EnforcementPolicy for the ServedVersionRemoval validation.
func (svr *servedVersionRemoval) SetEnforcement(enforcement config.EnforcementPolicy) {
	svr.enforcement = enforcement
}

// Compare compares an old and a new CustomResourceDefintion, checking for removal of
// any served versions present in the old CustomResourceDefinition in the new instance
// of the CustomResourceDefinition.
func (svr *servedVersionRemoval) Compare(a, b *apiextensionsv1.CustomResourceDefinition) validations.ComparisonResult {
	newServedVersions := sets.New[string]()

	for _, version := range b.Spec.Versions {
		if version.Served {
			newServedVersions.Insert(version.Name)
		}
	}

	oldServedVersions := sets.New[string]()

	for _, version := range a.Spec.Versions {
		if version.Served {
			oldServedVersions.Insert(version.Name)
		}
	}

	removedVersions := oldServedVersions.Difference(newServedVersions).UnsortedList()
	slices.Sort(removedVersions)

	var err error
	if len(removedVersions) > 0 {
		err = fmt.Errorf("%w : %v", ErrRemovedServedVersions, strings.Join(removedVersions, ", "))
	}

	return validations.HandleErrors(svr.Name(), svr.enforcement, err)
}

// ErrRemovedServedVersions represents an error state where served versions have been removed
// from the CustomResourceDefinition.
var ErrRemovedServedVersions = errors.New("served versions removed")
