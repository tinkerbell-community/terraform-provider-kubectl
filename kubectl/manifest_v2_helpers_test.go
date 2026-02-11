// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"math/big"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// makeManifestDynamic builds a types.Dynamic value from a map of string->attr.Value.
func makeManifestDynamic(attrTypes map[string]attr.Type, attrValues map[string]attr.Value) types.Dynamic {
	return types.DynamicValue(types.ObjectValueMust(attrTypes, attrValues))
}

func TestBuildUnstructured(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		model       *manifestResourceModel
		wantErr     bool
		checkResult func(t *testing.T, uo *meta_v1_unstruct.Unstructured)
	}{
		{
			name: "simple ConfigMap with data",
			model: &manifestResourceModel{
				Manifest: makeManifestDynamic(
					map[string]attr.Type{
						"apiVersion": types.StringType,
						"kind":       types.StringType,
						"metadata": types.ObjectType{AttrTypes: map[string]attr.Type{
							"name":      types.StringType,
							"namespace": types.StringType,
						}},
						"data": types.ObjectType{AttrTypes: map[string]attr.Type{
							"key1": types.StringType,
						}},
					},
					map[string]attr.Value{
						"apiVersion": types.StringValue("v1"),
						"kind":       types.StringValue("ConfigMap"),
						"metadata": types.ObjectValueMust(
							map[string]attr.Type{
								"name":      types.StringType,
								"namespace": types.StringType,
							},
							map[string]attr.Value{
								"name":      types.StringValue("test-config"),
								"namespace": types.StringValue("default"),
							},
						),
						"data": types.ObjectValueMust(
							map[string]attr.Type{"key1": types.StringType},
							map[string]attr.Value{"key1": types.StringValue("value1")},
						),
					},
				),
			},
			wantErr: false,
			checkResult: func(t *testing.T, uo *meta_v1_unstruct.Unstructured) {
				if uo.GetAPIVersion() != "v1" {
					t.Errorf("Expected apiVersion=v1, got %s", uo.GetAPIVersion())
				}
				if uo.GetKind() != "ConfigMap" {
					t.Errorf("Expected kind=ConfigMap, got %s", uo.GetKind())
				}
				if uo.GetName() != "test-config" {
					t.Errorf("Expected name=test-config, got %s", uo.GetName())
				}
				content := uo.UnstructuredContent()
				if data, ok := content["data"]; ok {
					dataMap := data.(map[string]any)
					if dataMap["key1"] != "value1" {
						t.Errorf("Expected data.key1=value1, got %v", dataMap["key1"])
					}
				} else {
					t.Error("Expected data to be present")
				}
				if _, ok := content["spec"]; ok {
					t.Error("Expected spec to NOT be present for ConfigMap")
				}
			},
		},
		{
			name: "Deployment with spec",
			model: &manifestResourceModel{
				Manifest: makeManifestDynamic(
					map[string]attr.Type{
						"apiVersion": types.StringType,
						"kind":       types.StringType,
						"metadata": types.ObjectType{AttrTypes: map[string]attr.Type{
							"name":      types.StringType,
							"namespace": types.StringType,
						}},
						"spec": types.ObjectType{AttrTypes: map[string]attr.Type{
							"replicas": types.NumberType,
						}},
					},
					map[string]attr.Value{
						"apiVersion": types.StringValue("apps/v1"),
						"kind":       types.StringValue("Deployment"),
						"metadata": types.ObjectValueMust(
							map[string]attr.Type{
								"name":      types.StringType,
								"namespace": types.StringType,
							},
							map[string]attr.Value{
								"name":      types.StringValue("nginx"),
								"namespace": types.StringValue("default"),
							},
						),
						"spec": types.ObjectValueMust(
							map[string]attr.Type{"replicas": types.NumberType},
							map[string]attr.Value{"replicas": types.NumberValue(mustNewBigFloat(3))},
						),
					},
				),
			},
			wantErr: false,
			checkResult: func(t *testing.T, uo *meta_v1_unstruct.Unstructured) {
				content := uo.UnstructuredContent()
				if spec, ok := content["spec"]; ok {
					specMap := spec.(map[string]any)
					if replicas, ok := specMap["replicas"]; !ok {
						t.Error("Expected spec.replicas to be present")
					} else if replicas != float64(3) {
						t.Errorf("Expected replicas=3, got %v", replicas)
					}
				} else {
					t.Error("Expected spec to be present")
				}
			},
		},
		{
			name: "null manifest should error",
			model: &manifestResourceModel{
				Manifest: types.DynamicNull(),
			},
			wantErr:     true,
			checkResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uo, diags := buildUnstructured(ctx, tt.model)
			if (diags.HasError()) != tt.wantErr {
				t.Errorf("buildUnstructured() error = %v, wantErr %v", diags, tt.wantErr)
				return
			}
			if !diags.HasError() && tt.checkResult != nil {
				tt.checkResult(t, uo)
			}
		})
	}
}

func TestSetStateFromUnstructured(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		input   *meta_v1_unstruct.Unstructured
		wantErr bool
		check   func(t *testing.T, model *manifestResourceModelV2)
	}{
		{
			name: "simple ConfigMap",
			input: &meta_v1_unstruct.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]any{
						"name":      "test-config",
						"namespace": "default",
					},
					"data": map[string]any{
						"key1": "value1",
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, model *manifestResourceModelV2) {
				if model.ID.ValueString() != "v1//ConfigMap//test-config//default" {
					t.Errorf(
						"Expected ID=v1//ConfigMap//test-config//default, got %s",
						model.ID.ValueString(),
					)
				}
				if model.Manifest.IsNull() {
					t.Error("Expected Manifest to be populated")
				}
				// Verify manifest contains apiVersion, kind, metadata, data but not status
				manifestMap, d := dynamicToMap(ctx, model.Manifest)
				if d.HasError() {
					t.Fatalf("Failed to convert manifest: %v", d)
				}
				if manifestMap["apiVersion"] != "v1" {
					t.Errorf("Expected manifest.apiVersion=v1, got %v", manifestMap["apiVersion"])
				}
				if manifestMap["kind"] != "ConfigMap" {
					t.Errorf("Expected manifest.kind=ConfigMap, got %v", manifestMap["kind"])
				}
				if _, ok := manifestMap["status"]; ok {
					t.Error("Expected manifest to NOT contain status")
				}
				if model.Object.IsNull() {
					t.Error("Expected Object to be populated")
				}
			},
		},
		{
			name: "Deployment with spec and status",
			input: &meta_v1_unstruct.Unstructured{
				Object: map[string]any{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]any{
						"name":      "nginx",
						"namespace": "default",
					},
					"spec": map[string]any{
						"replicas": float64(3),
					},
					"status": map[string]any{
						"readyReplicas": float64(3),
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, model *manifestResourceModelV2) {
				// Manifest should have spec but not status
				manifestMap, d := dynamicToMap(ctx, model.Manifest)
				if d.HasError() {
					t.Fatalf("Failed to convert manifest: %v", d)
				}
				if _, ok := manifestMap["spec"]; !ok {
					t.Error("Expected manifest to contain spec")
				}
				if _, ok := manifestMap["status"]; ok {
					t.Error("Expected manifest to NOT contain status")
				}
				// Status should be set separately
				if model.Status.IsNull() {
					t.Error("Expected Status to be populated")
				}
			},
		},
		{
			name: "cluster-scoped resource (no namespace)",
			input: &meta_v1_unstruct.Unstructured{
				Object: map[string]any{
					"apiVersion": "v1",
					"kind":       "Namespace",
					"metadata": map[string]any{
						"name": "test-namespace",
					},
				},
			},
			wantErr: false,
			check: func(t *testing.T, model *manifestResourceModelV2) {
				expectedID := "v1//Namespace//test-namespace"
				if model.ID.ValueString() != expectedID {
					t.Errorf("Expected ID=%s, got %s", expectedID, model.ID.ValueString())
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &manifestResourceModelV2{}
			diags := setStateFromUnstructured(ctx, tt.input, model)
			if (diags.HasError()) != tt.wantErr {
				t.Errorf("setStateFromUnstructured() error = %v, wantErr %v", diags, tt.wantErr)
				return
			}
			if !diags.HasError() && tt.check != nil {
				tt.check(t, model)
			}
		})
	}
}

func TestExtractManifestField(t *testing.T) {
	ctx := context.Background()

	manifest := makeManifestDynamic(
		map[string]attr.Type{
			"apiVersion": types.StringType,
			"kind":       types.StringType,
			"metadata": types.ObjectType{AttrTypes: map[string]attr.Type{
				"name":      types.StringType,
				"namespace": types.StringType,
			}},
		},
		map[string]attr.Value{
			"apiVersion": types.StringValue("v1"),
			"kind":       types.StringValue("ConfigMap"),
			"metadata": types.ObjectValueMust(
				map[string]attr.Type{
					"name":      types.StringType,
					"namespace": types.StringType,
				},
				map[string]attr.Value{
					"name":      types.StringValue("test"),
					"namespace": types.StringValue("default"),
				},
			),
		},
	)

	tests := []struct {
		name      string
		manifest  types.Dynamic
		fieldName string
		want      any
		wantErr   bool
	}{
		{
			name:      "extract apiVersion",
			manifest:  manifest,
			fieldName: "apiVersion",
			want:      "v1",
			wantErr:   false,
		},
		{
			name:      "extract kind",
			manifest:  manifest,
			fieldName: "kind",
			want:      "ConfigMap",
			wantErr:   false,
		},
		{
			name:      "field not present",
			manifest:  manifest,
			fieldName: "spec",
			want:      nil,
			wantErr:   false,
		},
		{
			name:      "null manifest",
			manifest:  types.DynamicNull(),
			fieldName: "apiVersion",
			want:      nil,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractManifestField(ctx, tt.manifest, tt.fieldName)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractManifestField() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("extractManifestField() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractManifestMetadataField(t *testing.T) {
	ctx := context.Background()

	manifest := makeManifestDynamic(
		map[string]attr.Type{
			"apiVersion": types.StringType,
			"kind":       types.StringType,
			"metadata": types.ObjectType{AttrTypes: map[string]attr.Type{
				"name":      types.StringType,
				"namespace": types.StringType,
			}},
		},
		map[string]attr.Value{
			"apiVersion": types.StringValue("v1"),
			"kind":       types.StringValue("ConfigMap"),
			"metadata": types.ObjectValueMust(
				map[string]attr.Type{
					"name":      types.StringType,
					"namespace": types.StringType,
				},
				map[string]attr.Value{
					"name":      types.StringValue("test"),
					"namespace": types.StringValue("default"),
				},
			),
		},
	)

	tests := []struct {
		name      string
		manifest  types.Dynamic
		fieldName string
		want      string
		wantErr   bool
	}{
		{
			name:      "extract name",
			manifest:  manifest,
			fieldName: "name",
			want:      "test",
			wantErr:   false,
		},
		{
			name:      "extract namespace",
			manifest:  manifest,
			fieldName: "namespace",
			want:      "default",
			wantErr:   false,
		},
		{
			name:      "field not present",
			manifest:  manifest,
			fieldName: "labels",
			want:      "",
			wantErr:   false,
		},
		{
			name:      "null manifest",
			manifest:  types.DynamicNull(),
			fieldName: "name",
			want:      "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractManifestMetadataField(ctx, tt.manifest, tt.fieldName)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractManifestMetadataField() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("extractManifestMetadataField() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRoundTripUnstructured(t *testing.T) {
	ctx := context.Background()

	// Create a complex Deployment manifest
	model := &manifestResourceModel{
		Manifest: makeManifestDynamic(
			map[string]attr.Type{
				"apiVersion": types.StringType,
				"kind":       types.StringType,
				"metadata": types.ObjectType{AttrTypes: map[string]attr.Type{
					"name":      types.StringType,
					"namespace": types.StringType,
					"labels": types.ObjectType{AttrTypes: map[string]attr.Type{
						"app": types.StringType,
					}},
				}},
				"spec": types.ObjectType{AttrTypes: map[string]attr.Type{
					"replicas": types.NumberType,
				}},
			},
			map[string]attr.Value{
				"apiVersion": types.StringValue("apps/v1"),
				"kind":       types.StringValue("Deployment"),
				"metadata": types.ObjectValueMust(
					map[string]attr.Type{
						"name":      types.StringType,
						"namespace": types.StringType,
						"labels": types.ObjectType{AttrTypes: map[string]attr.Type{
							"app": types.StringType,
						}},
					},
					map[string]attr.Value{
						"name":      types.StringValue("nginx"),
						"namespace": types.StringValue("default"),
						"labels": types.ObjectValueMust(
							map[string]attr.Type{"app": types.StringType},
							map[string]attr.Value{"app": types.StringValue("nginx")},
						),
					},
				),
				"spec": types.ObjectValueMust(
					map[string]attr.Type{"replicas": types.NumberType},
					map[string]attr.Value{"replicas": types.NumberValue(mustNewBigFloat(3))},
				),
			},
		),
	}

	// Build unstructured
	uo, diags := buildUnstructured(ctx, model)
	if diags.HasError() {
		t.Fatalf("buildUnstructured() failed: %v", diags)
	}

	// Verify unstructured content
	if uo.GetAPIVersion() != "apps/v1" {
		t.Errorf("Expected apiVersion=apps/v1, got %s", uo.GetAPIVersion())
	}
	if uo.GetKind() != "Deployment" {
		t.Errorf("Expected kind=Deployment, got %s", uo.GetKind())
	}
	if uo.GetName() != "nginx" {
		t.Errorf("Expected name=nginx, got %s", uo.GetName())
	}

	// Set state from unstructured
	resultModel := &manifestResourceModelV2{}
	diags = setStateFromUnstructured(ctx, uo, resultModel)
	if diags.HasError() {
		t.Fatalf("setStateFromUnstructured() failed: %v", diags)
	}

	// Verify round trip via manifest
	name, err := extractManifestMetadataField(ctx, resultModel.Manifest, "name")
	if err != nil {
		t.Fatalf("extractManifestMetadataField() failed: %v", err)
	}
	if name != "nginx" {
		t.Errorf("Expected name=nginx, got %s", name)
	}

	apiVersion, err := extractManifestField(ctx, resultModel.Manifest, "apiVersion")
	if err != nil {
		t.Fatalf("extractManifestField() failed: %v", err)
	}
	if apiVersion != "apps/v1" {
		t.Errorf("Expected apiVersion=apps/v1, got %v", apiVersion)
	}
}

// Helper function for tests.
func mustNewBigFloat(f float64) *big.Float {
	bf := big.NewFloat(f)
	return bf
}
