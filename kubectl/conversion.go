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

// dynamicToAny converts a types.Dynamic value to any Go value.
// Unlike dynamicToMap, this handles scalars (string, bool, number) as well as maps and lists.
// This is used for top-level Kubernetes fields that may be scalar (e.g., immutable, type).
//
//nolint:unused
func dynamicToAny(_ context.Context, d types.Dynamic) (any, diag.Diagnostics) {
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
			"Failed to convert Dynamic value",
			fmt.Sprintf("Error encoding attribute value: %s", err),
		)}
	}

	return result, nil
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

// anyToDynamic converts any Go value to types.Dynamic.
// This handles scalars, maps, and slices — used for Kubernetes top-level fields
// that may be of any type.
//
//nolint:unused
func anyToDynamic(ctx context.Context, v any) (types.Dynamic, diag.Diagnostics) {
	if v == nil {
		return types.DynamicNull(), nil
	}

	attrVal, diags := decodeAny(ctx, v)
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
