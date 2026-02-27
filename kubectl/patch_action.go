// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/action"
	actionschema "github.com/hashicorp/terraform-plugin-framework/action/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/api"
	yamlpkg "github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/yaml"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// Ensure interface satisfaction.
var (
	_ action.Action              = &patchAction{}
	_ action.ActionWithConfigure = &patchAction{}
)

// patchAction defines the action implementation for patching existing K8s resources.
// Unlike the kubectl_patch resource, this action is fire-and-forget: it applies
// a patch during invoke and does not manage state or revert on destroy.
type patchAction struct {
	providerData *kubectlProviderData
}

// patchActionModel describes the action configuration data model.
type patchActionModel struct {
	APIVersion     types.String  `tfsdk:"api_version"`
	Kind           types.String  `tfsdk:"kind"`
	Name           types.String  `tfsdk:"name"`
	Namespace      types.String  `tfsdk:"namespace"`
	Patch          types.Dynamic `tfsdk:"patch"`
	FieldManager   types.String  `tfsdk:"field_manager"`
	ForceConflicts types.Bool    `tfsdk:"force_conflicts"`
}

// NewPatchAction returns a new patch action.
func NewPatchAction() action.Action {
	return &patchAction{}
}

// Metadata returns the action type name.
func (a *patchAction) Metadata(
	ctx context.Context,
	req action.MetadataRequest,
	resp *action.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_patch"
}

// Schema defines the action schema.
func (a *patchAction) Schema(
	ctx context.Context,
	req action.SchemaRequest,
	resp *action.SchemaResponse,
) {
	resp.Schema = actionschema.Schema{
		MarkdownDescription: "Patches an existing Kubernetes resource using Server-Side Apply. " +
			"This action is fire-and-forget: it applies the patch when invoked but does not " +
			"track state or revert changes on destroy. Use this for one-shot operations like " +
			"triggering a rollout restart, scaling a deployment, or toggling a feature flag.",
		Attributes: map[string]actionschema.Attribute{
			"api_version": actionschema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Kubernetes API version of the target resource (e.g., `v1`, `apps/v1`).",
			},
			"kind": actionschema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Kubernetes resource kind of the target resource (e.g., `ConfigMap`, `Deployment`).",
			},
			"name": actionschema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the target Kubernetes resource.",
			},
			"namespace": actionschema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Namespace of the target Kubernetes resource. Omit for cluster-scoped resources.",
			},
			"patch": actionschema.DynamicAttribute{
				Required: true,
				MarkdownDescription: "An object containing the fields to patch on the target resource. " +
					"This is merged into the target using Server-Side Apply.",
			},
			"field_manager": actionschema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The name to use for the field manager. Default: `TerraformAction`.",
			},
			"force_conflicts": actionschema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Force changes against conflicts. Default: `true`.",
			},
		},
	}
}

// Configure receives the provider data.
func (a *patchAction) Configure(
	ctx context.Context,
	req action.ConfigureRequest,
	resp *action.ConfigureResponse,
) {
	if req.ProviderData == nil {
		return
	}

	providerData, ok := req.ProviderData.(*kubectlProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Action Configure Type",
			fmt.Sprintf(
				"Expected *kubectlProviderData, got: %T. Please report this issue to the provider developers.",
				req.ProviderData,
			),
		)
		return
	}

	a.providerData = providerData
}

// Invoke executes the patch against the Kubernetes API.
func (a *patchAction) Invoke(
	ctx context.Context,
	req action.InvokeRequest,
	resp *action.InvokeResponse,
) {
	var config patchActionModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiVersion := config.APIVersion.ValueString()
	kind := config.Kind.ValueString()
	name := config.Name.ValueString()

	namespace := ""
	if !config.Namespace.IsNull() && !config.Namespace.IsUnknown() {
		namespace = config.Namespace.ValueString()
	}

	fieldManager := "TerraformAction"
	if !config.FieldManager.IsNull() && !config.FieldManager.IsUnknown() {
		fieldManager = config.FieldManager.ValueString()
	}

	forceConflicts := true
	if !config.ForceConflicts.IsNull() && !config.ForceConflicts.IsUnknown() {
		forceConflicts = config.ForceConflicts.ValueBool()
	}

	// Convert patch Dynamic to map
	patchMap, diags := dynamicToMap(ctx, config.Patch)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if patchMap == nil {
		resp.Diagnostics.AddError("Invalid patch", "patch is required and cannot be null")
		return
	}

	// Build SSA patch payload
	payload := map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
	}

	metadata := map[string]any{
		"name": name,
	}
	if namespace != "" {
		metadata["namespace"] = namespace
	}

	// Merge patch metadata fields (labels, annotations, etc.)
	if patchMeta, ok := patchMap["metadata"]; ok {
		if patchMetaMap, ok := patchMeta.(map[string]any); ok {
			for k, v := range patchMetaMap {
				metadata[k] = v
			}
		}
	}
	payload["metadata"] = metadata

	// Merge non-metadata fields from patch
	for k, v := range patchMap {
		if k == "metadata" {
			continue
		}
		payload[k] = v
	}

	cleanedPayload := api.MapRemoveNulls(payload)

	uo := &meta_v1_unstruct.Unstructured{}
	uo.SetUnstructuredContent(cleanedPayload)

	// Send progress
	resp.SendProgress(action.InvokeProgressEvent{
		Message: fmt.Sprintf("Patching %s/%s %s...", kind, name, apiVersion),
	})

	// Build REST client
	tempUo := &meta_v1_unstruct.Unstructured{}
	tempUo.SetAPIVersion(apiVersion)
	tempUo.SetKind(kind)
	tempUo.SetName(name)
	if namespace != "" {
		tempUo.SetNamespace(namespace)
	}

	manifest := yamlpkg.NewFromUnstructured(tempUo)
	restClient := api.GetRestClientFromUnstructured(
		ctx,
		manifest,
		a.providerData.MainClientset,
		a.providerData.RestConfig,
	)
	if restClient.Error != nil {
		resp.Diagnostics.AddError(
			"Failed to Create REST Client",
			fmt.Sprintf("Could not create kubernetes rest client: %s", restClient.Error),
		)
		return
	}

	// Marshal to JSON
	jsonData, err := uo.MarshalJSON()
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Marshal Patch",
			fmt.Sprintf("Could not marshal patch to JSON: %s", err),
		)
		return
	}

	// Apply with a timeout
	patchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	result, err := restClient.ResourceInterface.Patch(
		patchCtx,
		name,
		k8stypes.ApplyPatchType,
		jsonData,
		meta_v1.PatchOptions{
			FieldManager: fieldManager,
			Force:        &forceConflicts,
		},
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Apply Patch",
			fmt.Sprintf("Could not apply patch to %s/%s %q: %s", kind, name, namespace, err),
		)
		return
	}

	log.Printf("[DEBUG] Action: successfully patched %s/%s (UID: %s)",
		result.GetKind(), result.GetName(), result.GetUID())

	resp.SendProgress(action.InvokeProgressEvent{
		Message: fmt.Sprintf("Successfully patched %s/%s %s", kind, name, apiVersion),
	})
}
