package kubectl

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/alekc/terraform-provider-kubectl/kubectl/morph"
	"github.com/alekc/terraform-provider-kubectl/kubectl/payload"
	"github.com/alekc/terraform-provider-kubectl/kubectl/util"
	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/thedevsaddam/gojsonq/v2"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                   = &manifestResource{}
	_ resource.ResourceWithConfigure      = &manifestResource{}
	_ resource.ResourceWithImportState    = &manifestResource{}
	_ resource.ResourceWithModifyPlan     = &manifestResource{}
	_ resource.ResourceWithValidateConfig = &manifestResource{}
)

// manifestResource defines the resource implementation.
type manifestResource struct {
	providerData *kubectlProviderData
}

// manifestResourceModel describes the resource data model.
type manifestResourceModel struct {
	ID             types.String   `tfsdk:"id"`
	APIVersion     types.String   `tfsdk:"api_version"`
	Kind           types.String   `tfsdk:"kind"`
	Metadata       types.Dynamic  `tfsdk:"metadata"`
	Spec           types.Dynamic  `tfsdk:"spec"`
	Status         types.Dynamic  `tfsdk:"status"`
	Object         types.Dynamic  `tfsdk:"object"`
	ComputedFields types.List     `tfsdk:"computed_fields"`
	ApplyOnly      types.Bool     `tfsdk:"apply_only"`
	DeleteCascade  types.String   `tfsdk:"delete_cascade"`
	Wait           types.List     `tfsdk:"wait"`
	FieldManager   types.List     `tfsdk:"field_manager"`
	Timeouts       timeouts.Value `tfsdk:"timeouts"`
}

// waitModel describes the wait block.
type waitModel struct {
	Rollout    types.Bool `tfsdk:"rollout"`
	Fields     types.List `tfsdk:"field"`
	Conditions types.List `tfsdk:"condition"`
	ErrorOn    types.List `tfsdk:"error_on"`
}

// waitFieldModel describes a field matcher in the wait block.
type waitFieldModel struct {
	Key       types.String `tfsdk:"key"`
	Value     types.String `tfsdk:"value"`
	ValueType types.String `tfsdk:"value_type"`
}

// waitConditionModel describes a condition in the wait block.
type waitConditionModel struct {
	Type   types.String `tfsdk:"type"`
	Status types.String `tfsdk:"status"`
}

// waitErrorOnModel describes an error_on condition in the wait block.
// If a matched condition is detected, the apply fails immediately.
type waitErrorOnModel struct {
	Key     types.String `tfsdk:"key"`
	Value   types.String `tfsdk:"value"`
	Message types.String `tfsdk:"message"`
}

// fieldManagerModel describes the field_manager block.
type fieldManagerModel struct {
	Name           types.String `tfsdk:"name"`
	ForceConflicts types.Bool   `tfsdk:"force_conflicts"`
}

// NewManifestResource returns a new manifest resource.
func NewManifestResource() resource.Resource {
	return &manifestResource{}
}

// Metadata returns the resource type name.
func (r *manifestResource) Metadata(
	ctx context.Context,
	req resource.MetadataRequest,
	resp *resource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_manifest"
}

// Schema defines the resource schema.
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
				MarkdownDescription: "Kubernetes resource identifier " +
					"(format: apiVersion//kind//name//namespace or apiVersion//kind//name for cluster-scoped)",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"api_version": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Kubernetes API version (e.g., `v1`, `apps/v1`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"kind": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Kubernetes resource kind (e.g., `ConfigMap`, `Deployment`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"metadata": schema.DynamicAttribute{
				Required:            true,
				MarkdownDescription: "Standard Kubernetes object metadata containing at minimum `name` and optionally `namespace`, `labels`, `annotations`, etc.",
			},
			"spec": schema.DynamicAttribute{
				Optional:            true,
				MarkdownDescription: "Resource specification. Structure depends on the resource kind.",
			},
			"status": schema.DynamicAttribute{
				Computed:            true,
				MarkdownDescription: "Resource status as reported by the Kubernetes API server.",
			},
			"object": schema.DynamicAttribute{
				Computed:            true,
				MarkdownDescription: "The full resource object as returned by the API server.",
			},
			"computed_fields": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				MarkdownDescription: "List of manifest fields whose values may be altered by the API server during apply. " +
					"Defaults to: `[\"metadata.annotations\", \"metadata.labels\"]`",
			},
			"apply_only": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Apply only (never delete the resource). Default: false",
			},
			"delete_cascade": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Cascade mode for deletion: Background or Foreground. " +
					"Default: Background",
				Validators: []validator.String{
					stringvalidator.OneOf(
						string(meta_v1.DeletePropagationBackground),
						string(meta_v1.DeletePropagationForeground),
					),
				},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{
				Create: true,
				Update: true,
				Delete: true,
			}),
		},
		Blocks: map[string]schema.Block{
			"wait": schema.ListNestedBlock{
				MarkdownDescription: "Configure waiter options.",
				Validators: []validator.List{
					listvalidator.SizeAtMost(1),
				},
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"rollout": schema.BoolAttribute{
							Optional:            true,
							MarkdownDescription: "Wait for rollout to complete on resources that support `kubectl rollout status`.",
						},
					},
					Blocks: map[string]schema.Block{
						"field": schema.ListNestedBlock{
							MarkdownDescription: "Wait for a resource field to reach an expected value. " +
								"Multiple `field` blocks can be specified; all must match.",
							NestedObject: schema.NestedBlockObject{
								Attributes: map[string]schema.Attribute{
									"key": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "JSON path to the field to check (e.g., `status.phase`, `status.podIP`).",
									},
									"value": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "The expected value or regex pattern to match.",
									},
									"value_type": schema.StringAttribute{
										Optional:            true,
										Computed:            true,
										Default:             stringdefault.StaticString("eq"),
										MarkdownDescription: "Comparison type: `eq` for exact match (default) or `regex` for regular expression matching.",
										Validators: []validator.String{
											stringvalidator.OneOf("eq", "regex"),
										},
									},
								},
							},
						},
						"condition": schema.ListNestedBlock{
							MarkdownDescription: "Wait for status conditions to match.",
							NestedObject: schema.NestedBlockObject{
								Attributes: map[string]schema.Attribute{
									"type": schema.StringAttribute{
										Optional:            true,
										MarkdownDescription: "The type of condition.",
									},
									"status": schema.StringAttribute{
										Optional:            true,
										MarkdownDescription: "The condition status.",
									},
								},
							},
						},
						"error_on": schema.ListNestedBlock{
							MarkdownDescription: "Fail the apply immediately if any of these conditions are detected. " +
								"Use this to detect error states during waiting, such as CrashLoopBackOff or Failed status. " +
								"The `key` is a JSON path (e.g., `status.containerStatuses.0.state.waiting.reason`) and " +
								"`value` is a regex pattern matched against the field value.",
							NestedObject: schema.NestedBlockObject{
								Attributes: map[string]schema.Attribute{
									"key": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "JSON path to the field to check (e.g., `status.phase`, `status.conditions.0.reason`).",
									},
									"value": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "Regex pattern to match against the field value. If matched, the apply fails immediately.",
									},
									"message": schema.StringAttribute{
										Optional:            true,
										MarkdownDescription: "Custom error message to display when this error condition is matched.",
									},
								},
							},
						},
					},
				},
			},
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
							MarkdownDescription: "The name to use for the field manager when applying server-side. Default: Terraform",
						},
						"force_conflicts": schema.BoolAttribute{
							Optional:            true,
							Computed:            true,
							Default:             booldefault.StaticBool(false),
							MarkdownDescription: "Force changes against conflicts. Default: false",
						},
					},
				},
			},
		},
	}
}

// Configure sets the provider data for the resource.
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

// ValidateConfig validates the resource configuration.
func (r *manifestResource) ValidateConfig(
	ctx context.Context,
	req resource.ValidateConfigRequest,
	resp *resource.ValidateConfigResponse,
) {
	var config manifestResourceModel
	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Validate metadata has "name"
	if !config.Metadata.IsNull() && !config.Metadata.IsUnknown() {
		metaMap, d := dynamicToMap(ctx, config.Metadata)
		resp.Diagnostics.Append(d...)
		if !resp.Diagnostics.HasError() && metaMap != nil {
			if _, ok := metaMap["name"]; !ok {
				resp.Diagnostics.AddAttributeError(
					path.Root("metadata"),
					"Missing required field",
					"metadata must contain a 'name' field",
				)
			}
		}
	}

	// Validate wait block — only one waiter type allowed
	if !config.Wait.IsNull() && !config.Wait.IsUnknown() {
		var waitModels []waitModel
		d := config.Wait.ElementsAs(ctx, &waitModels, false)
		resp.Diagnostics.Append(d...)
		if !resp.Diagnostics.HasError() && len(waitModels) > 0 {
			w := waitModels[0]
			waiters := 0
			if !w.Rollout.IsNull() && w.Rollout.ValueBool() {
				waiters++
			}
			if !w.Fields.IsNull() && !w.Fields.IsUnknown() {
				var fields []waitFieldModel
				w.Fields.ElementsAs(ctx, &fields, false)
				if len(fields) > 0 {
					waiters++
				}
			}
			if !w.Conditions.IsNull() && !w.Conditions.IsUnknown() {
				var conditions []waitConditionModel
				w.Conditions.ElementsAs(ctx, &conditions, false)
				if len(conditions) > 0 {
					waiters++
				}
			}
			if waiters > 1 {
				resp.Diagnostics.AddAttributeError(
					path.Root("wait"),
					"Invalid wait configuration",
					"You may only set one of rollout, field, or condition in a wait block.",
				)
			}
		}
	}
}

// Create creates a new Kubernetes resource.
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
	var backoffStrategy backoff.BackOff = retryConfig
	if retryCount > 0 {
		backoffStrategy = backoff.WithMaxRetries(retryConfig, uint64(retryCount))
	}

	err := backoff.Retry(func() error {
		return r.applyManifest(createCtx, &plan)
	}, backoffStrategy)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Create Resource",
			fmt.Sprintf("Could not apply manifest: %s", err),
		)
		return
	}

	// Set state
	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// Read reads the current state of the resource.
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

// Update updates an existing resource.
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
	updateTimeout, diags := plan.Timeouts.Update(ctx, 10*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	updateCtx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	// Apply with retry
	retryConfig := backoff.NewExponentialBackOff()
	retryConfig.InitialInterval = 3 * time.Second
	retryConfig.MaxInterval = 30 * time.Second
	retryConfig.MaxElapsedTime = updateTimeout

	retryCount := r.providerData.ApplyRetryCount
	var backoffStrategy backoff.BackOff = retryConfig
	if retryCount > 0 {
		backoffStrategy = backoff.WithMaxRetries(retryConfig, uint64(retryCount))
	}

	err := backoff.Retry(func() error {
		return r.applyManifest(updateCtx, &plan)
	}, backoffStrategy)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Update Resource",
			fmt.Sprintf("Could not apply manifest: %s", err),
		)
		return
	}

	// Set state
	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// Delete removes the resource.
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

// ImportState imports an existing resource.
func (r *manifestResource) ImportState(
	ctx context.Context,
	req resource.ImportStateRequest,
	resp *resource.ImportStateResponse,
) {
	// Support both formats:
	// 1. apiVersion=<value>,kind=<value>,name=<value>[,namespace=<value>]
	// 2. apiVersion//kind//name[//namespace]
	var apiVersion, kind, name, namespace string

	if strings.Contains(req.ID, "=") {
		// ParseResourceID format (key=value pairs)
		gvk, n, ns, err := util.ParseResourceID(req.ID)
		if err != nil {
			resp.Diagnostics.AddError(
				"Invalid Import ID",
				fmt.Sprintf("Failed to parse import ID: %s", err),
			)
			return
		}
		apiVersion = gvk.GroupVersion().String()
		kind = gvk.Kind
		name = n
		namespace = ns
	} else {
		// // separated format
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
		apiVersion = idParts[0]
		kind = idParts[1]
		name = idParts[2]
		if len(idParts) == 4 {
			namespace = idParts[3]
		}
	}

	// Build metadata from import ID
	metadataMap := map[string]any{
		"name": name,
	}
	if namespace != "" {
		metadataMap["namespace"] = namespace
	}

	metadataDynamic, diags := mapToDynamic(ctx, metadataMap)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	model := manifestResourceModel{
		ID:         types.StringValue(req.ID),
		APIVersion: types.StringValue(apiVersion),
		Kind:       types.StringValue(kind),
		Metadata:   metadataDynamic,
		Spec:       types.DynamicNull(),
		Status:     types.DynamicNull(),
		Object:     types.DynamicNull(),
		ApplyOnly:  types.BoolValue(false),
	}

	// Attempt OpenAPI-aware import if we have provider data
	if r.providerData != nil {
		gvk := k8sschema.FromAPIVersionAndKind(apiVersion, kind)
		objectType, typeHints, err := r.providerData.TFTypeFromOpenAPI(ctx, gvk, false)
		if err == nil && objectType != nil {
			if importErr := r.readManifestWithOpenAPI(ctx, &model, objectType, typeHints); importErr != nil {
				resp.Diagnostics.AddError(
					"Failed to Import Resource",
					fmt.Sprintf("Could not read resource from Kubernetes: %s", importErr),
				)
				return
			}

			resp.Diagnostics.AddWarning(
				"Apply needed after 'import'",
				"Please run apply after a successful import to realign the resource state to the configuration in Terraform.",
			)

			diags = resp.State.Set(ctx, model)
			resp.Diagnostics.Append(diags...)
			return
		}
		// Fall through to basic read if OpenAPI resolution fails
		log.Printf("[WARN] Could not resolve OpenAPI type during import: %v", err)
	}

	// Fall back to basic read
	if err := r.readManifestV2(ctx, &model); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Import Resource",
			fmt.Sprintf("Could not read resource from Kubernetes: %s", err),
		)
		return
	}

	resp.Diagnostics.AddWarning(
		"Apply needed after 'import'",
		"Please run apply after a successful import to realign the resource state to the configuration in Terraform.",
	)

	// Set state
	diags = resp.State.Set(ctx, model)
	resp.Diagnostics.Append(diags...)
}

// ModifyPlan handles plan modification with OpenAPI type resolution.
func (r *manifestResource) ModifyPlan(
	ctx context.Context,
	req resource.ModifyPlanRequest,
	resp *resource.ModifyPlanResponse,
) {
	// Only modify plan during updates (not create or destroy)
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

	// Check if metadata.name or metadata.namespace changed — requires replacement
	planName, _ := extractMetadataField(ctx, plan.Metadata, "name")
	stateName, _ := extractMetadataField(ctx, state.Metadata, "name")
	planNamespace, _ := extractMetadataField(ctx, plan.Metadata, "namespace")
	stateNamespace, _ := extractMetadataField(ctx, state.Metadata, "namespace")

	if planName != stateName {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("metadata"))
	}
	if planNamespace != stateNamespace {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("metadata"))
	}

	// Attempt OpenAPI type resolution for computed field handling
	if r.providerData != nil && !plan.APIVersion.IsUnknown() && !plan.Kind.IsUnknown() {
		r.modifyPlanWithOpenAPI(ctx, &plan, &state, resp)
		if resp.Diagnostics.HasError() {
			return
		}
	} else {
		// Without OpenAPI, just mark computed fields as unknown
		plan.Status = types.DynamicUnknown()
		plan.Object = types.DynamicUnknown()
	}

	diags = resp.Plan.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// modifyPlanWithOpenAPI uses OpenAPI spec to resolve the resource type and
// handle computed fields during plan modification.
func (r *manifestResource) modifyPlanWithOpenAPI(
	ctx context.Context,
	plan *manifestResourceModel,
	state *manifestResourceModel,
	resp *resource.ModifyPlanResponse,
) {
	apiVersion := plan.APIVersion.ValueString()
	kind := plan.Kind.ValueString()
	gvk := k8sschema.FromAPIVersionAndKind(apiVersion, kind)

	// Try to resolve OpenAPI type
	objectType, _, err := r.providerData.TFTypeFromOpenAPI(ctx, gvk, false)
	if err != nil {
		// If we can't resolve the type, continue without OpenAPI enhancement
		log.Printf("[DEBUG] Could not resolve OpenAPI type for %s: %v", gvk.String(), err)
		plan.Status = types.DynamicUnknown()
		plan.Object = types.DynamicUnknown()
		return
	}

	if !objectType.Is(tftypes.Object{}) {
		// Non-structural CRD — no schema available
		log.Printf("[DEBUG] Non-structural type for %s, skipping OpenAPI plan modification", gvk.String())
		plan.Status = types.DynamicUnknown()
		plan.Object = types.DynamicUnknown()
		return
	}

	// Build manifest map from plan attributes for morphing
	manifestMap := map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
	}

	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() {
		metaMap, d := dynamicToMap(ctx, plan.Metadata)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		if metaMap != nil {
			manifestMap["metadata"] = metaMap
		}
	}

	if !plan.Spec.IsNull() && !plan.Spec.IsUnknown() {
		specMap, d := dynamicToMap(ctx, plan.Spec)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		if specMap != nil {
			manifestMap["spec"] = specMap
		}
	}

	// Convert to tftypes.Value using OpenAPI type
	planTfValue, err := payload.ToTFValue(
		manifestMap,
		objectType,
		nil,
		tftypes.NewAttributePath(),
	)
	if err != nil {
		log.Printf("[DEBUG] Could not convert plan to tftypes.Value: %v", err)
		plan.Status = types.DynamicUnknown()
		plan.Object = types.DynamicUnknown()
		return
	}

	// Apply morph.ValueToType to coerce to OpenAPI type
	morphedPlan, d := morph.ValueToType(planTfValue, objectType, tftypes.NewAttributePath())
	if len(d) > 0 {
		log.Printf("[DEBUG] Could not morph plan to OpenAPI type: %v", d)
		plan.Status = types.DynamicUnknown()
		plan.Object = types.DynamicUnknown()
		return
	}

	// Apply morph.DeepUnknown to mark unspecified fields as unknown
	completePlan, err := morph.DeepUnknown(objectType, morphedPlan, tftypes.NewAttributePath())
	if err != nil {
		log.Printf("[DEBUG] Could not apply DeepUnknown: %v", err)
		plan.Status = types.DynamicUnknown()
		plan.Object = types.DynamicUnknown()
		return
	}

	// Handle computed_fields: mark specified paths as unknown
	computedFields := make(map[string]*tftypes.AttributePath)
	if !plan.ComputedFields.IsNull() && !plan.ComputedFields.IsUnknown() {
		var cfList []string
		plan.ComputedFields.ElementsAs(ctx, &cfList, false)
		for _, cf := range cfList {
			atp, err := FieldPathToTftypesPath(cf)
			if err != nil {
				log.Printf("[DEBUG] Could not parse computed_fields path %s: %v", cf, err)
				continue
			}
			computedFields[atp.String()] = atp
		}
	}
	if len(computedFields) == 0 {
		// Default computed fields
		atp := tftypes.NewAttributePath().WithAttributeName("metadata").WithAttributeName("annotations")
		computedFields[atp.String()] = atp
		atp = tftypes.NewAttributePath().WithAttributeName("metadata").WithAttributeName("labels")
		computedFields[atp.String()] = atp
	}

	// Transform to mark computed_fields as unknown
	completePlan, err = tftypes.Transform(
		completePlan,
		func(ap *tftypes.AttributePath, v tftypes.Value) (tftypes.Value, error) {
			if _, ok := computedFields[ap.String()]; ok {
				return tftypes.NewValue(v.Type(), tftypes.UnknownValue), nil
			}
			return v, nil
		},
	)
	if err != nil {
		log.Printf("[DEBUG] Could not mark computed fields: %v", err)
		plan.Status = types.DynamicUnknown()
		plan.Object = types.DynamicUnknown()
		return
	}

	// Convert back to map[string]any for framework
	resultMap, err := payload.FromTFValue(completePlan, nil, tftypes.NewAttributePath())
	if err != nil {
		log.Printf("[DEBUG] Could not convert morphed plan back to map: %v", err)
		plan.Status = types.DynamicUnknown()
		plan.Object = types.DynamicUnknown()
		return
	}

	resultContent, ok := resultMap.(map[string]any)
	if !ok {
		plan.Status = types.DynamicUnknown()
		plan.Object = types.DynamicUnknown()
		return
	}

	// Update plan metadata from morphed result
	if meta, ok := resultContent["metadata"].(map[string]any); ok {
		metaDynamic, d := mapToDynamic(ctx, meta)
		resp.Diagnostics.Append(d...)
		if !resp.Diagnostics.HasError() {
			plan.Metadata = metaDynamic
		}
	}

	// Update plan spec from morphed result
	if spec, ok := resultContent["spec"].(map[string]any); ok {
		specDynamic, d := mapToDynamic(ctx, spec)
		resp.Diagnostics.Append(d...)
		if !resp.Diagnostics.HasError() {
			plan.Spec = specDynamic
		}
	}

	// Mark status and object as unknown (computed)
	plan.Status = types.DynamicUnknown()
	plan.Object = types.DynamicUnknown()
}

// applyManifest applies the manifest to Kubernetes using server-side apply,
// then handles wait conditions including error_on.
func (r *manifestResource) applyManifest(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// Delegate to applyManifestV2 for the actual apply
	if err := r.applyManifestV2(ctx, model); err != nil {
		return err
	}

	// Read back to populate state from server response
	if err := r.readManifestV2(ctx, model); err != nil {
		return fmt.Errorf("failed to read manifest after apply: %w", err)
	}

	// Handle wait block if specified
	if model.Wait.IsNull() || model.Wait.IsUnknown() {
		return nil
	}

	var waitModels []waitModel
	diags := model.Wait.ElementsAs(ctx, &waitModels, false)
	if diags.HasError() || len(waitModels) == 0 {
		return nil
	}

	wait := waitModels[0]

	createTimeout, d := model.Timeouts.Create(ctx, 10*time.Minute)
	if d.HasError() {
		return fmt.Errorf("failed to get create timeout: %v", d)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	// Get name/namespace for log messages
	name, _ := extractMetadataField(ctx, model.Metadata, "name")
	namespace, _ := extractMetadataField(ctx, model.Metadata, "namespace")
	kind := model.Kind.ValueString()
	apiVersion := model.APIVersion.ValueString()

	// Check error_on conditions first during wait
	var errorOnConditions []waitErrorOnModel
	if !wait.ErrorOn.IsNull() {
		wait.ErrorOn.ElementsAs(ctx, &errorOnConditions, false)
	}

	// Get dynamic client resource interface for waiters
	getResourceInterface := func() (dynamic.ResourceInterface, error) {
		gvk := k8sschema.FromAPIVersionAndKind(apiVersion, kind)
		rm, err := r.providerData.getRestMapper()
		if err != nil {
			return nil, fmt.Errorf("failed to get REST mapper: %w", err)
		}
		client, err := r.providerData.getDynamicClient()
		if err != nil {
			return nil, fmt.Errorf("failed to get dynamic client: %w", err)
		}
		rmapping, err := rm.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return nil, fmt.Errorf("failed to get REST mapping: %w", err)
		}
		rcl := client.Resource(rmapping.Resource)
		if namespace != "" {
			return rcl.Namespace(namespace), nil
		}
		return rcl, nil
	}

	// Handle rollout wait using RolloutWaiter (polymorphichelpers)
	if !wait.Rollout.IsNull() && wait.Rollout.ValueBool() {
		log.Printf("[INFO] Waiting for rollout of %s/%s", kind, name)

		rs, err := getResourceInterface()
		if err != nil {
			return fmt.Errorf("failed to set up rollout wait: %w", err)
		}

		waiter := &RolloutWaiter{
			resource:     rs,
			resourceName: name,
			logger:       r.providerData.logger,
		}

		if len(errorOnConditions) > 0 {
			if err := r.waitWithErrorCheck(timeoutCtx, rs, waiter, name, errorOnConditions); err != nil {
				return fmt.Errorf("failed to wait for rollout: %w", err)
			}
		} else {
			if err := waiter.Wait(timeoutCtx); err != nil {
				return fmt.Errorf("failed to wait for rollout: %w", err)
			}
		}

		log.Printf("[INFO] Rollout complete for %s/%s", kind, name)
	}

	// Handle condition and field waits
	var conditions []waitConditionModel
	if !wait.Conditions.IsNull() {
		wait.Conditions.ElementsAs(ctx, &conditions, false)
	}

	var waitFields []waitFieldModel
	if !wait.Fields.IsNull() {
		wait.Fields.ElementsAs(ctx, &waitFields, false)
	}

	if len(conditions) > 0 || len(waitFields) > 0 {
		log.Printf("[INFO] Waiting for conditions/fields on %s/%s", kind, name)

		rs, err := getResourceInterface()
		if err != nil {
			return fmt.Errorf("failed to set up condition/field wait: %w", err)
		}

		// Get OpenAPI type for field wait
		var objectType tftypes.Type
		var typeHints map[string]string
		if len(waitFields) > 0 {
			gvk := k8sschema.FromAPIVersionAndKind(apiVersion, kind)
			objectType, typeHints, err = r.providerData.TFTypeFromOpenAPI(ctx, gvk, true)
			if err != nil {
				log.Printf("[WARN] Could not resolve OpenAPI type for field wait: %v", err)
				objectType = tftypes.DynamicPseudoType
			}
		}

		// Build field matchers
		var fieldMatchers []FieldMatcher
		for _, f := range waitFields {
			valueType := "eq"
			if !f.ValueType.IsNull() && f.ValueType.ValueString() != "" {
				valueType = f.ValueType.ValueString()
			}

			p, pErr := FieldPathToTftypesPath(f.Key.ValueString())
			if pErr != nil {
				return fmt.Errorf("invalid field path %q: %w", f.Key.ValueString(), pErr)
			}

			var re *regexp.Regexp
			if valueType == "regex" {
				re, err = regexp.Compile(f.Value.ValueString())
				if err != nil {
					return fmt.Errorf("invalid regex %q: %w", f.Value.ValueString(), err)
				}
			} else {
				// For eq, create a regex that matches exactly
				re = regexp.MustCompile("^" + regexp.QuoteMeta(f.Value.ValueString()) + "$")
			}

			fieldMatchers = append(fieldMatchers, FieldMatcher{
				Path:         p,
				ValueMatcher: re,
			})
		}

		// Build condition matchers
		var conditionMatchers []ConditionMatcher
		for _, c := range conditions {
			conditionMatchers = append(conditionMatchers, ConditionMatcher{
				Type:   c.Type.ValueString(),
				Status: c.Status.ValueString(),
			})
		}

		// Create waiter
		waiter := NewResourceWaiterFromConfig(
			rs,
			name,
			objectType,
			typeHints,
			false, // rollout already handled above
			fieldMatchers,
			conditionMatchers,
			r.providerData.logger,
		)

		// Run waiter with error_on checking if configured
		if len(errorOnConditions) > 0 {
			err = r.waitWithErrorCheck(timeoutCtx, rs, waiter, name, errorOnConditions)
		} else {
			err = waiter.Wait(timeoutCtx)
		}

		if err != nil {
			return fmt.Errorf("failed to wait for conditions: %w", err)
		}

		log.Printf("[INFO] Conditions/fields met for %s/%s", kind, name)
	} else if len(errorOnConditions) > 0 {
		// error_on conditions without any other waiter — check once
		rs, err := getResourceInterface()
		if err != nil {
			return fmt.Errorf("failed to set up error_on check: %w", err)
		}

		res, err := rs.Get(ctx, name, meta_v1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get resource for error_on check: %w", err)
		}

		yamlJSON, err := res.MarshalJSON()
		if err != nil {
			return fmt.Errorf("failed to marshal resource for error_on check: %w", err)
		}

		for _, e := range errorOnConditions {
			key := e.Key.ValueString()
			value := e.Value.ValueString()

			v := gojsonq.New().FromString(string(yamlJSON)).Find(key)
			if v == nil {
				continue
			}

			stringVal := fmt.Sprintf("%v", v)
			matched, matchErr := regexp.MatchString(value, stringVal)
			if matchErr != nil {
				return fmt.Errorf("error_on: invalid regex %q: %w", value, matchErr)
			}

			if matched {
				msg := ""
				if !e.Message.IsNull() {
					msg = e.Message.ValueString()
				}
				if msg != "" {
					return fmt.Errorf(
						"error condition met for %s: %s (key=%s, value=%s matched pattern %s)",
						name, msg, key, stringVal, value,
					)
				}
				return fmt.Errorf(
					"error condition met for %s: key=%s value=%s matched pattern %s",
					name, key, stringVal, value,
				)
			}
		}
	}

	return nil
}

// waitWithErrorCheck runs the waiter while also checking for error_on conditions.
func (r *manifestResource) waitWithErrorCheck(
	ctx context.Context,
	rs dynamic.ResourceInterface,
	waiter Waiter,
	name string,
	errorOnConditions []waitErrorOnModel,
) error {
	errCh := make(chan error, 1)

	// Run the waiter in a goroutine
	go func() {
		errCh <- waiter.Wait(ctx)
	}()

	// Check error_on conditions periodically while waiter runs
	ticker := time.NewTicker(waiterSleepTime)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			return err
		case <-ticker.C:
			// Check error conditions
			res, err := rs.Get(ctx, name, meta_v1.GetOptions{})
			if err != nil {
				continue
			}

			yamlJSON, err := res.MarshalJSON()
			if err != nil {
				continue
			}

			for _, e := range errorOnConditions {
				key := e.Key.ValueString()
				value := e.Value.ValueString()

				v := gojsonq.New().FromString(string(yamlJSON)).Find(key)
				if v == nil {
					continue
				}

				stringVal := fmt.Sprintf("%v", v)
				matched, matchErr := regexp.MatchString(value, stringVal)
				if matchErr != nil {
					return fmt.Errorf("error_on: invalid regex %q: %w", value, matchErr)
				}

				if matched {
					msg := ""
					if !e.Message.IsNull() {
						msg = e.Message.ValueString()
					}
					if msg != "" {
						return fmt.Errorf(
							"error condition met for %s: %s (key=%s, value=%s matched pattern %s)",
							name, msg, key, stringVal, value,
						)
					}
					return fmt.Errorf(
						"error condition met for %s: key=%s value=%s matched pattern %s",
						name, key, stringVal, value,
					)
				}
			}
		case <-ctx.Done():
			return fmt.Errorf("%s timed out waiting for resource", name)
		}
	}
}

// readManifest reads a resource from Kubernetes and populates model state.
func (r *manifestResource) readManifest(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	return r.readManifestV2(ctx, model)
}

// readManifestWithOpenAPI reads a resource using OpenAPI type resolution for better type fidelity.
func (r *manifestResource) readManifestWithOpenAPI(
	ctx context.Context,
	model *manifestResourceModel,
	objectType tftypes.Type,
	typeHints map[string]string,
) error {
	// Extract name and namespace from metadata
	name, err := extractMetadataField(ctx, model.Metadata, "name")
	if err != nil || name == "" {
		return fmt.Errorf("failed to extract name from metadata: %w", err)
	}
	namespace, _ := extractMetadataField(ctx, model.Metadata, "namespace")

	// Get GVK
	apiVersion := model.APIVersion.ValueString()
	kind := model.Kind.ValueString()
	gvk := k8sschema.FromAPIVersionAndKind(apiVersion, kind)

	// Get REST mapper and dynamic client
	rm, err := r.providerData.getRestMapper()
	if err != nil {
		return fmt.Errorf("failed to get REST mapper: %w", err)
	}
	client, err := r.providerData.getDynamicClient()
	if err != nil {
		return fmt.Errorf("failed to get dynamic client: %w", err)
	}

	// Get GVR from GVK
	rmapping, err := rm.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to get REST mapping: %w", err)
	}
	gvr := rmapping.Resource

	// Determine if namespaced
	ns, err := IsResourceNamespaced(gvk, rm)
	if err != nil {
		return fmt.Errorf("failed to check if resource is namespaced: %w", err)
	}

	// Get resource from API
	var result *meta_v1_unstruct.Unstructured
	rcl := client.Resource(gvr)
	if ns && namespace != "" {
		result, err = rcl.Namespace(namespace).Get(ctx, name, meta_v1.GetOptions{})
	} else {
		result, err = rcl.Get(ctx, name, meta_v1.GetOptions{})
	}
	if err != nil {
		return err
	}

	// Remove server-side fields
	content := RemoveServerSideFields(result.UnstructuredContent())

	// Convert using OpenAPI type
	tfValue, err := payload.ToTFValue(content, objectType, typeHints, tftypes.NewAttributePath())
	if err != nil {
		// Fall back to basic read
		return r.readManifestV2(ctx, model)
	}

	// Apply morph.DeepUnknown and then UnknownToNull
	tfValue, err = morph.DeepUnknown(objectType, tfValue, tftypes.NewAttributePath())
	if err != nil {
		return r.readManifestV2(ctx, model)
	}
	tfValue = morph.UnknownToNull(tfValue)

	// Convert back to map and set state
	resultMap, err := payload.FromTFValue(tfValue, nil, tftypes.NewAttributePath())
	if err != nil {
		return r.readManifestV2(ctx, model)
	}

	resultContent, ok := resultMap.(map[string]any)
	if !ok {
		return r.readManifestV2(ctx, model)
	}

	// Use setStateFromUnstructured but with the cleaned content
	return r.setStateFromOpenAPIResult(ctx, resultContent, result, model)
}

// setStateFromOpenAPIResult populates the model from an OpenAPI-typed result map.
func (r *manifestResource) setStateFromOpenAPIResult(
	ctx context.Context,
	content map[string]any,
	rawObj *meta_v1_unstruct.Unstructured,
	model *manifestResourceModel,
) error {
	model.APIVersion = types.StringValue(rawObj.GetAPIVersion())
	model.Kind = types.StringValue(rawObj.GetKind())

	// Set ID
	namespace := rawObj.GetNamespace()
	name := rawObj.GetName()
	if namespace != "" {
		model.ID = types.StringValue(fmt.Sprintf(
			"%s//%s//%s//%s",
			rawObj.GetAPIVersion(), rawObj.GetKind(), name, namespace,
		))
	} else {
		model.ID = types.StringValue(fmt.Sprintf(
			"%s//%s//%s",
			rawObj.GetAPIVersion(), rawObj.GetKind(), name,
		))
	}

	var diags diag.Diagnostics

	// Convert metadata from cleaned content
	if meta, ok := content["metadata"].(map[string]any); ok {
		metaDynamic, d := mapToDynamic(ctx, meta)
		diags.Append(d...)
		if !diags.HasError() {
			model.Metadata = metaDynamic
		}
	}

	// Convert spec from cleaned content
	if spec, ok := content["spec"].(map[string]any); ok {
		specDynamic, d := mapToDynamic(ctx, spec)
		diags.Append(d...)
		if !diags.HasError() {
			model.Spec = specDynamic
		}
	} else {
		model.Spec = types.DynamicNull()
	}

	// Status from raw object (server-side fields were removed from content)
	rawContent := rawObj.UnstructuredContent()
	if status, ok := rawContent["status"].(map[string]any); ok {
		statusDynamic, d := mapToDynamic(ctx, status)
		diags.Append(d...)
		if !diags.HasError() {
			model.Status = statusDynamic
		}
	} else {
		model.Status = types.DynamicNull()
	}

	// Full object from raw
	objectDynamic, d := mapToDynamic(ctx, rawContent)
	diags.Append(d...)
	if !diags.HasError() {
		model.Object = objectDynamic
	}

	if diags.HasError() {
		return fmt.Errorf("failed to set state: %v", diags)
	}

	return nil
}

// deleteManifest deletes a Kubernetes resource.
func (r *manifestResource) deleteManifest(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// Delegate to deleteManifestV2 for the actual delete
	if err := r.deleteManifestV2(ctx, model); err != nil {
		return err
	}

	// Wait for deletion to complete
	name, _ := extractMetadataField(ctx, model.Metadata, "name")
	namespace, _ := extractMetadataField(ctx, model.Metadata, "namespace")
	apiVersion := model.APIVersion.ValueString()
	kind := model.Kind.ValueString()

	deleteTimeout, d := model.Timeouts.Delete(ctx, 5*time.Minute)
	if d.HasError() {
		log.Printf("[WARN] Could not parse delete timeout, using 5 minute default")
		deleteTimeout = 5 * time.Minute
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()

	// Use dynamic client for deletion polling
	gvk := k8sschema.FromAPIVersionAndKind(apiVersion, kind)
	rm, err := r.providerData.getRestMapper()
	if err != nil {
		log.Printf("[WARN] Cannot poll deletion status: %v", err)
		return nil
	}
	client, err := r.providerData.getDynamicClient()
	if err != nil {
		log.Printf("[WARN] Cannot poll deletion status: %v", err)
		return nil
	}
	rmapping, err := rm.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		log.Printf("[WARN] Cannot poll deletion status: %v", err)
		return nil
	}

	var rs dynamic.ResourceInterface
	rcl := client.Resource(rmapping.Resource)
	if namespace != "" {
		rs = rcl.Namespace(namespace)
	} else {
		rs = rcl
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			log.Printf("[WARN] Timeout waiting for deletion, but delete was initiated")
			return nil
		case <-ticker.C:
			_, err := rs.Get(ctx, name, meta_v1.GetOptions{})
			if isNotFoundError(err) {
				log.Printf("[INFO] Confirmed deleted: %s/%s", kind, name)
				return nil
			}
			if err != nil {
				log.Printf("[DEBUG] Error checking deletion status: %v", err)
			}
		}
	}
}

func isNotFoundError(err error) bool {
	return util.IsNotFoundError(err)
}
