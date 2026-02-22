// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/alekc/terraform-provider-kubectl/kubectl/api"
	yamlpkg "github.com/alekc/terraform-provider-kubectl/kubectl/yaml"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/dynamicplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource               = &patchResource{}
	_ resource.ResourceWithConfigure  = &patchResource{}
	_ resource.ResourceWithModifyPlan = &patchResource{}
)

// patchResource defines the resource implementation for patching existing K8s resources.
type patchResource struct {
	providerData *kubectlProviderData
}

// patchResourceModel describes the resource data model.
type patchResourceModel struct {
	ID           types.String   `tfsdk:"id"`
	APIVersion   types.String   `tfsdk:"api_version"`
	Kind         types.String   `tfsdk:"kind"`
	Name         types.String   `tfsdk:"name"`
	Namespace    types.String   `tfsdk:"namespace"`
	Patch        types.Dynamic  `tfsdk:"patch"`
	Object       types.Dynamic  `tfsdk:"object"`
	FieldManager types.List     `tfsdk:"field_manager"`
	Timeouts     timeouts.Value `tfsdk:"timeouts"`
}

// patchFieldManagerModel describes the field_manager block.
type patchFieldManagerModel struct {
	Name           types.String `tfsdk:"name"`
	ForceConflicts types.Bool   `tfsdk:"force_conflicts"`
}

// NewPatchResource returns a new patch resource.
func NewPatchResource() resource.Resource {
	return &patchResource{}
}

// Metadata returns the resource type name.
func (r *patchResource) Metadata(
	ctx context.Context,
	req resource.MetadataRequest,
	resp *resource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_patch"
}

// Schema defines the resource schema.
func (r *patchResource) Schema(
	ctx context.Context,
	req resource.SchemaRequest,
	resp *resource.SchemaResponse,
) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Patches an existing Kubernetes resource using Server-Side Apply. " +
			"Unlike `kubectl_manifest`, this resource does not manage the full lifecycle of the target — " +
			"it only applies and reverts a targeted patch. Use this to add labels, annotations, or " +
			"modify specific fields on resources managed outside of Terraform.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Composite identifier in the form `apiVersion//kind//name[//namespace]`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"api_version": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Kubernetes API version of the target resource (e.g., `v1`, `apps/v1`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"kind": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Kubernetes resource kind of the target resource (e.g., `ConfigMap`, `Deployment`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the target Kubernetes resource.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"namespace": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Namespace of the target Kubernetes resource. Omit for cluster-scoped resources.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"patch": schema.DynamicAttribute{
				Required: true,
				MarkdownDescription: "An object containing the fields to patch on the target resource. " +
					"This is merged into the target using Server-Side Apply. For example, to add a label: " +
					"`{ metadata = { labels = { \"my-label\" = \"my-value\" } } }`.",
			},
			"object": schema.DynamicAttribute{
				Computed:            true,
				MarkdownDescription: "The full resource object after the patch has been applied.",
				PlanModifiers: []planmodifier.Dynamic{
					dynamicplanmodifier.UseStateForUnknown(),
				},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{
				Create: true,
				Update: true,
				Delete: true,
			}),
		},
		Blocks: map[string]schema.Block{
			"field_manager": schema.ListNestedBlock{
				MarkdownDescription: "Configure field manager options for server-side apply.",
				Validators: []validator.List{
					listvalidator.SizeAtMost(1),
				},
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Optional:            true,
							Computed:            true,
							Default:             stringdefault.StaticString("Terraform"),
							MarkdownDescription: "The name to use for the field manager. Default: `Terraform`",
						},
						"force_conflicts": schema.BoolAttribute{
							Optional:            true,
							Computed:            true,
							Default:             booldefault.StaticBool(false),
							MarkdownDescription: "Force changes against conflicts. Default: `false`",
						},
					},
				},
			},
		},
	}
}

// Configure sets the provider data for the resource.
func (r *patchResource) Configure(
	ctx context.Context,
	req resource.ConfigureRequest,
	resp *resource.ConfigureResponse,
) {
	if req.ProviderData == nil {
		return
	}

	providerData, ok := req.ProviderData.(*kubectlProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf(
				"Expected *kubectlProviderData, got: %T. Please report this issue to the provider developers.",
				req.ProviderData,
			),
		)
		return
	}

	r.providerData = providerData
}

// Create applies the patch to the target resource.
func (r *patchResource) Create(
	ctx context.Context,
	req resource.CreateRequest,
	resp *resource.CreateResponse,
) {
	var plan patchResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	createTimeout, d := plan.Timeouts.Create(ctx, 5*time.Minute)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	createCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	if err := r.applyPatch(createCtx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Apply Patch",
			fmt.Sprintf("Could not apply patch: %s", err),
		)
		return
	}

	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// Read reads the current state of the patched resource.
func (r *patchResource) Read(
	ctx context.Context,
	req resource.ReadRequest,
	resp *resource.ReadResponse,
) {
	var state patchResourceModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readPatchedResource(ctx, &state); err != nil {
		if isNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Failed to Read Resource",
			fmt.Sprintf("Could not read patched resource: %s", err),
		)
		return
	}

	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

// Update re-applies the patch with the new values.
func (r *patchResource) Update(
	ctx context.Context,
	req resource.UpdateRequest,
	resp *resource.UpdateResponse,
) {
	var plan patchResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateTimeout, d := plan.Timeouts.Update(ctx, 5*time.Minute)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateCtx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	if err := r.applyPatch(updateCtx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Update Patch",
			fmt.Sprintf("Could not apply updated patch: %s", err),
		)
		return
	}

	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// Delete reverts the patch by applying an empty patch with the same field manager.
// This removes the fields owned by this Terraform resource's field manager.
func (r *patchResource) Delete(
	ctx context.Context,
	req resource.DeleteRequest,
	resp *resource.DeleteResponse,
) {
	var state patchResourceModel
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteTimeout, d := state.Timeouts.Delete(ctx, 5*time.Minute)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteCtx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()

	if err := r.revertPatch(deleteCtx, &state); err != nil {
		if isNotFoundError(err) {
			log.Printf("[DEBUG] Target resource already deleted, nothing to revert")
			return
		}

		resp.Diagnostics.AddError(
			"Failed to Revert Patch",
			fmt.Sprintf("Could not revert patch on delete: %s", err),
		)
	}
}

// ModifyPlan handles plan modification.
func (r *patchResource) ModifyPlan(
	ctx context.Context,
	req resource.ModifyPlanRequest,
	resp *resource.ModifyPlanResponse,
) {
	// On create or destroy, nothing to do
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var plan, state patchResourceModel
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	diags = req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If patch changed, object is unknown (new value after apply)
	if !plan.Patch.Equal(state.Patch) {
		plan.Object = types.DynamicUnknown()
	} else {
		plan.Object = state.Object
	}

	diags = resp.Plan.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// --- Internal helpers ---

// getFieldManagerConfig extracts field manager name and force_conflicts from the model.
func (r *patchResource) getFieldManagerConfig(
	ctx context.Context,
	model *patchResourceModel,
) (string, bool, diag.Diagnostics) {
	fieldManagerName := "Terraform"
	forceConflicts := false

	if !model.FieldManager.IsNull() {
		var fmModels []patchFieldManagerModel
		diags := model.FieldManager.ElementsAs(ctx, &fmModels, false)
		if diags.HasError() {
			return "", false, diags
		}
		if len(fmModels) > 0 {
			if !fmModels[0].Name.IsNull() {
				fieldManagerName = fmModels[0].Name.ValueString()
			}
			if !fmModels[0].ForceConflicts.IsNull() {
				forceConflicts = fmModels[0].ForceConflicts.ValueBool()
			}
		}
	}

	return fieldManagerName, forceConflicts, nil
}

// buildPatchUnstructured constructs the Unstructured patch payload from the model.
// The patch payload includes apiVersion, kind, metadata (name + namespace), and
// all fields from the patch Dynamic attribute merged at the top level.
func (r *patchResource) buildPatchUnstructured(
	ctx context.Context,
	model *patchResourceModel,
) (*meta_v1_unstruct.Unstructured, diag.Diagnostics) {
	var diags diag.Diagnostics

	// Convert patch Dynamic to map
	patchMap, d := dynamicToMap(ctx, model.Patch)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}
	if patchMap == nil {
		diags.AddError("Invalid patch", "patch is required and cannot be null")
		return nil, diags
	}

	// Build the SSA patch payload: must contain apiVersion, kind, metadata
	payload := map[string]any{
		"apiVersion": model.APIVersion.ValueString(),
		"kind":       model.Kind.ValueString(),
	}

	// Build metadata with name (and namespace if set)
	metadata := map[string]any{
		"name": model.Name.ValueString(),
	}
	if !model.Namespace.IsNull() && model.Namespace.ValueString() != "" {
		metadata["namespace"] = model.Namespace.ValueString()
	}

	// If the patch contains metadata fields (e.g., labels, annotations), merge them
	if patchMeta, ok := patchMap["metadata"]; ok {
		if patchMetaMap, ok := patchMeta.(map[string]any); ok {
			for k, v := range patchMetaMap {
				metadata[k] = v
			}
		}
	}
	payload["metadata"] = metadata

	// Merge all non-metadata fields from the patch into the payload
	for k, v := range patchMap {
		if k == "metadata" {
			continue // Already handled above
		}
		payload[k] = v
	}

	// Remove null values
	cleanedPayload := api.MapRemoveNulls(payload)

	uo := &meta_v1_unstruct.Unstructured{}
	uo.SetUnstructuredContent(cleanedPayload)

	return uo, diags
}

// getRestClient creates a REST client for the target resource.
func (r *patchResource) getRestClient(
	ctx context.Context,
	model *patchResourceModel,
) (*api.RestClientResult, error) {
	// Build a temporary Unstructured to resolve the REST client
	tempUo := &meta_v1_unstruct.Unstructured{}
	tempUo.SetAPIVersion(model.APIVersion.ValueString())
	tempUo.SetKind(model.Kind.ValueString())
	tempUo.SetName(model.Name.ValueString())
	if !model.Namespace.IsNull() && model.Namespace.ValueString() != "" {
		tempUo.SetNamespace(model.Namespace.ValueString())
	}

	manifest := yamlpkg.NewFromUnstructured(tempUo)
	restClient := api.GetRestClientFromUnstructured(
		ctx,
		manifest,
		r.providerData.MainClientset,
		r.providerData.RestConfig,
	)
	if restClient.Error != nil {
		return nil, fmt.Errorf("failed to create kubernetes rest client: %w", restClient.Error)
	}

	return restClient, nil
}

// applyPatch applies the SSA patch to the target resource.
func (r *patchResource) applyPatch(
	ctx context.Context,
	model *patchResourceModel,
) error {
	fieldManagerName, forceConflicts, diags := r.getFieldManagerConfig(ctx, model)
	if diags.HasError() {
		return fmt.Errorf("failed to get field manager config: %v", diags)
	}

	uo, d := r.buildPatchUnstructured(ctx, model)
	if d.HasError() {
		return fmt.Errorf("failed to build patch payload: %v", d)
	}

	log.Printf("[DEBUG] Applying patch to %s/%s/%s",
		model.APIVersion.ValueString(), model.Kind.ValueString(), model.Name.ValueString())

	restClient, err := r.getRestClient(ctx, model)
	if err != nil {
		return err
	}

	// Marshal to JSON for SSA
	jsonData, err := uo.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal patch to JSON: %w", err)
	}

	// Apply using Server-Side Apply
	result, err := restClient.ResourceInterface.Patch(
		ctx,
		model.Name.ValueString(),
		k8stypes.ApplyPatchType,
		jsonData,
		meta_v1.PatchOptions{
			FieldManager: fieldManagerName,
			Force:        &forceConflicts,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to apply patch: %w", err)
	}

	log.Printf("[DEBUG] Successfully applied patch to: %s/%s (UID: %s)",
		result.GetKind(), result.GetName(), result.GetUID())

	// Set ID
	r.setID(model)

	// Set object from result
	return r.setObjectFromResult(ctx, result, model)
}

// readPatchedResource reads the target resource from Kubernetes.
func (r *patchResource) readPatchedResource(
	ctx context.Context,
	model *patchResourceModel,
) error {
	restClient, err := r.getRestClient(ctx, model)
	if err != nil {
		return err
	}

	result, err := restClient.ResourceInterface.Get(
		ctx,
		model.Name.ValueString(),
		meta_v1.GetOptions{},
	)
	if err != nil {
		return err
	}

	return r.setObjectFromResult(ctx, result, model)
}

// revertPatch reverts the patch by applying an empty SSA patch with the same
// field manager. This causes the API server to remove all fields previously
// owned by this field manager that are not in the new (empty) apply request.
func (r *patchResource) revertPatch(
	ctx context.Context,
	model *patchResourceModel,
) error {
	fieldManagerName, forceConflicts, diags := r.getFieldManagerConfig(ctx, model)
	if diags.HasError() {
		return fmt.Errorf("failed to get field manager config: %v", diags)
	}

	// Build a minimal SSA payload with just identity fields.
	// When applied with the same field manager, the API server will release
	// ownership of all fields that this field manager previously owned
	// but are not present in this new apply request.
	payload := map[string]any{
		"apiVersion": model.APIVersion.ValueString(),
		"kind":       model.Kind.ValueString(),
		"metadata": map[string]any{
			"name": model.Name.ValueString(),
		},
	}
	if !model.Namespace.IsNull() && model.Namespace.ValueString() != "" {
		metadata, ok := payload["metadata"].(map[string]any)
		if !ok {
			return fmt.Errorf("metadata is not a map[string]any")
		}
		metadata["namespace"] = model.Namespace.ValueString()
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal revert payload: %w", err)
	}

	restClient, err := r.getRestClient(ctx, model)
	if err != nil {
		return err
	}

	log.Printf("[DEBUG] Reverting patch on %s/%s/%s (field_manager=%s)",
		model.APIVersion.ValueString(), model.Kind.ValueString(),
		model.Name.ValueString(), fieldManagerName)

	_, err = restClient.ResourceInterface.Patch(
		ctx,
		model.Name.ValueString(),
		k8stypes.ApplyPatchType,
		jsonData,
		meta_v1.PatchOptions{
			FieldManager: fieldManagerName,
			Force:        &forceConflicts,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to revert patch: %w", err)
	}

	log.Printf("[DEBUG] Successfully reverted patch on %s/%s",
		model.Kind.ValueString(), model.Name.ValueString())

	return nil
}

// setID sets the composite ID on the model.
func (r *patchResource) setID(model *patchResourceModel) {
	if !model.Namespace.IsNull() && model.Namespace.ValueString() != "" {
		model.ID = types.StringValue(fmt.Sprintf(
			"%s//%s//%s//%s",
			model.APIVersion.ValueString(),
			model.Kind.ValueString(),
			model.Name.ValueString(),
			model.Namespace.ValueString(),
		))
	} else {
		model.ID = types.StringValue(fmt.Sprintf(
			"%s//%s//%s",
			model.APIVersion.ValueString(),
			model.Kind.ValueString(),
			model.Name.ValueString(),
		))
	}
}

// setObjectFromResult converts the Kubernetes response to a Dynamic value and sets it on the model.
func (r *patchResource) setObjectFromResult(
	ctx context.Context,
	result *meta_v1_unstruct.Unstructured,
	model *patchResourceModel,
) error {
	content := result.UnstructuredContent()
	objectDynamic, d := mapToDynamic(ctx, content)
	if d.HasError() {
		return fmt.Errorf("failed to convert object to dynamic: %v", d)
	}
	model.Object = objectDynamic

	// Ensure ID is set
	r.setID(model)

	return nil
}

// patchFieldManagerBlockObjectType returns the attr.Type for the field_manager block.
//
//nolint:unused
func patchFieldManagerBlockObjectType() attr.Type {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"name":            types.StringType,
			"force_conflicts": types.BoolType,
		},
	}
}
