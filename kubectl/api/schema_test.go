// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package api

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestExtensionBool(t *testing.T) {
	tests := map[string]struct {
		input    any
		expected bool
	}{
		"native true":    {input: true, expected: true},
		"native false":   {input: false, expected: false},
		"json true":      {input: json.RawMessage(`true`), expected: true},
		"json false":     {input: json.RawMessage(`false`), expected: false},
		"json invalid":   {input: json.RawMessage(`"not-a-bool"`), expected: false},
		"json malformed": {input: json.RawMessage(`{`), expected: false},
		"nil":            {input: nil, expected: false},
		"string":         {input: "true", expected: false},
		"int":            {input: 1, expected: false},
		"float":          {input: 1.0, expected: false},
		"json empty":     {input: json.RawMessage(``), expected: false},
		"json null":      {input: json.RawMessage(`null`), expected: false},
		"json number":    {input: json.RawMessage(`1`), expected: false},
		"json object":    {input: json.RawMessage(`{"key":"val"}`), expected: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := extensionBool(tc.input)
			if got != tc.expected {
				t.Errorf("extensionBool(%v) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}

func TestIsTypeFullyKnown(t *testing.T) {
	type testSample struct {
		s bool
		t tftypes.Type
	}

	type testSamples map[string]testSample

	samples := testSamples{
		"DynamicPseudoType": {
			s: false,
			t: tftypes.DynamicPseudoType,
		},
		"String": {
			s: true,
			t: tftypes.String,
		},
		"StringList": {
			s: true,
			t: tftypes.List{ElementType: tftypes.String},
		},
		"DynamicPseudoTypeList": {
			s: false,
			t: tftypes.List{ElementType: tftypes.DynamicPseudoType},
		},
		"DynamicPseudoTypeMap": {
			s: false,
			t: tftypes.Map{ElementType: tftypes.DynamicPseudoType},
		},
		"StringMap": {
			s: true,
			t: tftypes.Map{ElementType: tftypes.String},
		},
		"Object": {
			s: true,
			t: tftypes.Object{
				AttributeTypes: map[string]tftypes.Type{
					"foo": tftypes.String,
					"bar": tftypes.Number,
				},
			},
		},
		"ObjectDynamic": {
			s: false,
			t: tftypes.Object{
				AttributeTypes: map[string]tftypes.Type{
					"foo": tftypes.String,
					"bar": tftypes.DynamicPseudoType,
				},
			},
		},
		"ListObject": {
			s: true,
			t: tftypes.List{
				ElementType: tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"foo": tftypes.String,
						"bar": tftypes.Number,
					},
				},
			},
		},
		"ListObjectDynamic": {
			s: false,
			t: tftypes.List{
				ElementType: tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"foo": tftypes.String,
						"bar": tftypes.DynamicPseudoType,
					},
				},
			},
		},
	}

	for name, v := range samples {
		t.Run(name,
			func(t *testing.T) {
				if isTypeFullyKnown(v.t) != v.s {
					t.Fatalf("sample %s failed", name)
				}
			})
	}
}
