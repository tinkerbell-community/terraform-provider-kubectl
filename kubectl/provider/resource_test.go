// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"reflect"
	"testing"
)

func TestRemoveNulls(t *testing.T) {
	samples := []struct {
		in  map[string]any
		out map[string]any
	}{
		{
			in: map[string]any{
				"foo": nil,
			},
			out: map[string]any{},
		},
		{
			in: map[string]any{
				"foo": nil,
				"bar": "test",
			},
			out: map[string]any{
				"bar": "test",
			},
		},
		{
			in: map[string]any{
				"foo": nil,
				"bar": []any{nil, "test"},
			},
			out: map[string]any{
				"bar": []any{"test"},
			},
		},
		{
			in: map[string]any{
				"foo": nil,
				"bar": []any{
					map[string]any{
						"some":  nil,
						"other": "data",
					},
					"test",
				},
			},
			out: map[string]any{
				"bar": []any{
					map[string]any{
						"other": "data",
					},
					"test",
				},
			},
		},
	}

	for i, s := range samples {
		t.Run(fmt.Sprintf("sample%d", i+1), func(t *testing.T) {
			o := mapRemoveNulls(s.in)
			if !reflect.DeepEqual(s.out, o) {
				t.Fatal("sample and output are not equal")
			}
		})
	}
}
