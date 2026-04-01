/*
Copyright 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package revisiongenerator

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestValidateAdoptExistingAnnotation(t *testing.T) {
	tests := []struct {
		name         string
		annotations  map[string]string
		wantErr      bool
		wantTerminal bool
	}{
		{
			name:        "nil annotations",
			annotations: nil,
			wantErr:     false,
		},
		{
			name:        "no adopt-existing annotation",
			annotations: map[string]string{"other": "value"},
			wantErr:     false,
		},
		{
			name:        "adopt-existing always",
			annotations: map[string]string{AdoptExistingAnnotation: AdoptExistingAlways},
			wantErr:     false,
		},
		{
			name:        "adopt-existing never",
			annotations: map[string]string{AdoptExistingAnnotation: AdoptExistingNever},
			wantErr:     false,
		},
		{
			name:         "adopt-existing invalid value",
			annotations:  map[string]string{AdoptExistingAnnotation: "invalid"},
			wantErr:      true,
			wantTerminal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{}
			obj.SetAnnotations(tt.annotations)

			err := ValidateAdoptExistingAnnotation(obj)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAdoptExistingAnnotation() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				if !errors.Is(err, ErrInvalidAdoptExistingAnnotation) {
					t.Errorf("expected error to wrap ErrInvalidAdoptExistingAnnotation, got %v", err)
				}

				if tt.wantTerminal && !errors.Is(err, reconcile.TerminalError(nil)) {
					t.Errorf("expected terminal error, got %v", err)
				}
			}
		})
	}
}

func TestValidateRenderedRevision(t *testing.T) {
	t.Run("valid revision", func(t *testing.T) {
		rev := &renderedRevision{
			components: []*renderedComponent{
				{
					objects: []unstructured.Unstructured{
						makeUnstructuredWithAnnotations(nil),
						makeUnstructuredWithAnnotations(map[string]string{AdoptExistingAnnotation: AdoptExistingAlways}),
					},
				},
			},
		}

		if err := validateRenderedRevision(rev); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("invalid annotation in objects", func(t *testing.T) {
		rev := &renderedRevision{
			components: []*renderedComponent{
				{
					objects: []unstructured.Unstructured{
						makeUnstructuredWithAnnotations(map[string]string{AdoptExistingAnnotation: "bad"}),
					},
				},
			},
		}

		err := validateRenderedRevision(rev)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !errors.Is(err, ErrInvalidAdoptExistingAnnotation) {
			t.Errorf("expected error to wrap ErrInvalidAdoptExistingAnnotation, got %v", err)
		}
	})

	t.Run("invalid annotation in CRDs", func(t *testing.T) {
		rev := &renderedRevision{
			components: []*renderedComponent{
				{
					crds: []unstructured.Unstructured{
						makeUnstructuredWithAnnotations(map[string]string{AdoptExistingAnnotation: "bad"}),
					},
				},
			},
		}

		err := validateRenderedRevision(rev)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !errors.Is(err, ErrInvalidAdoptExistingAnnotation) {
			t.Errorf("expected error to wrap ErrInvalidAdoptExistingAnnotation, got %v", err)
		}
	})
}

func makeUnstructuredWithAnnotations(annotations map[string]string) unstructured.Unstructured {
	obj := unstructured.Unstructured{}
	obj.SetAnnotations(annotations)

	return obj
}
