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

// buildUnstructured converts the model's manifest Dynamic attribute
// into a Kubernetes unstructured object.
func buildUnstructured(
	ctx context.Context,
	model *manifestResourceModel,
) (*meta_v1_unstruct.Unstructured, diag.Diagnostics) {
	var diags diag.Diagnostics

	manifestMap, d := dynamicToMap(ctx, model.Manifest)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}
	if manifestMap == nil {
		diags.AddError(
			"Invalid manifest",
			"manifest is required and cannot be null",
		)
		return nil, diags
	}

	uo := &meta_v1_unstruct.Unstructured{}
	uo.SetUnstructuredContent(manifestMap)

	return uo, diags
}

// setStateFromUnstructured populates the state model from a Kubernetes unstructured object.
// It sets model.Manifest (without status), model.Status, model.Object, and model.ID.
func setStateFromUnstructured(
	ctx context.Context,
	uo *meta_v1_unstruct.Unstructured,
	model *manifestResourceModel,
) diag.Diagnostics {
	var diags diag.Diagnostics

	content := uo.UnstructuredContent()

	// Set ID to Kubernetes UID (or fallback)
	name := uo.GetName()
	namespace := uo.GetNamespace()
	uid := string(uo.GetUID())
	if uid != "" {
		model.ID = types.StringValue(uid)
	} else {
		if namespace != "" {
			model.ID = types.StringValue(fmt.Sprintf(
				"%s//%s//%s//%s",
				uo.GetAPIVersion(), uo.GetKind(), name, namespace,
			))
		} else {
			model.ID = types.StringValue(fmt.Sprintf(
				"%s//%s//%s",
				uo.GetAPIVersion(), uo.GetKind(), name,
			))
		}
	}

	// Build manifest from content (without status)
	manifestContent := make(map[string]any)
	for k, v := range content {
		if k != "status" {
			manifestContent[k] = v
		}
	}
	manifestDynamic, d := mapToDynamic(ctx, manifestContent)
	diags.Append(d...)
	if !diags.HasError() {
		model.Manifest = manifestDynamic
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

// extractManifestField safely extracts a top-level field from the manifest Dynamic attribute.
// Returns the raw value (any type) and an error.
func extractManifestField(
	ctx context.Context,
	manifest types.Dynamic,
	fieldName string,
) (any, error) {
	if manifest.IsNull() || manifest.IsUnknown() {
		return nil, fmt.Errorf("manifest is null or unknown")
	}

	manifestMap, diags := dynamicToMap(ctx, manifest)
	if diags.HasError() {
		return nil, fmt.Errorf("failed to convert manifest: %v", diags)
	}

	if val, ok := manifestMap[fieldName]; ok {
		return val, nil
	}

	return nil, nil // Field not present
}

// extractManifestMetadataField safely extracts a field from manifest.metadata.
// Returns the string value and an error.
func extractManifestMetadataField(
	ctx context.Context,
	manifest types.Dynamic,
	fieldName string,
) (string, error) {
	metadataAny, err := extractManifestField(ctx, manifest, "metadata")
	if err != nil {
		return "", err
	}
	if metadataAny == nil {
		return "", nil
	}

	metadataMap, ok := metadataAny.(map[string]any)
	if !ok {
		return "", fmt.Errorf("manifest.metadata is not a map")
	}

	if val, ok := metadataMap[fieldName]; ok {
		if strVal, ok := val.(string); ok {
			return strVal, nil
		}
		return fmt.Sprintf("%v", val), nil
	}

	return "", nil // Field not present (may be optional)
}

// manifestResourceModelV2 is now unified as manifestResourceModel - kept as alias for backward compatibility.
type manifestResourceModelV2 = manifestResourceModel

// reconcileDynamicWithPrior reconciles a Dynamic value from the API with a
// prior Dynamic value (from state). It keeps only the attributes that existed
// in the prior value, updated with current values from the API.
// This prevents perpetual diffs caused by server-generated fields.
// It recursively reconciles nested maps so that server-defaulted fields
// at any depth are excluded (e.g., spec.template.spec.dnsPolicy).
func reconcileDynamicWithPrior(
	ctx context.Context,
	prior types.Dynamic,
	apiResult types.Dynamic,
) types.Dynamic {
	if prior.IsNull() || prior.IsUnknown() {
		return prior
	}
	if apiResult.IsNull() || apiResult.IsUnknown() {
		return prior
	}

	priorMap, d := dynamicToMap(ctx, prior)
	if d.HasError() || priorMap == nil {
		return prior
	}
	apiMap, d := dynamicToMap(ctx, apiResult)
	if d.HasError() || apiMap == nil {
		return prior
	}

	// Deep reconcile: keep only attributes from prior, recursing into nested maps
	result := deepReconcileMaps(priorMap, apiMap)

	dynResult, d := mapToDynamic(ctx, result)
	if d.HasError() {
		return prior
	}
	return dynResult
}

// deepReconcileMaps recursively reconciles two maps, keeping only keys from
// the prior map but using values from the API map (which may have been updated).
// For nested maps, it recurses to filter server-generated fields at all depths.
// For arrays of maps, it reconciles each element by index.
func deepReconcileMaps(prior, api map[string]any) map[string]any {
	result := make(map[string]any, len(prior))
	for k, priorVal := range prior {
		apiVal, ok := api[k]
		if !ok {
			result[k] = priorVal
			continue
		}

		// Recurse into nested maps
		priorMap, priorIsMap := priorVal.(map[string]any)
		apiMap, apiIsMap := apiVal.(map[string]any)
		if priorIsMap && apiIsMap {
			result[k] = deepReconcileMaps(priorMap, apiMap)
			continue
		}

		// Recurse into arrays of maps (e.g., containers, volumes)
		priorSlice, priorIsSlice := priorVal.([]any)
		apiSlice, apiIsSlice := apiVal.([]any)
		if priorIsSlice && apiIsSlice {
			result[k] = deepReconcileSlices(priorSlice, apiSlice)
			continue
		}

		// Scalar or type mismatch: use API value
		result[k] = apiVal
	}
	return result
}

// deepReconcileSlices reconciles two slices element-by-element.
// For elements that are maps, it reconciles them recursively.
// If the API slice has fewer elements, keeps prior elements.
func deepReconcileSlices(prior, api []any) []any {
	result := make([]any, len(prior))
	for i, priorElem := range prior {
		if i >= len(api) {
			result[i] = priorElem
			continue
		}
		priorMap, priorIsMap := priorElem.(map[string]any)
		apiMap, apiIsMap := api[i].(map[string]any)
		if priorIsMap && apiIsMap {
			result[i] = deepReconcileMaps(priorMap, apiMap)
		} else {
			result[i] = api[i]
		}
	}
	return result
}
