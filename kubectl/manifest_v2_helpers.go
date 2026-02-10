// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// buildUnstructured converts the new schema attributes (api_version, kind, metadata, spec)
// into a Kubernetes unstructured object.
// This is the core function that replaces yaml.ParseYAML() for the new schema.
func buildUnstructured(
	ctx context.Context,
	apiVersion, kind string,
	metadata, spec types.Dynamic,
) (*meta_v1_unstruct.Unstructured, diag.Diagnostics) {
	var diags diag.Diagnostics

	// Start with base structure
	obj := map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
	}

	// Convert metadata (Required)
	metadataMap, d := dynamicToMap(ctx, metadata)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}
	if metadataMap == nil {
		diags.AddError(
			"Invalid metadata",
			"metadata is required and cannot be null",
		)
		return nil, diags
	}
	obj["metadata"] = metadataMap

	// Convert spec (Optional)
	if !spec.IsNull() && !spec.IsUnknown() {
		specMap, d := dynamicToMap(ctx, spec)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
		if specMap != nil {
			obj["spec"] = specMap
		}
	}

	// Create unstructured object
	uo := &meta_v1_unstruct.Unstructured{}
	uo.SetUnstructuredContent(obj)

	return uo, diags
}

// setStateFromUnstructured populates the state model from a Kubernetes unstructured object.
// This is the core function that replaces the current readManifest() logic for the new schema.
func setStateFromUnstructured(
	ctx context.Context,
	uo *meta_v1_unstruct.Unstructured,
	model *manifestResourceModel,
) diag.Diagnostics {
	var diags diag.Diagnostics

	content := uo.UnstructuredContent()

	// Set api_version and kind (always present)
	model.APIVersion = types.StringValue(uo.GetAPIVersion())
	model.Kind = types.StringValue(uo.GetKind())

	// Set ID (computed)
	namespace := uo.GetNamespace()
	name := uo.GetName()
	if namespace != "" {
		model.ID = types.StringValue(fmt.Sprintf(
			"%s//%s//%s//%s",
			uo.GetAPIVersion(),
			uo.GetKind(),
			name,
			namespace,
		))
	} else {
		model.ID = types.StringValue(fmt.Sprintf(
			"%s//%s//%s",
			uo.GetAPIVersion(),
			uo.GetKind(),
			name,
		))
	}

	// Convert metadata to Dynamic
	if meta, ok := content["metadata"]; ok {
		metaDynamic, d := mapToDynamic(ctx, meta.(map[string]any))
		diags.Append(d...)
		if !diags.HasError() {
			model.Metadata = metaDynamic
		}
	} else {
		diags.AddError(
			"Missing metadata",
			"Kubernetes resource must have metadata",
		)
		return diags
	}

	// Convert spec to Dynamic (optional)
	if spec, ok := content["spec"]; ok {
		if specMap, ok := spec.(map[string]any); ok {
			specDynamic, d := mapToDynamic(ctx, specMap)
			diags.Append(d...)
			if !diags.HasError() {
				model.Spec = specDynamic
			}
		}
	} else {
		// spec is optional - set to null if not present
		model.Spec = types.DynamicNull()
	}

	// Convert status to Dynamic (computed, optional)
	if status, ok := content["status"]; ok {
		if statusMap, ok := status.(map[string]any); ok {
			statusDynamic, d := mapToDynamic(ctx, statusMap)
			diags.Append(d...)
			if !diags.HasError() {
				model.Status = statusDynamic
			}
		}
	} else {
		// status may not be present for all resources
		model.Status = types.DynamicNull()
	}

	// Convert full object to Dynamic (computed)
	objectDynamic, d := mapToDynamic(ctx, content)
	diags.Append(d...)
	if !diags.HasError() {
		model.Object = objectDynamic
	}

	return diags
}

// extractMetadataField safely extracts a field from the metadata Dynamic attribute.
func extractMetadataField(
	ctx context.Context,
	metadata types.Dynamic,
	fieldName string,
) (string, error) {
	if metadata.IsNull() || metadata.IsUnknown() {
		return "", fmt.Errorf("metadata is null or unknown")
	}

	metaMap, diags := dynamicToMap(ctx, metadata)
	if diags.HasError() {
		return "", fmt.Errorf("failed to convert metadata: %v", diags)
	}

	if val, ok := metaMap[fieldName]; ok {
		if strVal, ok := val.(string); ok {
			return strVal, nil
		}
		return "", fmt.Errorf("metadata.%s is not a string", fieldName)
	}

	return "", nil // Field not present (may be optional)
}

// manifestResourceModelV2 is now unified as manifestResourceModel - kept as alias for backward compatibility.
type manifestResourceModelV2 = manifestResourceModel
