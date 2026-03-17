// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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
		if val == nil {
			return "", nil
		}
		if strVal, ok := val.(string); ok {
			return strVal, nil
		}
		return fmt.Sprintf("%v", val), nil
	}

	return "", nil // Field not present (may be optional)
}

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

	// Preserve the container types (Object vs Map, Tuple vs List) from the
	// prior value so the state's Dynamic matches the plan's Dynamic on
	// subsequent plan cycles. Without this, mapToDynamic always produces
	// ObjectValue/TupleValue, which can differ from the plan type when the
	// HCL config produced MapValue/ListValue, causing spurious hasChange=true
	// and "(known after apply)" churn on object/status.
	dynResult, d := mapToDynamicPreservingTypes(ctx, result, prior)
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

		// Scalar or type mismatch: use API value, but don't overwrite a
		// configured non-null prior value with a null from the remote.
		// The API server (via OpenAPI schema filling + morph.UnknownToNull)
		// sets absent optional fields to null; we should not let those nulls
		// erase fields the user explicitly configured.
		if apiVal == nil && priorVal != nil {
			result[k] = priorVal
		} else {
			result[k] = apiVal
		}
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
		} else if api[i] == nil && priorElem != nil {
			// Don't overwrite a non-null prior element with null from remote.
			result[i] = priorElem
		} else {
			result[i] = api[i]
		}
	}
	return result
}

// walkMapByTFPath extracts a value from a nested map using a tftypes.AttributePath.
// Supports AttributeName (map key), ElementKeyString (map key), and ElementKeyInt (slice index).
// Returns (value, true) if found, (nil, false) if any step fails to resolve.
func walkMapByTFPath(m map[string]any, p *tftypes.AttributePath) (any, bool) {
	var current any = m
	for _, step := range p.Steps() {
		switch s := step.(type) {
		case tftypes.AttributeName:
			cm, ok := current.(map[string]any)
			if !ok {
				return nil, false
			}
			current, ok = cm[string(s)]
			if !ok {
				return nil, false
			}
		case tftypes.ElementKeyString:
			cm, ok := current.(map[string]any)
			if !ok {
				return nil, false
			}
			current, ok = cm[string(s)]
			if !ok {
				return nil, false
			}
		case tftypes.ElementKeyInt:
			cl, ok := current.([]any)
			if !ok {
				return nil, false
			}
			idx := int(s)
			if idx < 0 || idx >= len(cl) {
				return nil, false
			}
			current = cl[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

// extractManifestWoFromConfig reads the manifest_wo attribute from config
// (write-only attributes must be read from config, not plan/state).
func extractManifestWoFromConfig(
	ctx context.Context,
	config tfsdk.Config,
	diagnostics *diag.Diagnostics,
) types.Dynamic {
	var manifestWo types.Dynamic
	d := config.GetAttribute(ctx, path.Root("manifest_wo"), &manifestWo)
	diagnostics.Append(d...)
	return manifestWo
}

// computeManifestWoChecksum produces a deterministic SHA-256 hex digest of
// manifest_wo so that changes to write-only values can be detected across plan cycles.
func computeManifestWoChecksum(m map[string]any) string {
	data, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// extractLeafPaths collects all leaf (non-map) key paths from a nested map
// using dot-separated notation. Used to determine which paths from manifest_wo
// should be masked in the object attribute.
func extractLeafPaths(m map[string]any, prefix string) []string {
	var paths []string
	for k, v := range m {
		fullPath := k
		if prefix != "" {
			fullPath = prefix + "." + k
		}
		if subMap, ok := v.(map[string]any); ok {
			paths = append(paths, extractLeafPaths(subMap, fullPath)...)
		} else {
			paths = append(paths, fullPath)
		}
	}
	sort.Strings(paths)
	return paths
}

// deepMergeMaps recursively merges overlay into base in-place.
// For map values, it recurses; for all other types, overlay values replace base values.
func deepMergeMaps(base, overlay map[string]any) {
	for k, v := range overlay {
		if subOverlay, ok := v.(map[string]any); ok {
			if subBase, ok := base[k].(map[string]any); ok {
				deepMergeMaps(subBase, subOverlay)
				continue
			}
		}
		base[k] = v
	}
}

// maskFieldsWoPaths removes write-only field paths from model.Object to prevent
// sensitive values from being stored in Terraform state.
func maskFieldsWoPaths(ctx context.Context, model *manifestResourceModel, keys []string) error {
	if model.Object.IsNull() || model.Object.IsUnknown() {
		return nil
	}
	objMap, d := dynamicToMap(ctx, model.Object)
	if d.HasError() {
		return fmt.Errorf("failed to convert object to map for WO masking: %v", d)
	}
	if objMap == nil {
		return nil
	}
	for _, key := range keys {
		woDeleteAtPath(objMap, strings.Split(key, "."))
	}
	maskedDynamic, d := mapToDynamic(ctx, objMap)
	if d.HasError() {
		return fmt.Errorf("failed to convert masked object back to dynamic: %v", d)
	}
	model.Object = maskedDynamic
	return nil
}

// woDeleteAtPath removes the leaf key at the given path parts from the nested structure.
// Silently skips missing paths.
func woDeleteAtPath(node any, parts []string) {
	if len(parts) == 0 || node == nil {
		return
	}
	if len(parts) == 1 {
		if n, ok := node.(map[string]any); ok {
			delete(n, parts[0])
		}
		return
	}
	switch n := node.(type) {
	case map[string]any:
		child, exists := n[parts[0]]
		if !exists {
			return
		}
		woDeleteAtPath(child, parts[1:])
	case []any:
		idx, err := strconv.Atoi(parts[0])
		if err != nil || idx < 0 || idx >= len(n) {
			return
		}
		woDeleteAtPath(n[idx], parts[1:])
	}
}
