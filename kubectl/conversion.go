// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"fmt"
	"math/big"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// dynamicToMap converts a types.Dynamic value to map[string]any
// This is used to convert Dynamic attributes to Kubernetes unstructured objects.
func dynamicToMap(_ context.Context, d types.Dynamic) (map[string]any, diag.Diagnostics) {
	if d.IsNull() || d.IsUnknown() {
		return nil, nil
	}

	underlying := d.UnderlyingValue()
	if underlying == nil {
		return nil, nil
	}
	result, err := encodeAttrValue(underlying)
	if err != nil {
		return nil, diag.Diagnostics{diag.NewErrorDiagnostic(
			"Failed to convert Dynamic to map",
			fmt.Sprintf("Error encoding attribute value: %s", err),
		)}
	}

	m, ok := result.(map[string]any)
	if !ok {
		return nil, diag.Diagnostics{diag.NewErrorDiagnostic(
			"Invalid Dynamic value",
			fmt.Sprintf("Expected map[string]any, got %T", result),
		)}
	}

	return m, nil
}

// mapToDynamic converts map[string]any to types.Dynamic
// This is used to convert Kubernetes unstructured objects to Dynamic attributes.
func mapToDynamic(ctx context.Context, m map[string]any) (types.Dynamic, diag.Diagnostics) {
	if m == nil {
		return types.DynamicNull(), nil
	}

	attrVal, diags := decodeAny(ctx, m)
	if diags.HasError() {
		return types.DynamicNull(), diags
	}

	return types.DynamicValue(attrVal), nil
}

// encodeAttrValue converts attr.Value to any
// Based on kubectl/functions/encode.go:encodeValue.
func encodeAttrValue(v attr.Value) (any, error) {
	if v == nil || v.IsNull() || v.IsUnknown() {
		return nil, nil
	}

	switch vv := v.(type) {
	case basetypes.StringValue:
		return vv.ValueString(), nil
	case basetypes.NumberValue:
		bf := vv.ValueBigFloat()
		if bf == nil {
			return nil, nil
		}
		f, _ := bf.Float64()
		return f, nil
	case basetypes.BoolValue:
		return vv.ValueBool(), nil
	case basetypes.ObjectValue:
		return encodeObject(vv)
	case basetypes.TupleValue:
		return encodeTuple(vv)
	case basetypes.MapValue:
		return encodeMap(vv)
	case basetypes.ListValue:
		return encodeList(vv)
	case basetypes.SetValue:
		return encodeSet(vv)
	case basetypes.DynamicValue:
		// For dynamic values, encode the underlying value
		return encodeAttrValue(vv.UnderlyingValue())
	default:
		return nil, fmt.Errorf("tried to encode unsupported type: %T: %v", v, vv)
	}
}

func encodeSet(sv basetypes.SetValue) ([]any, error) {
	elems := sv.Elements()
	size := len(elems)
	l := make([]any, size)
	for i := 0; i < size; i++ {
		var err error
		l[i], err = encodeAttrValue(elems[i])
		if err != nil {
			return nil, err
		}
	}
	return l, nil
}

func encodeList(lv basetypes.ListValue) ([]any, error) {
	elems := lv.Elements()
	size := len(elems)
	l := make([]any, size)
	for i := 0; i < size; i++ {
		var err error
		l[i], err = encodeAttrValue(elems[i])
		if err != nil {
			return nil, err
		}
	}
	return l, nil
}

func encodeTuple(tv basetypes.TupleValue) ([]any, error) {
	elems := tv.Elements()
	size := len(elems)
	l := make([]any, size)
	for i := 0; i < size; i++ {
		var err error
		l[i], err = encodeAttrValue(elems[i])
		if err != nil {
			return nil, err
		}
	}
	return l, nil
}

func encodeObject(ov basetypes.ObjectValue) (map[string]any, error) {
	attrs := ov.Attributes()
	m := make(map[string]any, len(attrs))
	for k, v := range attrs {
		var err error
		m[k], err = encodeAttrValue(v)
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

func encodeMap(mv basetypes.MapValue) (map[string]any, error) {
	elems := mv.Elements()
	m := make(map[string]any, len(elems))
	for k, v := range elems {
		var err error
		m[k], err = encodeAttrValue(v)
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

// decodeAny converts any to attr.Value
// Based on kubectl/functions/decode.go:decodeScalar.
func decodeAny(ctx context.Context, m any) (value attr.Value, diags diag.Diagnostics) {
	switch v := m.(type) {
	case nil:
		value = types.DynamicNull()
	case float64:
		value = types.NumberValue(big.NewFloat(float64(v)))
	case int:
		value = types.NumberValue(big.NewFloat(float64(v)))
	case int64:
		value = types.NumberValue(big.NewFloat(float64(v)))
	case bool:
		value = types.BoolValue(v)
	case string:
		value = types.StringValue(v)
	case []any:
		return decodeSequence(ctx, v)
	case map[string]any:
		return decodeMapping(ctx, v)
	default:
		diags.Append(diag.NewErrorDiagnostic(
			"Failed to decode",
			fmt.Sprintf("unexpected type: %T for value %#v", v, v),
		))
	}
	return
}

func decodeMapping(ctx context.Context, m map[string]any) (attr.Value, diag.Diagnostics) {
	vm := make(map[string]attr.Value, len(m))
	tm := make(map[string]attr.Type, len(m))

	for k, v := range m {
		vv, diags := decodeAny(ctx, v)
		if diags.HasError() {
			return nil, diags
		}
		if vv == nil {
			vv = types.DynamicNull()
		}
		vm[k] = vv
		tm[k] = vv.Type(ctx)
	}

	return types.ObjectValue(tm, vm)
}

func decodeSequence(ctx context.Context, s []any) (attr.Value, diag.Diagnostics) {
	vl := make([]attr.Value, len(s))
	tl := make([]attr.Type, len(s))

	for i, v := range s {
		vv, diags := decodeAny(ctx, v)
		if diags.HasError() {
			return nil, diags
		}
		if vv == nil {
			vv = types.DynamicNull()
		}
		vl[i] = vv
		tl[i] = vv.Type(ctx)
	}

	return types.TupleValue(tl, vl)
}

// mapToDynamicPreservingTypes converts map[string]any to types.Dynamic while
// preserving the container types (Map vs Object, List vs Tuple) from a
// reference Dynamic value. HCL can produce either MapVal or ObjectVal for
// map-like structures depending on context (literal vs variable), and
// Terraform validates that planned values match config types. Without
// preserving the original types, the OpenAPI round-trip in modifyPlanWithOpenAPI
// always produces ObjectVal/TupleVal, causing "Provider produced invalid plan"
// errors when the config uses MapVal/ListVal.
func mapToDynamicPreservingTypes(
	ctx context.Context,
	m map[string]any,
	hint types.Dynamic,
) (types.Dynamic, diag.Diagnostics) {
	if m == nil {
		return types.DynamicNull(), nil
	}

	var hintValue attr.Value
	if !hint.IsNull() && !hint.IsUnknown() {
		hintValue = hint.UnderlyingValue()
	}

	attrVal, diags := decodeAnyPreservingType(ctx, m, hintValue)
	if diags.HasError() {
		return types.DynamicNull(), diags
	}

	return types.DynamicValue(attrVal), nil
}

// decodeAnyPreservingType converts a Go value to attr.Value, using hint to
// choose Map vs Object and List vs Tuple container types.
func decodeAnyPreservingType(
	ctx context.Context,
	val any,
	hint attr.Value,
) (attr.Value, diag.Diagnostics) {
	switch v := val.(type) {
	case nil:
		return types.DynamicNull(), nil
	case map[string]any:
		return decodeMappingPreservingType(ctx, v, hint)
	case []any:
		return decodeSequencePreservingType(ctx, v, hint)
	default:
		return decodeAny(ctx, val)
	}
}

func decodeMappingPreservingType(
	ctx context.Context,
	m map[string]any,
	hint attr.Value,
) (attr.Value, diag.Diagnostics) {
	// Build child hints from the reference value.
	childHints := make(map[string]attr.Value)
	_, preferMap := hint.(basetypes.MapValue)

	switch h := hint.(type) {
	case basetypes.MapValue:
		for k, v := range h.Elements() {
			childHints[k] = v
		}
	case basetypes.ObjectValue:
		for k, v := range h.Attributes() {
			childHints[k] = v
		}
	}

	vm := make(map[string]attr.Value, len(m))
	tm := make(map[string]attr.Type, len(m))

	for k, v := range m {
		vv, diags := decodeAnyPreservingType(ctx, v, childHints[k])
		if diags.HasError() {
			return nil, diags
		}
		if vv == nil {
			vv = types.DynamicNull()
		}
		vm[k] = vv
		tm[k] = vv.Type(ctx)
	}

	if preferMap && len(vm) > 0 {
		// MapValue requires homogeneous element types.
		var commonType attr.Type
		homogeneous := true
		for _, t := range tm {
			if commonType == nil {
				commonType = t
			} else if !t.Equal(commonType) {
				homogeneous = false
				break
			}
		}
		if homogeneous {
			mapVal, diags := types.MapValue(commonType, vm)
			if !diags.HasError() {
				return mapVal, nil
			}
		}
	}

	return types.ObjectValue(tm, vm)
}

func decodeSequencePreservingType(
	ctx context.Context,
	s []any,
	hint attr.Value,
) (attr.Value, diag.Diagnostics) {
	// Build child hints from the reference value.
	var childHints []attr.Value
	_, preferList := hint.(basetypes.ListValue)

	switch h := hint.(type) {
	case basetypes.ListValue:
		childHints = h.Elements()
	case basetypes.TupleValue:
		childHints = h.Elements()
	}

	vl := make([]attr.Value, len(s))
	tl := make([]attr.Type, len(s))

	for i, v := range s {
		var childHint attr.Value
		if i < len(childHints) {
			childHint = childHints[i]
		}
		vv, diags := decodeAnyPreservingType(ctx, v, childHint)
		if diags.HasError() {
			return nil, diags
		}
		if vv == nil {
			vv = types.DynamicNull()
		}
		vl[i] = vv
		tl[i] = vv.Type(ctx)
	}

	if preferList && len(vl) > 0 {
		commonType := tl[0]
		homogeneous := true
		for i := 1; i < len(tl); i++ {
			if !tl[i].Equal(commonType) {
				homogeneous = false
				break
			}
		}
		if homogeneous {
			listVal, diags := types.ListValue(commonType, vl)
			if !diags.HasError() {
				return listVal, nil
			}
		}
	}

	return types.TupleValue(tl, vl)
}
