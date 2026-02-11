// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"sigs.k8s.io/yaml"
)

var functionDocumentSeparator = regexp.MustCompile(`(:?^|\s*\n)---\s*`)

// decodeManifestYAML decodes a YAML string containing one or more Kubernetes
// manifests into a Tuple of objects. When validate is true, each document is
// checked for the required apiVersion, kind and metadata fields.
func decodeManifestYAML(ctx context.Context, manifest string, validate bool) (v types.Tuple, diags diag.Diagnostics) {
	docs := functionDocumentSeparator.Split(manifest, -1)
	dtypes := []attr.Type{}
	dvalues := []attr.Value{}
	diags = diag.Diagnostics{}

	for _, d := range docs {
		var data map[string]any
		err := yaml.Unmarshal([]byte(d), &data)
		if err != nil {
			diags.Append(diag.NewErrorDiagnostic("Invalid YAML document", err.Error()))
			return
		}

		if len(data) == 0 {
			diags.Append(
				diag.NewWarningDiagnostic(
					"Empty document",
					"encountered a YAML document with no values",
				),
			)
			continue
		}

		if validate {
			if err := validateKubernetesManifestFields(data); err != nil {
				diags.Append(diag.NewErrorDiagnostic("Invalid Kubernetes manifest", err.Error()))
				return
			}
		}

		obj, d := decodeYAMLScalar(ctx, data)
		diags.Append(d...)
		if diags.HasError() {
			return
		}
		dtypes = append(dtypes, obj.Type(ctx))
		dvalues = append(dvalues, obj)
	}

	return types.TupleValue(dtypes, dvalues)
}

func decodeYAMLMapping(ctx context.Context, m map[string]any) (attr.Value, diag.Diagnostics) {
	vm := make(map[string]attr.Value, len(m))
	tm := make(map[string]attr.Type, len(m))

	for k, v := range m {
		vv, diags := decodeYAMLScalar(ctx, v)
		if diags.HasError() {
			return nil, diags
		}
		vm[k] = vv
		tm[k] = vv.Type(ctx)
	}

	return types.ObjectValue(tm, vm)
}

func decodeYAMLSequence(ctx context.Context, s []any) (attr.Value, diag.Diagnostics) {
	vl := make([]attr.Value, len(s))
	tl := make([]attr.Type, len(s))

	for i, v := range s {
		vv, diags := decodeYAMLScalar(ctx, v)
		if diags.HasError() {
			return nil, diags
		}
		vl[i] = vv
		tl[i] = vv.Type(ctx)
	}

	return types.TupleValue(tl, vl)
}

func decodeYAMLScalar(ctx context.Context, m any) (value attr.Value, diags diag.Diagnostics) {
	switch v := m.(type) {
	case nil:
		value = types.DynamicNull()
	case float64:
		value = types.NumberValue(big.NewFloat(v))
	case bool:
		value = types.BoolValue(v)
	case string:
		value = types.StringValue(v)
	case []any:
		return decodeYAMLSequence(ctx, v)
	case map[string]any:
		return decodeYAMLMapping(ctx, v)
	default:
		diags.Append(
			diag.NewErrorDiagnostic(
				"failed to decode",
				fmt.Sprintf("unexpected type: %T for value %#v", v, v),
			),
		)
	}
	return
}

func validateKubernetesManifestFields(m map[string]any) error {
	for _, k := range []string{"apiVersion", "kind", "metadata"} {
		if _, ok := m[k]; !ok {
			return fmt.Errorf("missing field %q", k)
		}
	}
	return nil
}

// --- Encoding helpers ---

func encodeYAMLValue(v attr.Value) (any, error) {
	if v.IsNull() {
		return nil, nil
	}

	switch vv := v.(type) {
	case basetypes.StringValue:
		return vv.ValueString(), nil
	case basetypes.NumberValue:
		f, _ := vv.ValueBigFloat().Float64()
		return f, nil
	case basetypes.BoolValue:
		return vv.ValueBool(), nil
	case basetypes.ObjectValue:
		return encodeYAMLObject(vv)
	case basetypes.TupleValue:
		return encodeYAMLTuple(vv)
	case basetypes.MapValue:
		return encodeYAMLMapValue(vv)
	case basetypes.ListValue:
		return encodeYAMLList(vv)
	case basetypes.SetValue:
		return encodeYAMLSet(vv)
	default:
		return nil, fmt.Errorf("tried to encode unsupported type: %T: %v", v, vv)
	}
}

func encodeYAMLSet(sv basetypes.SetValue) ([]any, error) {
	elems := sv.Elements()
	l := make([]any, len(elems))
	for i := range len(elems) {
		var err error
		l[i], err = encodeYAMLValue(elems[i])
		if err != nil {
			return nil, err
		}
	}
	return l, nil
}

func encodeYAMLList(lv basetypes.ListValue) ([]any, error) {
	elems := lv.Elements()
	l := make([]any, len(elems))
	for i := range len(elems) {
		var err error
		l[i], err = encodeYAMLValue(elems[i])
		if err != nil {
			return nil, err
		}
	}
	return l, nil
}

func encodeYAMLTuple(tv basetypes.TupleValue) ([]any, error) {
	elems := tv.Elements()
	l := make([]any, len(elems))
	for i := range len(elems) {
		var err error
		l[i], err = encodeYAMLValue(elems[i])
		if err != nil {
			return nil, err
		}
	}
	return l, nil
}

func encodeYAMLObject(ov basetypes.ObjectValue) (map[string]any, error) {
	attrs := ov.Attributes()
	m := make(map[string]any, len(attrs))
	for k, v := range attrs {
		var err error
		m[k], err = encodeYAMLValue(v)
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

func encodeYAMLMapValue(mv basetypes.MapValue) (map[string]any, error) {
	elems := mv.Elements()
	m := make(map[string]any, len(elems))
	for k, v := range elems {
		var err error
		m[k], err = encodeYAMLValue(v)
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

func marshalManifest(m map[string]any, validate bool) (encoded string, diags diag.Diagnostics) {
	if validate {
		if err := validateKubernetesManifestFields(m); err != nil {
			diags.Append(diag.NewErrorDiagnostic("Invalid Kubernetes manifest", err.Error()))
			return
		}
	}
	b, err := yaml.Marshal(m)
	if err != nil {
		diags.Append(diag.NewErrorDiagnostic("Error marshalling yaml", err.Error()))
		return
	}
	return string(b), nil
}

func encodeManifestYAML(v attr.Value, validate bool) (encoded string, diags diag.Diagnostics) {
	val, err := encodeYAMLValue(v)
	if err != nil {
		return "", diag.Diagnostics{diag.NewErrorDiagnostic("Error decoding manifest", err.Error())}
	}

	if m, ok := val.(map[string]any); ok {
		return marshalManifest(m, validate)
	} else if l, ok := val.([]any); ok {
		for _, vv := range l {
			m, ok := vv.(map[string]any)
			if !ok {
				diags.Append(diag.NewErrorDiagnostic(
					"List of manifests contained an invalid resource",
					fmt.Sprintf("value doesn't seem to be a manifest: %#v", vv),
				))
				return
			}
			s, d := marshalManifest(m, validate)
			if d.HasError() {
				return "", d
			}
			encoded = strings.Join([]string{encoded, s}, "---\n")
		}
		return encoded, nil
	}

	diags.Append(diag.NewErrorDiagnostic(
		"Invalid manifest", fmt.Sprintf("value doesn't seem to be a manifest: %#v", val)))
	return
}
