package kubectl

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/alekc/terraform-provider-kubectl/yaml"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Ensure provider defined types fully satisfy framework interfaces
var (
	_ resource.Resource                = &manifestResource{}
	_ resource.ResourceWithConfigure   = &manifestResource{}
	_ resource.ResourceWithImportState = &manifestResource{}
	_ resource.ResourceWithModifyPlan  = &manifestResource{}
)

// manifestResource defines the resource implementation
type manifestResource struct {
	providerData *kubectlProviderData
}

// manifestResourceModel describes the resource data model
type manifestResourceModel struct {
	ID                     types.String   `tfsdk:"id"`
	UID                    types.String   `tfsdk:"uid"`
	LiveUID                types.String   `tfsdk:"live_uid"`
	YAMLInCluster          types.String   `tfsdk:"yaml_incluster"`
	LiveManifestInCluster  types.String   `tfsdk:"live_manifest_incluster"`
	APIVersion             types.String   `tfsdk:"api_version"`
	Kind                   types.String   `tfsdk:"kind"`
	Name                   types.String   `tfsdk:"name"`
	Namespace              types.String   `tfsdk:"namespace"`
	OverrideNamespace      types.String   `tfsdk:"override_namespace"`
	YAMLBody               types.String   `tfsdk:"yaml_body"`
	YAMLBodyParsed         types.String   `tfsdk:"yaml_body_parsed"`
	SensitiveFields        types.List     `tfsdk:"sensitive_fields"`
	ForceNew               types.Bool     `tfsdk:"force_new"`
	ServerSideApply        types.Bool     `tfsdk:"server_side_apply"`
	FieldManager           types.String   `tfsdk:"field_manager"`
	ForceConflicts         types.Bool     `tfsdk:"force_conflicts"`
	ApplyOnly              types.Bool     `tfsdk:"apply_only"`
	IgnoreFields           types.List     `tfsdk:"ignore_fields"`
	Wait                   types.Bool     `tfsdk:"wait"`
	WaitForRollout         types.Bool     `tfsdk:"wait_for_rollout"`
	ValidateSchema         types.Bool     `tfsdk:"validate_schema"`
	WaitFor                types.List     `tfsdk:"wait_for"`
	DeleteCascade          types.String   `tfsdk:"delete_cascade"`
	Timeouts               timeouts.Value `tfsdk:"timeouts"`
}

// waitForModel describes the wait_for block
type waitForModel struct {
	Conditions types.List `tfsdk:"condition"`
	Fields     types.List `tfsdk:"field"`
}

// waitConditionModel describes a condition in wait_for
type waitConditionModel struct {
	Type   types.String `tfsdk:"type"`
	Status types.String `tfsdk:"status"`
}

// waitFieldModel describes a field in wait_for
type waitFieldModel struct {
	Key       types.String `tfsdk:"key"`
	Value     types.String `tfsdk:"value"`
	ValueType types.String `tfsdk:"value_type"`
}

// NewManifestResource returns a new manifest resource
func NewManifestResource() resource.Resource {
	return &manifestResource{}
}

// Metadata returns the resource type name
func (r *manifestResource) Metadata(
	ctx context.Context,
	req resource.MetadataRequest,
	resp *resource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_manifest"
}

// Schema defines the resource schema
func (r *manifestResource) Schema(
	ctx context.Context,
	req resource.SchemaRequest,
	resp *resource.SchemaResponse,
) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Deploy and manage any Kubernetes resource using YAML manifests. " +
			"Handles the full lifecycle including creation, updates with drift detection, and deletion.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Kubernetes resource self-link " +
					"(format: apiVersion/kind/namespace/name or apiVersion/kind/name for cluster-scoped)",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"uid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "UID of the resource as assigned by Kubernetes at creation time",
			},
			"live_uid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Current UID of the resource in the cluster (for drift detection)",
			},
			"yaml_incluster": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Fingerprint of the resource as last seen in the cluster",
			},
			"live_manifest_incluster": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Current fingerprint of the resource in the cluster",
			},
			"api_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "API version (extracted from yaml_body)",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"kind": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource kind (extracted from yaml_body)",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource name (extracted from yaml_body)",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"namespace": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource namespace (extracted from yaml_body, empty for cluster-scoped resources)",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"override_namespace": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Override the namespace specified in yaml_body",
			},
			"yaml_body": schema.StringAttribute{
				Required:            true,
				Sensitive:           true,
				MarkdownDescription: "YAML manifest content for the Kubernetes resource",
			},
			"yaml_body_parsed": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Parsed YAML body with sensitive fields obfuscated (for display)",
			},
			"sensitive_fields": schema.ListAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "JSON paths to fields that should be obfuscated in yaml_body_parsed",
			},
			"force_new": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Force delete and recreate instead of update in-place. Default: false",
			},
			"server_side_apply": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Use server-side apply instead of client-side apply. Default: false",
			},
			"field_manager": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("kubectl"),
				MarkdownDescription: "Field manager name for server-side apply. Default: kubectl",
			},
			"force_conflicts": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Force apply even if there are field manager conflicts. Default: false",
			},
			"apply_only": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Apply only (never delete the resource). Default: false",
			},
			"ignore_fields": schema.ListAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "JSON paths to ignore when detecting drift. Useful for fields managed by controllers.",
			},
			"wait": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Wait for deletion to complete (finalizers). Default: false",
			},
			"wait_for_rollout": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Wait for Deployments/StatefulSets/DaemonSets to complete rollout. Default: true",
			},
			"validate_schema": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Validate YAML against Kubernetes OpenAPI schema. Default: true",
			},
			"delete_cascade": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Cascade mode for deletion: Background or Foreground. " +
					"Default: Background (or Foreground if wait is true)",
				Validators: []validator.String{
					stringvalidator.OneOf(
						string(meta_v1.DeletePropagationBackground),
						string(meta_v1.DeletePropagationForeground),
					),
				},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{
				Create: true,
			}),
		},
		Blocks: map[string]schema.Block{
			"wait_for": schema.ListNestedBlock{
				MarkdownDescription: "Wait for specific conditions or field values before considering operation complete",
				Validators: []validator.List{
					listvalidator.SizeAtMost(1),
				},
				NestedObject: schema.NestedBlockObject{
					Blocks: map[string]schema.Block{
						"condition": schema.ListNestedBlock{
							MarkdownDescription: "Wait for status conditions to match",
							NestedObject: schema.NestedBlockObject{
								Attributes: map[string]schema.Attribute{
									"type": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "Condition type to check",
									},
									"status": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "Expected status value (e.g., True, False)",
									},
								},
							},
						},
						"field": schema.ListNestedBlock{
							MarkdownDescription: "Wait for specific fields to match values",
							NestedObject: schema.NestedBlockObject{
								Attributes: map[string]schema.Attribute{
									"key": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "JSON path to the field",
									},
									"value": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "Expected value",
									},
									"value_type": schema.StringAttribute{
										Optional:            true,
										Computed:            true,
										Default:             stringdefault.StaticString("eq"),
										MarkdownDescription: "Value comparison type: eq (equals) or regex. Default: eq",
										Validators: []validator.String{
											stringvalidator.OneOf("eq", "regex"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// Configure sets the provider data for the resource
func (r *manifestResource) Configure(
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

// Create creates a new Kubernetes resource
func (r *manifestResource) Create(
	ctx context.Context,
	req resource.CreateRequest,
	resp *resource.CreateResponse,
) {
	var plan manifestResourceModel

	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get timeout from config
	createTimeout, diags := plan.Timeouts.Create(ctx, 10*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create context with timeout
	createCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	// Apply with retry
	retryConfig := backoff.NewExponentialBackOff()
	retryConfig.InitialInterval = 3 * time.Second
	retryConfig.MaxInterval = 30 * time.Second
	retryConfig.MaxElapsedTime = createTimeout

	retryCount := r.providerData.ApplyRetryCount
	if retryCount > 0 {
		retryConfig = backoff.WithMaxRetries(retryConfig, uint64(retryCount))
	}

	err := backoff.Retry(func() error {
		return r.applyManifest(createCtx, &plan)
	}, retryConfig)

	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Create Resource",
			fmt.Sprintf("Could not apply manifest: %s", err),
		)
		return
	}

	// Read back to get computed values
	if err := r.readManifest(createCtx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Read Resource",
			fmt.Sprintf("Could not read manifest after creation: %s", err),
		)
		return
	}

	// Set state
	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// Read reads the current state of the resource
func (r *manifestResource) Read(
	ctx context.Context,
	req resource.ReadRequest,
	resp *resource.ReadResponse,
) {
	var state manifestResourceModel

	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read from Kubernetes API
	if err := r.readManifest(ctx, &state); err != nil {
		// If resource not found, remove from state
		if isNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Failed to Read Resource",
			fmt.Sprintf("Could not read manifest: %s", err),
		)
		return
	}

	// Set state
	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

// Update updates an existing resource
func (r *manifestResource) Update(
	ctx context.Context,
	req resource.UpdateRequest,
	resp *resource.UpdateResponse,
) {
	var plan manifestResourceModel

	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get timeout
	createTimeout, diags := plan.Timeouts.Create(ctx, 10*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	// Apply with retry
	retryConfig := backoff.NewExponentialBackOff()
	retryConfig.InitialInterval = 3 * time.Second
	retryConfig.MaxInterval = 30 * time.Second
	retryConfig.MaxElapsedTime = createTimeout

	retryCount := r.providerData.ApplyRetryCount
	if retryCount > 0 {
		retryConfig = backoff.WithMaxRetries(retryConfig, uint64(retryCount))
	}

	err := backoff.Retry(func() error {
		return r.applyManifest(updateCtx, &plan)
	}, retryConfig)

	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Update Resource",
			fmt.Sprintf("Could not apply manifest: %s", err),
		)
		return
	}

	// Read back
	if err := r.readManifest(updateCtx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Read Resource",
			fmt.Sprintf("Could not read manifest after update: %s", err),
		)
		return
	}

	// Set state
	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// Delete removes the resource
func (r *manifestResource) Delete(
	ctx context.Context,
	req resource.DeleteRequest,
	resp *resource.DeleteResponse,
) {
	var state manifestResourceModel

	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if apply_only mode
	if !state.ApplyOnly.IsNull() && state.ApplyOnly.ValueBool() {
		log.Printf("[INFO] apply_only is set, skipping deletion")
		return
	}

	// Delete the resource from Kubernetes
	if err := r.deleteManifest(ctx, &state); err != nil {
		// If not found, that's ok - already deleted
		if !isNotFoundError(err) {
			resp.Diagnostics.AddError(
				"Failed to Delete Resource",
				fmt.Sprintf("Could not delete manifest: %s", err),
			)
			return
		}
	}
}

// ImportState imports an existing resource
func (r *manifestResource) ImportState(
	ctx context.Context,
	req resource.ImportStateRequest,
	resp *resource.ImportStateResponse,
) {
	// Expected format: apiVersion//kind//name//namespace
	// or apiVersion//kind//name for cluster-scoped resources
	idParts := strings.Split(req.ID, "//")
	if len(idParts) != 3 && len(idParts) != 4 {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf(
				"Expected ID in format 'apiVersion//kind//name//namespace' or "+
					"'apiVersion//kind//name' for cluster-scoped resources, got: %s",
				req.ID,
			),
		)
		return
	}

	apiVersion := idParts[0]
	kind := idParts[1]
	name := idParts[2]
	namespace := ""
	if len(idParts) == 4 {
		namespace = idParts[3]
	}

	// Build minimal YAML
	yamlBody := fmt.Sprintf(`apiVersion: %s
kind: %s
metadata:
  name: %s`, apiVersion, kind, name)

	if namespace != "" {
		yamlBody = fmt.Sprintf(`apiVersion: %s
kind: %s
metadata:
  name: %s
  namespace: %s`, apiVersion, kind, name, namespace)
	}

	// Parse to validate
	manifest, err := yaml.ParseYAML(yamlBody)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Parse Import YAML",
			fmt.Sprintf("Could not parse constructed YAML: %s", err),
		)
		return
	}

	// TODO: Implement getRestClientFromUnstructured and read from cluster
	// For now, set basic state
	model := manifestResourceModel{
		YAMLBody:        types.StringValue(yamlBody),
		APIVersion:      types.StringValue(apiVersion),
		Kind:            types.StringValue(kind),
		Name:            types.StringValue(name),
		ForceNew:        types.BoolValue(false),
		ServerSideApply: types.BoolValue(false),
		ApplyOnly:       types.BoolValue(false),
	}

	if namespace != "" {
		model.Namespace = types.StringValue(namespace)
	} else {
		model.Namespace = types.StringNull()
	}

	model.ID = types.StringValue(manifest.GetSelfLink())

	// Read from Kubernetes to populate remaining fields
	if err := r.readManifest(ctx, &model); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Import Resource",
			fmt.Sprintf("Could not read resource from Kubernetes: %s", err),
		)
		return
	}

	// Set state
	diags := resp.State.Set(ctx, model)
	resp.Diagnostics.Append(diags...)
}

// ModifyPlan handles plan modification for drift detection and force_new
func (r *manifestResource) ModifyPlan(
	ctx context.Context,
	req resource.ModifyPlanRequest,
	resp *resource.ModifyPlanResponse,
) {
	// Only modify plan during updates
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var plan, state manifestResourceModel

	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)

	diags = req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	// If force_new is set and yaml_body changed, require replace
	if !plan.ForceNew.IsNull() && plan.ForceNew.ValueBool() {
		if !plan.YAMLBody.Equal(state.YAMLBody) {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("yaml_body"))
		}
	}

	// Check if YAML body is known (not interpolated)
	if plan.YAMLBody.IsUnknown() {
		log.Printf("[TRACE] yaml_body value interpolated, setting computed fields as unknown")
		plan.YAMLBodyParsed = types.StringUnknown()
		plan.YAMLInCluster = types.StringUnknown()
		diags = resp.Plan.Set(ctx, plan)
		resp.Diagnostics.Append(diags...)
		return
	}

	// Parse YAML and set computed fields
	parsedYaml, err := yaml.ParseYAML(plan.YAMLBody.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Parse YAML",
			fmt.Sprintf("Could not parse yaml_body: %s", err),
		)
		return
	}

	// Apply namespace override if set
	if !plan.OverrideNamespace.IsNull() {
		parsedYaml.SetNamespace(plan.OverrideNamespace.ValueString())
	}

	// Set computed fields from parsed YAML
	plan.APIVersion = types.StringValue(parsedYaml.GetAPIVersion())
	plan.Kind = types.StringValue(parsedYaml.GetKind())
	plan.Name = types.StringValue(parsedYaml.GetName())

	if parsedYaml.GetNamespace() != "" {
		plan.Namespace = types.StringValue(parsedYaml.GetNamespace())
	} else {
		plan.Namespace = types.StringNull()
	}

	// Create obfuscated YAML for display
	obfuscatedYaml, err := r.obfuscateSensitiveFields(ctx, parsedYaml, plan.SensitiveFields)
	if err == nil {
		yamlStr, err := obfuscatedYaml.AsYAML()
		if err == nil {
			plan.YAMLBodyParsed = types.StringValue(yamlStr)
		}
	}

	// Detect UID drift
	if !state.UID.IsNull() && !state.LiveUID.IsNull() {
		if !state.UID.Equal(state.LiveUID) {
			log.Printf("[TRACE] DETECTED UID DRIFT %s vs %s", state.UID.ValueString(), state.LiveUID.ValueString())
			plan.UID = types.StringUnknown()
		}
	}

	// Detect manifest drift
	if !state.YAMLInCluster.IsNull() && !state.LiveManifestInCluster.IsNull() {
		if !state.YAMLInCluster.Equal(state.LiveManifestInCluster) {
			log.Printf("[TRACE] DETECTED YAML STATE DIFFERENCE")
			plan.YAMLInCluster = types.StringUnknown()
		}
	}

	// Update plan
	diags = resp.Plan.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// Helper methods (stubs - to be implemented)

func (r *manifestResource) applyManifest(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// TODO: Implement manifest apply logic
	// - Parse YAML
	// - Get REST client
	// - Apply using kubectl apply or server-side apply
	// - Wait for rollout if needed
	// - Wait for conditions if specified
	return fmt.Errorf("applyManifest not yet implemented")
}

func (r *manifestResource) readManifest(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// TODO: Implement manifest read logic
	// - Parse YAML to get resource identifiers
	// - Get REST client
	// - Read from Kubernetes API
	// - Update model with current state
	// - Generate fingerprints for drift detection
	return fmt.Errorf("readManifest not yet implemented")
}

func (r *manifestResource) deleteManifest(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// TODO: Implement manifest delete logic
	// - Parse YAML
	// - Get REST client
	// - Delete with appropriate cascade mode
	// - Wait if specified
	return fmt.Errorf("deleteManifest not yet implemented")
}

func (r *manifestResource) obfuscateSensitiveFields(
	ctx context.Context,
	manifest *yaml.Manifest,
	sensitiveFields types.List,
) (*yaml.Manifest, error) {
	// TODO: Implement sensitive field obfuscation
	// - Extract sensitive field paths
	// - Replace values with "(sensitive value)"
	return manifest, nil
}

func isNotFoundError(err error) bool {
	// TODO: Check if error is Kubernetes NotFound error
	return false
}

// Attribute types for nested blocks

var waitForAttrTypes = map[string]attr.Type{
	"condition": types.ListType{
		ElemType: types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"type":   types.StringType,
				"status": types.StringType,
			},
		},
	},
	"field": types.ListType{
		ElemType: types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"key":        types.StringType,
				"value":      types.StringType,
				"value_type": types.StringType,
			},
		},
	},
}
