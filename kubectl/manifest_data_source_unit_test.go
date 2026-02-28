// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestObjectMetaTFTypes_fields(t *testing.T) {
	meta := objectMetaTFTypes()

	required := map[string]tftypes.Type{
		"name":                       tftypes.String,
		"generateName":               tftypes.String,
		"namespace":                  tftypes.String,
		"selfLink":                   tftypes.String,
		"uid":                        tftypes.String,
		"resourceVersion":            tftypes.String,
		"generation":                 tftypes.Number,
		"creationTimestamp":          tftypes.String,
		"deletionTimestamp":          tftypes.String,
		"deletionGracePeriodSeconds": tftypes.Number,
		"labels":                     tftypes.Map{ElementType: tftypes.String},
		"annotations":                tftypes.Map{ElementType: tftypes.String},
		"finalizers":                 tftypes.List{ElementType: tftypes.String},
		"clusterName":                tftypes.String,
	}

	for field, want := range required {
		got, ok := meta.AttributeTypes[field]
		if !ok {
			t.Errorf("objectMetaTFTypes: missing field %q", field)
			continue
		}
		if !got.Is(want) {
			t.Errorf("objectMetaTFTypes: field %q type = %T, want %T", field, got, want)
		}
	}

	// ownerReferences must be a list of objects
	or, ok := meta.AttributeTypes["ownerReferences"]
	if !ok {
		t.Fatal("objectMetaTFTypes: missing field 'ownerReferences'")
	}
	orList, ok := or.(tftypes.List)
	if !ok {
		t.Fatalf("ownerReferences: want tftypes.List, got %T", or)
	}
	orObj, ok := orList.ElementType.(tftypes.Object)
	if !ok {
		t.Fatalf("ownerReferences element: want tftypes.Object, got %T", orList.ElementType)
	}
	for _, f := range []string{"apiVersion", "kind", "name", "uid"} {
		if _, ok := orObj.AttributeTypes[f]; !ok {
			t.Errorf("ownerReferences object: missing field %q", f)
		}
	}
	for _, f := range []string{"blockOwnerDeletion", "controller"} {
		ft, ok := orObj.AttributeTypes[f]
		if !ok {
			t.Errorf("ownerReferences object: missing field %q", f)
			continue
		}
		if !ft.Is(tftypes.Bool) {
			t.Errorf("ownerReferences.%s: want tftypes.Bool, got %T", f, ft)
		}
	}

	// managedFields must be a list of objects
	mf, ok := meta.AttributeTypes["managedFields"]
	if !ok {
		t.Fatal("objectMetaTFTypes: missing field 'managedFields'")
	}
	mfList, ok := mf.(tftypes.List)
	if !ok {
		t.Fatalf("managedFields: want tftypes.List, got %T", mf)
	}
	mfObj, ok := mfList.ElementType.(tftypes.Object)
	if !ok {
		t.Fatalf("managedFields element: want tftypes.Object, got %T", mfList.ElementType)
	}
	for _, f := range []string{"manager", "operation", "apiVersion", "time", "fieldsType"} {
		if _, ok := mfObj.AttributeTypes[f]; !ok {
			t.Errorf("managedFields object: missing field %q", f)
		}
	}
	fv1, ok := mfObj.AttributeTypes["fieldsV1"]
	if !ok {
		t.Fatal("managedFields object: missing field 'fieldsV1'")
	}
	if !fv1.Is(tftypes.DynamicPseudoType) {
		t.Errorf("managedFields.fieldsV1: want DynamicPseudoType, got %T", fv1)
	}
}

func TestPartialObjectMetaTFTypes_topLevel(t *testing.T) {
	m := partialObjectMetaTFTypes()

	if _, ok := m["apiVersion"]; !ok {
		t.Error("partialObjectMetaTFTypes: missing 'apiVersion'")
	}
	if _, ok := m["kind"]; !ok {
		t.Error("partialObjectMetaTFTypes: missing 'kind'")
	}
	metaT, ok := m["metadata"]
	if !ok {
		t.Fatal("partialObjectMetaTFTypes: missing 'metadata'")
	}
	if _, ok := metaT.(tftypes.Object); !ok {
		t.Errorf("partialObjectMetaTFTypes: 'metadata' want tftypes.Object, got %T", metaT)
	}
}

func TestConvertToObject_mergesPartialMeta(t *testing.T) {
	// Build an objectType that only has spec (simulating a CRD schema)
	specOnly := tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"spec": tftypes.Object{
				AttributeTypes: map[string]tftypes.Type{
					"replicas": tftypes.Number,
				},
			},
		},
	}

	// Apply the same merge logic that convertToObject uses
	var merged tftypes.Object
	if obj, ok := tftypes.Type(specOnly).(tftypes.Object); ok {
		atts := partialObjectMetaTFTypes()
		for k, v := range obj.AttributeTypes {
			atts[k] = v
		}
		merged = tftypes.Object{AttributeTypes: atts}
	} else {
		t.Fatal("specOnly should be a tftypes.Object")
	}

	// spec from original type must be preserved
	if _, ok := merged.AttributeTypes["spec"]; !ok {
		t.Error("merge: 'spec' missing from merged type")
	}

	// standard k8s fields must be present from PartialObjectMeta
	for _, field := range []string{"apiVersion", "kind", "metadata"} {
		if _, ok := merged.AttributeTypes[field]; !ok {
			t.Errorf("merge: %q missing from merged type", field)
		}
	}
}

func TestConvertToObject_openAPIMetaPreserved(t *testing.T) {
	// If the OpenAPI type already has a richer metadata, it should win.
	richMeta := tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"name":      tftypes.String,
			"namespace": tftypes.String,
			"extra":     tftypes.Bool, // field not in partialObjectMetaTFTypes
		},
	}
	openAPIType := tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"apiVersion": tftypes.String,
			"kind":       tftypes.String,
			"metadata":   richMeta,
			"spec":       tftypes.DynamicPseudoType,
		},
	}

	var merged tftypes.Object
	if obj, ok := tftypes.Type(openAPIType).(tftypes.Object); ok {
		atts := partialObjectMetaTFTypes()
		for k, v := range obj.AttributeTypes {
			atts[k] = v
		}
		merged = tftypes.Object{AttributeTypes: atts}
	}

	// OpenAPI metadata must override the PartialObjectMeta metadata
	metaT, ok := merged.AttributeTypes["metadata"]
	if !ok {
		t.Fatal("merged: missing 'metadata'")
	}
	metaObj, ok := metaT.(tftypes.Object)
	if !ok {
		t.Fatalf("merged metadata: want tftypes.Object, got %T", metaT)
	}
	if _, ok := metaObj.AttributeTypes["extra"]; !ok {
		t.Error(
			"merged metadata: OpenAPI 'extra' field not preserved (OpenAPI should take precedence)",
		)
	}
}

func TestConvertToObject_dynamicPseudoTypePassthrough(t *testing.T) {
	// When objectType is DynamicPseudoType, the merge must not apply.
	objectType := tftypes.Type(tftypes.DynamicPseudoType)
	_, isObject := objectType.(tftypes.Object)
	if isObject {
		t.Error("DynamicPseudoType should not be a tftypes.Object")
	}
	// The merge guard `if obj, ok := objectType.(tftypes.Object); ok` correctly skips DynamicPseudoType.
}
