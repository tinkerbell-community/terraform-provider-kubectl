// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"math/big"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestDynamicToMap(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		input    types.Dynamic
		expected map[string]any
		wantErr  bool
	}{
		{
			name:     "null dynamic",
			input:    types.DynamicNull(),
			expected: nil,
			wantErr:  false,
		},
		{
			name:     "unknown dynamic",
			input:    types.DynamicUnknown(),
			expected: nil,
			wantErr:  false,
		},
		{
			name: "simple object",
			input: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"name":      types.StringType,
					"namespace": types.StringType,
				},
				map[string]attr.Value{
					"name":      types.StringValue("test"),
					"namespace": types.StringValue("default"),
				},
			)),
			expected: map[string]any{
				"name":      "test",
				"namespace": "default",
			},
			wantErr: false,
		},
		{
			name: "nested object",
			input: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"metadata": types.ObjectType{
						AttrTypes: map[string]attr.Type{
							"name":      types.StringType,
							"namespace": types.StringType,
						},
					},
				},
				map[string]attr.Value{
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
			)),
			expected: map[string]any{
				"metadata": map[string]any{
					"name":      "test",
					"namespace": "default",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := dynamicToMap(ctx, tt.input)
			if (diags.HasError()) != tt.wantErr {
				t.Errorf("dynamicToMap() error = %v, wantErr %v", diags, tt.wantErr)
				return
			}
			if !cmp.Equal(got, tt.expected) {
				t.Errorf("dynamicToMap() diff:\n%s", cmp.Diff(tt.expected, got))
			}
		})
	}
}

func TestMapToDynamic(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		input   map[string]any
		wantErr bool
		check   func(t *testing.T, result types.Dynamic)
	}{
		{
			name:    "nil map",
			input:   nil,
			wantErr: false,
			check: func(t *testing.T, result types.Dynamic) {
				if !result.IsNull() {
					t.Errorf("Expected null dynamic, got %v", result)
				}
			},
		},
		{
			name: "simple map",
			input: map[string]any{
				"name":      "test",
				"namespace": "default",
			},
			wantErr: false,
			check: func(t *testing.T, result types.Dynamic) {
				if result.IsNull() || result.IsUnknown() {
					t.Errorf("Expected non-null/unknown dynamic")
					return
				}
				underlying := result.UnderlyingValue()
				obj, ok := underlying.(types.Object)
				if !ok {
					t.Errorf("Expected Object type, got %T", underlying)
					return
				}
				attrs := obj.Attributes()
				if len(attrs) != 2 {
					t.Errorf("Expected 2 attributes, got %d", len(attrs))
				}
			},
		},
		{
			name: "nested map",
			input: map[string]any{
				"metadata": map[string]any{
					"name":      "test",
					"namespace": "default",
				},
			},
			wantErr: false,
			check: func(t *testing.T, result types.Dynamic) {
				if result.IsNull() || result.IsUnknown() {
					t.Errorf("Expected non-null/unknown dynamic")
					return
				}
			},
		},
		{
			name: "map with numbers",
			input: map[string]any{
				"replicas": float64(3),
				"enabled":  true,
			},
			wantErr: false,
			check: func(t *testing.T, result types.Dynamic) {
				if result.IsNull() || result.IsUnknown() {
					t.Errorf("Expected non-null/unknown dynamic")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := mapToDynamic(ctx, tt.input)
			if (diags.HasError()) != tt.wantErr {
				t.Errorf("mapToDynamic() error = %v, wantErr %v", diags, tt.wantErr)
				return
			}
			if !diags.HasError() && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		data map[string]any
	}{
		{
			name: "simple ConfigMap metadata",
			data: map[string]any{
				"name":      "test-config",
				"namespace": "default",
			},
		},
		{
			name: "complex nested structure",
			data: map[string]any{
				"metadata": map[string]any{
					"name":      "test",
					"namespace": "default",
					"labels": map[string]any{
						"app": "test",
					},
				},
				"spec": map[string]any{
					"replicas": float64(3),
					"selector": map[string]any{
						"matchLabels": map[string]any{
							"app": "test",
						},
					},
				},
			},
		},
		{
			name: "with arrays",
			data: map[string]any{
				"items": []any{
					"item1",
					"item2",
					float64(3),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert map -> Dynamic
			dynamic, diags := mapToDynamic(ctx, tt.data)
			if diags.HasError() {
				t.Fatalf("mapToDynamic() error = %v", diags)
			}

			// Convert Dynamic -> map
			result, diags := dynamicToMap(ctx, dynamic)
			if diags.HasError() {
				t.Fatalf("dynamicToMap() error = %v", diags)
			}

			// Compare
			if !cmp.Equal(result, tt.data) {
				t.Errorf("Round trip failed, diff:\n%s", cmp.Diff(tt.data, result))
			}
		})
	}
}

func TestEncodeAttrValue(t *testing.T) {
	tests := []struct {
		name     string
		input    attr.Value
		expected any
		wantErr  bool
	}{
		{
			name:     "null value",
			input:    types.StringNull(),
			expected: nil,
			wantErr:  false,
		},
		{
			name:     "string value",
			input:    types.StringValue("test"),
			expected: "test",
			wantErr:  false,
		},
		{
			name:     "number value",
			input:    types.NumberValue(big.NewFloat(42.5)),
			expected: 42.5,
			wantErr:  false,
		},
		{
			name:     "bool value",
			input:    types.BoolValue(true),
			expected: true,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeAttrValue(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("encodeAttrValue() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !cmp.Equal(got, tt.expected) {
				t.Errorf("encodeAttrValue() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDecodeAny(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		input   any
		wantErr bool
		check   func(t *testing.T, result attr.Value)
	}{
		{
			name:    "nil value",
			input:   nil,
			wantErr: false,
			check: func(t *testing.T, result attr.Value) {
				if !result.IsNull() {
					t.Errorf("Expected null value, got %v", result)
				}
			},
		},
		{
			name:    "string value",
			input:   "test",
			wantErr: false,
			check: func(t *testing.T, result attr.Value) {
				str, ok := result.(types.String)
				if !ok {
					t.Errorf("Expected String type, got %T", result)
					return
				}
				if str.ValueString() != "test" {
					t.Errorf("Expected 'test', got %v", str.ValueString())
				}
			},
		},
		{
			name:    "number value",
			input:   float64(42),
			wantErr: false,
			check: func(t *testing.T, result attr.Value) {
				_, ok := result.(types.Number)
				if !ok {
					t.Errorf("Expected Number type, got %T", result)
				}
			},
		},
		{
			name:    "bool value",
			input:   true,
			wantErr: false,
			check: func(t *testing.T, result attr.Value) {
				b, ok := result.(types.Bool)
				if !ok {
					t.Errorf("Expected Bool type, got %T", result)
					return
				}
				if !b.ValueBool() {
					t.Errorf("Expected true, got false")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := decodeAny(ctx, tt.input)
			if (diags.HasError()) != tt.wantErr {
				t.Errorf("decodeAny() error = %v, wantErr %v", diags, tt.wantErr)
				return
			}
			if !diags.HasError() && tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestMapToDynamicPreservingTypes(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name  string
		input map[string]any
		hint  types.Dynamic
		check func(t *testing.T, result types.Dynamic)
	}{
		{
			name: "preserves MapVal for homogeneous string values",
			input: map[string]any{
				"distro":    "talos",
				"image_tag": "v1.12.5",
				"version":   "v1.12.5",
			},
			hint: types.DynamicValue(types.MapValueMust(
				types.StringType,
				map[string]attr.Value{
					"distro":    types.StringValue("talos"),
					"image_tag": types.StringValue("v1.12.4"),
					"version":   types.StringValue("v1.12.4"),
				},
			)),
			check: func(t *testing.T, result types.Dynamic) {
				underlying := result.UnderlyingValue()
				if _, ok := underlying.(types.Map); !ok {
					t.Errorf("Expected Map type, got %T", underlying)
				}
			},
		},
		{
			name: "preserves ObjectVal when hint is Object",
			input: map[string]any{
				"name":      "test",
				"namespace": "default",
			},
			hint: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"name":      types.StringType,
					"namespace": types.StringType,
				},
				map[string]attr.Value{
					"name":      types.StringValue("test"),
					"namespace": types.StringValue("default"),
				},
			)),
			check: func(t *testing.T, result types.Dynamic) {
				underlying := result.UnderlyingValue()
				if _, ok := underlying.(types.Object); !ok {
					t.Errorf("Expected Object type, got %T", underlying)
				}
			},
		},
		{
			name: "preserves ListVal for homogeneous elements",
			input: map[string]any{
				"items": []any{
					map[string]any{"device": "/dev/sda"},
					map[string]any{"device": "/dev/sdb"},
				},
			},
			hint: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"items": types.ListType{ElemType: types.ObjectType{
						AttrTypes: map[string]attr.Type{"device": types.StringType},
					}},
				},
				map[string]attr.Value{
					"items": types.ListValueMust(
						types.ObjectType{
							AttrTypes: map[string]attr.Type{"device": types.StringType},
						},
						[]attr.Value{
							types.ObjectValueMust(
								map[string]attr.Type{"device": types.StringType},
								map[string]attr.Value{"device": types.StringValue("/dev/sda")},
							),
						},
					),
				},
			)),
			check: func(t *testing.T, result types.Dynamic) {
				underlying := result.UnderlyingValue()
				obj, ok := underlying.(types.Object)
				if !ok {
					t.Fatalf("Expected Object type, got %T", underlying)
				}
				items := obj.Attributes()["items"]
				if _, ok := items.(types.List); !ok {
					t.Errorf("Expected List type for items, got %T", items)
				}
			},
		},
		{
			name: "preserves nested MapVal inside ObjectVal",
			input: map[string]any{
				"metadata": map[string]any{
					"name": "test",
				},
				"spec": map[string]any{
					"operating_system": map[string]any{
						"distro":  "talos",
						"version": "v1.12.5",
					},
				},
			},
			hint: types.DynamicValue(types.ObjectValueMust(
				map[string]attr.Type{
					"metadata": types.ObjectType{
						AttrTypes: map[string]attr.Type{"name": types.StringType},
					},
					"spec": types.ObjectType{
						AttrTypes: map[string]attr.Type{
							"operating_system": types.MapType{ElemType: types.StringType},
						},
					},
				},
				map[string]attr.Value{
					"metadata": types.ObjectValueMust(
						map[string]attr.Type{"name": types.StringType},
						map[string]attr.Value{"name": types.StringValue("test")},
					),
					"spec": types.ObjectValueMust(
						map[string]attr.Type{
							"operating_system": types.MapType{ElemType: types.StringType},
						},
						map[string]attr.Value{
							"operating_system": types.MapValueMust(
								types.StringType,
								map[string]attr.Value{
									"distro":  types.StringValue("talos"),
									"version": types.StringValue("v1.12.4"),
								},
							),
						},
					),
				},
			)),
			check: func(t *testing.T, result types.Dynamic) {
				obj, ok := result.UnderlyingValue().(types.Object)
				if !ok {
					t.Fatalf("Expected Object type, got %T", result.UnderlyingValue())
				}
				spec, ok := obj.Attributes()["spec"].(types.Object)
				if !ok {
					t.Fatalf("Expected Object type for spec, got %T", obj.Attributes()["spec"])
				}
				os := spec.Attributes()["operating_system"]
				if _, ok := os.(types.Map); !ok {
					t.Errorf("Expected Map type for operating_system, got %T", os)
				}
			},
		},
		{
			name: "falls back to ObjectVal when no hint",
			input: map[string]any{
				"name":      "test",
				"namespace": "default",
			},
			hint: types.DynamicNull(),
			check: func(t *testing.T, result types.Dynamic) {
				underlying := result.UnderlyingValue()
				if _, ok := underlying.(types.Object); !ok {
					t.Errorf("Expected Object type (default), got %T", underlying)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := mapToDynamicPreservingTypes(ctx, tt.input, tt.hint)
			if diags.HasError() {
				t.Fatalf("mapToDynamicPreservingTypes() error = %v", diags)
			}
			tt.check(t, got)
		})
	}
}

// TestDynamicMapComparisonRobustness demonstrates that comparing Dynamic values
// at the map[string]any level (via dynamicToMap) is more robust than
// types.Dynamic.Equal, which is sensitive to container type differences
// (Object vs Map, Tuple vs List).
func TestDynamicMapComparisonRobustness(t *testing.T) {
	ctx := context.Background()

	// Build a Dynamic value using ObjectValue (as mapToDynamic would produce)
	objDyn := types.DynamicValue(types.ObjectValueMust(
		map[string]attr.Type{
			"name": types.StringType,
			"port": types.NumberType,
		},
		map[string]attr.Value{
			"name": types.StringValue("test"),
			"port": types.NumberValue(big.NewFloat(623)),
		},
	))

	// Build a semantically equivalent Dynamic value using MapValue
	mapDyn := types.DynamicValue(types.MapValueMust(
		types.DynamicType,
		map[string]attr.Value{
			"name": types.DynamicValue(types.StringValue("test")),
			"port": types.DynamicValue(types.NumberValue(big.NewFloat(623))),
		},
	))

	// types.Dynamic.Equal is type-sensitive — Object != Map
	if objDyn.Equal(mapDyn) {
		t.Log("Dynamic.Equal returned true for Object vs Map — no type sensitivity issue")
	} else {
		t.Log(
			"Dynamic.Equal returned false for Object vs Map — map comparison would be more robust",
		)
	}

	// Map-level comparison normalizes away container type differences
	objMap, d := dynamicToMap(ctx, objDyn)
	if d.HasError() {
		t.Fatalf("dynamicToMap(objDyn) error: %v", d)
	}
	mapMap, d := dynamicToMap(ctx, mapDyn)
	if d.HasError() {
		t.Fatalf("dynamicToMap(mapDyn) error: %v", d)
	}

	if diff := cmp.Diff(objMap, mapMap); diff != "" {
		t.Errorf("dynamicToMap results differ (-obj +map):\n%s", diff)
	}
}
