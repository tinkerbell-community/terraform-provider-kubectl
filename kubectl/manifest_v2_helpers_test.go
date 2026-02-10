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

func TestBuildUnstructured(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		apiVersion  string
		kind        string
		metadata    types.Dynamic
		spec        types.Dynamic
		wantErr     bool
		checkResult func(t *testing.T, uo *meta_v1_unstruct.Unstructured)
	}{
		{
			name:       "simple ConfigMap",
			apiVersion: "v1",
			kind:       "ConfigMap",
			metadata: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"name":      types.StringType,
					"namespace": types.StringType,
				},
				map[string]attr.Value{
					"name":      types.StringValue("test-config"),
					"namespace": types.StringValue("default"),
				},
			)),
			spec:    types.DynamicNull(),
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
				if uo.GetNamespace() != "default" {
					t.Errorf("Expected namespace=default, got %s", uo.GetNamespace())
				}
			},
		},
		{
			name:       "Deployment with spec",
			apiVersion: "apps/v1",
			kind:       "Deployment",
			metadata: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"name":      types.StringType,
					"namespace": types.StringType,
				},
				map[string]attr.Value{
					"name":      types.StringValue("nginx"),
					"namespace": types.StringValue("default"),
				},
			)),
			spec: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"replicas": types.NumberType,
				},
				map[string]attr.Value{
					"replicas": types.NumberValue(mustNewBigFloat(3)),
				},
			)),
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
			name:       "null metadata should error",
			apiVersion: "v1",
			kind:       "ConfigMap",
			metadata:   types.DynamicNull(),
			spec:       types.DynamicNull(),
			wantErr:    true,
			checkResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uo, diags := buildUnstructured(ctx, tt.apiVersion, tt.kind, tt.metadata, tt.spec)
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
				if model.APIVersion.ValueString() != "v1" {
					t.Errorf("Expected APIVersion=v1, got %s", model.APIVersion.ValueString())
				}
				if model.Kind.ValueString() != "ConfigMap" {
					t.Errorf("Expected Kind=ConfigMap, got %s", model.Kind.ValueString())
				}
				if model.ID.ValueString() != "v1//ConfigMap//test-config//default" {
					t.Errorf("Expected ID=v1//ConfigMap//test-config//default, got %s", model.ID.ValueString())
				}
				if model.Metadata.IsNull() {
					t.Error("Expected Metadata to be populated")
				}
				if !model.Spec.IsNull() {
					t.Error("Expected Spec to be null (ConfigMap doesn't have spec)")
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
				if model.Spec.IsNull() {
					t.Error("Expected Spec to be populated")
				}
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

func TestExtractMetadataField(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		metadata  types.Dynamic
		fieldName string
		want      string
		wantErr   bool
	}{
		{
			name: "extract name",
			metadata: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"name":      types.StringType,
					"namespace": types.StringType,
				},
				map[string]attr.Value{
					"name":      types.StringValue("test"),
					"namespace": types.StringValue("default"),
				},
			)),
			fieldName: "name",
			want:      "test",
			wantErr:   false,
		},
		{
			name: "extract namespace",
			metadata: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"name":      types.StringType,
					"namespace": types.StringType,
				},
				map[string]attr.Value{
					"name":      types.StringValue("test"),
					"namespace": types.StringValue("default"),
				},
			)),
			fieldName: "namespace",
			want:      "default",
			wantErr:   false,
		},
		{
			name: "field not present",
			metadata: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"name": types.StringType,
				},
				map[string]attr.Value{
					"name": types.StringValue("test"),
				},
			)),
			fieldName: "namespace",
			want:      "",
			wantErr:   false,
		},
		{
			name:      "null metadata",
			metadata:  types.DynamicNull(),
			fieldName: "name",
			want:      "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractMetadataField(ctx, tt.metadata, tt.fieldName)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractMetadataField() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("extractMetadataField() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRoundTripUnstructured(t *testing.T) {
	ctx := context.Background()

	// Create a complex Deployment manifest
	metadata := types.DynamicValue(types.ObjectValueMust(
		map[string]attr.Type{
			"name":      types.StringType,
			"namespace": types.StringType,
			"labels": types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"app": types.StringType,
				},
			},
		},
		map[string]attr.Value{
			"name":      types.StringValue("nginx"),
			"namespace": types.StringValue("default"),
			"labels": types.ObjectValueMust(
				map[string]attr.Type{
					"app": types.StringType,
				},
				map[string]attr.Value{
					"app": types.StringValue("nginx"),
				},
			),
		},
	))

	spec := types.DynamicValue(types.ObjectValueMust(
		map[string]attr.Type{
			"replicas": types.NumberType,
		},
		map[string]attr.Value{
			"replicas": types.NumberValue(mustNewBigFloat(3)),
		},
	))

	// Build unstructured
	uo, diags := buildUnstructured(ctx, "apps/v1", "Deployment", metadata, spec)
	if diags.HasError() {
		t.Fatalf("buildUnstructured() failed: %v", diags)
	}

	// Set state from unstructured
	model := &manifestResourceModelV2{}
	diags = setStateFromUnstructured(ctx, uo, model)
	if diags.HasError() {
		t.Fatalf("setStateFromUnstructured() failed: %v", diags)
	}

	// Verify round trip
	if model.APIVersion.ValueString() != "apps/v1" {
		t.Errorf("Expected APIVersion=apps/v1, got %s", model.APIVersion.ValueString())
	}
	if model.Kind.ValueString() != "Deployment" {
		t.Errorf("Expected Kind=Deployment, got %s", model.Kind.ValueString())
	}

	// Extract metadata.name to verify
	name, err := extractMetadataField(ctx, model.Metadata, "name")
	if err != nil {
		t.Fatalf("extractMetadataField() failed: %v", err)
	}
	if name != "nginx" {
		t.Errorf("Expected name=nginx, got %s", name)
	}
}

// Helper function for tests
func mustNewBigFloat(f float64) *big.Float {
	bf := big.NewFloat(f)
	return bf
}
