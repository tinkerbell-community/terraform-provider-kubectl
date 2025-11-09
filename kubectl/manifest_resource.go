package kubectl

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alekc/terraform-provider-kubectl/flatten"
	"github.com/alekc/terraform-provider-kubectl/kubectl/util"
	"github.com/alekc/terraform-provider-kubectl/yaml"
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
	"github.com/thedevsaddam/gojsonq/v2"
	apps_v1 "k8s.io/api/apps/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &manifestResource{}
	_ resource.ResourceWithConfigure   = &manifestResource{}
	_ resource.ResourceWithImportState = &manifestResource{}
	_ resource.ResourceWithModifyPlan  = &manifestResource{}
)

// manifestResource defines the resource implementation.
type manifestResource struct {
	providerData *kubectlProviderData
}

// manifestResourceModel describes the resource data model.
type manifestResourceModel struct {
	ID                    types.String   `tfsdk:"id"`
	UID                   types.String   `tfsdk:"uid"`
	LiveUID               types.String   `tfsdk:"live_uid"`
	YAMLInCluster         types.String   `tfsdk:"yaml_incluster"`
	LiveManifestInCluster types.String   `tfsdk:"live_manifest_incluster"`
	APIVersion            types.String   `tfsdk:"api_version"`
	Kind                  types.String   `tfsdk:"kind"`
	Name                  types.String   `tfsdk:"name"`
	Namespace             types.String   `tfsdk:"namespace"`
	OverrideNamespace     types.String   `tfsdk:"override_namespace"`
	YAMLBody              types.String   `tfsdk:"yaml_body"`
	YAMLBodyParsed        types.String   `tfsdk:"yaml_body_parsed"`
	SensitiveFields       types.List     `tfsdk:"sensitive_fields"`
	ForceNew              types.Bool     `tfsdk:"force_new"`
	ServerSideApply       types.Bool     `tfsdk:"server_side_apply"`
	FieldManager          types.String   `tfsdk:"field_manager"`
	ForceConflicts        types.Bool     `tfsdk:"force_conflicts"`
	ApplyOnly             types.Bool     `tfsdk:"apply_only"`
	IgnoreFields          types.List     `tfsdk:"ignore_fields"`
	Wait                  types.Bool     `tfsdk:"wait"`
	WaitForRollout        types.Bool     `tfsdk:"wait_for_rollout"`
	ValidateSchema        types.Bool     `tfsdk:"validate_schema"`
	WaitFor               types.List     `tfsdk:"wait_for"`
	DeleteCascade         types.String   `tfsdk:"delete_cascade"`
	Timeouts              timeouts.Value `tfsdk:"timeouts"`
}

// waitForModel describes the wait_for block.
type waitForModel struct {
	Conditions types.List `tfsdk:"condition"`
	Fields     types.List `tfsdk:"field"`
}

// waitConditionModel describes a condition in wait_for.
type waitConditionModel struct {
	Type   types.String `tfsdk:"type"`
	Status types.String `tfsdk:"status"`
}

// waitFieldModel describes a field in wait_for.
type waitFieldModel struct {
	Key       types.String `tfsdk:"key"`
	Value     types.String `tfsdk:"value"`
	ValueType types.String `tfsdk:"value_type"`
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

// ModifyPlan handles plan modification for drift detection and force_new.
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
			log.Printf(
				"[TRACE] DETECTED UID DRIFT %s vs %s",
				state.UID.ValueString(),
				state.LiveUID.ValueString(),
			)
			plan.UID = types.StringUnknown()
		}
	}

	// Enhanced cluster read for better drift detection
	// Read from cluster to compare critical fields that might require replacement
	restClient := util.GetRestClientFromUnstructured(
		ctx,
		parsedYaml,
		r.providerData.MainClientset,
		r.providerData.RestConfig,
	)
	if restClient.Error == nil {
		// Try to read current state from cluster
		liveResource, err := restClient.ResourceInterface.Get(
			ctx,
			parsedYaml.GetName(),
			meta_v1.GetOptions{},
		)
		if err == nil {
			// Check if immutable fields changed (like some labels, annotations patterns)
			// For now, we primarily rely on force_new flag and UID drift
			log.Printf(
				"[TRACE] Successfully read live resource %s for plan comparison",
				parsedYaml.GetSelfLink(),
			)

			// Store live UID for comparison
			if liveUID := string(liveResource.GetUID()); liveUID != "" {
				if !state.UID.IsNull() && state.UID.ValueString() != liveUID {
					log.Printf("[TRACE] Resource UID mismatch detected, may require replacement")
					resp.RequiresReplace = append(resp.RequiresReplace, path.Root("yaml_body"))
				}
			}
		} else if !util.IsNotFoundError(err) {
			log.Printf("[DEBUG] Could not read live resource during plan: %v", err)
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
	yamlBody := model.YAMLBody.ValueString()

	// Parse YAML into manifest
	manifest, err := yaml.ParseYAML(yamlBody)
	if err != nil {
		return fmt.Errorf("failed to parse kubernetes resource: %w", err)
	}

	// Apply namespace override if provided
	if !model.OverrideNamespace.IsNull() {
		manifest.SetNamespace(model.OverrideNamespace.ValueString())
	}

	log.Printf("[DEBUG] %v apply kubernetes resource:\n%s", manifest, yamlBody)

	// Create REST client for this resource type
	restClient := util.GetRestClientFromUnstructured(
		ctx,
		manifest,
		r.providerData.MainClientset,
		r.providerData.RestConfig,
	)
	if restClient.Error != nil {
		return fmt.Errorf(
			"%v failed to create kubernetes rest client for resource: %w",
			manifest,
			restClient.Error,
		)
	}

	// Convert manifest back to YAML (in case namespace was overridden)
	yamlBody, err = manifest.AsYAML()
	if err != nil {
		return fmt.Errorf("%v failed to convert to yaml: %w", manifest, err)
	}

	// Create temp file for kubectl apply
	tmpfile, err := os.CreateTemp("", "*kubectl_manifest.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(yamlBody)); err != nil {
		tmpfile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmpfile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Create apply options
	applyOptions := util.NewApplyOptions(yamlBody, r.providerData.RestConfig)

	// Configure apply options
	validateSchema := true
	if !model.ValidateSchema.IsNull() {
		validateSchema = model.ValidateSchema.ValueBool()
	}

	serverSideApply := false
	if !model.ServerSideApply.IsNull() {
		serverSideApply = model.ServerSideApply.ValueBool()
	}

	fieldManager := "kubectl"
	if !model.FieldManager.IsNull() {
		fieldManager = model.FieldManager.ValueString()
	}

	forceConflicts := false
	if !model.ForceConflicts.IsNull() {
		forceConflicts = model.ForceConflicts.ValueBool()
	}

	util.ConfigureApplyOptions(
		applyOptions,
		manifest,
		tmpfile.Name(),
		validateSchema,
		serverSideApply,
		fieldManager,
		forceConflicts,
	)

	log.Printf("[INFO] %s perform apply of manifest", manifest)

	// Run apply
	if err := applyOptions.Run(); err != nil {
		return fmt.Errorf("%v failed to run apply: %w", manifest, err)
	}

	log.Printf("[INFO] %v manifest applied, fetch resource from kubernetes", manifest)

	// Get the resource from Kubernetes
	rawResponse, err := restClient.ResourceInterface.Get(
		ctx,
		manifest.GetName(),
		meta_v1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("%v failed to fetch resource from kubernetes: %w", manifest, err)
	}

	// Set response wrapper
	response := yaml.NewFromUnstructured(rawResponse)

	// Generate ID (apiVersion//kind//namespace//name or apiVersion//kind//name)
	if response.HasNamespace() {
		model.ID = types.StringValue(fmt.Sprintf(
			"%s//%s//%s//%s",
			response.GetAPIVersion(),
			response.GetKind(),
			response.GetNamespace(),
			response.GetName(),
		))
	} else {
		model.ID = types.StringValue(fmt.Sprintf(
			"%s//%s//%s",
			response.GetAPIVersion(),
			response.GetKind(),
			response.GetName(),
		))
	}

	// Set computed values
	model.APIVersion = types.StringValue(response.GetAPIVersion())
	model.Kind = types.StringValue(response.GetKind())
	model.Name = types.StringValue(response.GetName())

	if response.HasNamespace() {
		model.Namespace = types.StringValue(response.GetNamespace())
	} else {
		model.Namespace = types.StringNull()
	}

	model.UID = types.StringValue(string(response.GetUID()))
	model.LiveUID = types.StringValue(string(response.GetUID()))

	log.Printf("[DEBUG] %v fetched successfully, set id to: %v", manifest, model.ID.ValueString())

	// Handle wait_for_rollout if specified
	if !model.WaitForRollout.IsNull() && model.WaitForRollout.ValueBool() {
		createTimeout, diags := model.Timeouts.Create(ctx, 10*time.Minute)
		if diags.HasError() {
			return fmt.Errorf("failed to get create timeout: %v", diags)
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, createTimeout)
		defer cancel()

		log.Printf("[INFO] %v waiting for rollout", manifest)

		switch manifest.GetKind() {
		case "Deployment":
			err = r.waitForDeployment(
				timeoutCtx,
				manifest.GetNamespace(),
				manifest.GetName(),
				createTimeout,
			)
		case "StatefulSet":
			err = r.waitForStatefulSet(
				timeoutCtx,
				manifest.GetNamespace(),
				manifest.GetName(),
				createTimeout,
			)
		case "DaemonSet":
			err = r.waitForDaemonSet(
				timeoutCtx,
				manifest.GetNamespace(),
				manifest.GetName(),
				createTimeout,
			)
		default:
			log.Printf("[WARN] wait_for_rollout not supported for kind %s", manifest.GetKind())
		}

		if err != nil {
			return fmt.Errorf("%v failed to wait for rollout: %w", manifest, err)
		}

		log.Printf("[INFO] %v rollout complete", manifest)
	}

	// Handle wait_for conditions if specified
	if !model.WaitFor.IsNull() && !model.WaitFor.IsUnknown() {
		var waitForList []waitForModel
		diags := model.WaitFor.ElementsAs(ctx, &waitForList, false)
		if diags.HasError() || len(waitForList) == 0 {
			return fmt.Errorf("failed to parse wait_for block")
		}

		waitFor := waitForList[0]

		// Extract conditions and fields
		var conditions []waitConditionModel
		var fields []waitFieldModel
		if !waitFor.Conditions.IsNull() {
			waitFor.Conditions.ElementsAs(ctx, &conditions, false)
		}
		if !waitFor.Fields.IsNull() {
			waitFor.Fields.ElementsAs(ctx, &fields, false)
		}

		if len(conditions) == 0 && len(fields) == 0 {
			return fmt.Errorf("wait_for block requires at least one condition or field")
		}

		createTimeout, diags := model.Timeouts.Create(ctx, 10*time.Minute)
		if diags.HasError() {
			return fmt.Errorf("failed to get create timeout: %v", diags)
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, createTimeout)
		defer cancel()

		log.Printf("[INFO] %v waiting for conditions", manifest)

		err = r.waitForConditions(
			timeoutCtx,
			restClient,
			conditions,
			fields,
			manifest.GetName(),
			createTimeout,
		)
		if err != nil {
			return fmt.Errorf("%v failed to wait for conditions: %w", manifest, err)
		}

		log.Printf("[INFO] %v conditions met", manifest)
	}

	return nil
}

func (r *manifestResource) readManifest(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	yamlBody := model.YAMLBody.ValueString()

	// Parse YAML to get resource identifiers
	manifest, err := yaml.ParseYAML(yamlBody)
	if err != nil {
		return fmt.Errorf("failed to parse kubernetes resource: %w", err)
	}

	// Apply namespace override if provided
	if !model.OverrideNamespace.IsNull() {
		manifest.SetNamespace(model.OverrideNamespace.ValueString())
	}

	log.Printf("[DEBUG] %v reading kubernetes resource", manifest)

	// Create REST client for this resource type
	restClient := util.GetRestClientFromUnstructured(
		ctx,
		manifest,
		r.providerData.MainClientset,
		r.providerData.RestConfig,
	)
	if restClient.Error != nil {
		return fmt.Errorf(
			"%v failed to create kubernetes rest client for resource: %w",
			manifest,
			restClient.Error,
		)
	}

	// Get the resource from Kubernetes
	rawResponse, err := restClient.ResourceInterface.Get(
		ctx,
		manifest.GetName(),
		meta_v1.GetOptions{},
	)
	if err != nil {
		if util.IsNotFoundError(err) {
			// Resource not found - will be removed from state by caller
			return err
		}
		return fmt.Errorf("%v failed to fetch resource from kubernetes: %w", manifest, err)
	}

	// Set response wrapper
	response := yaml.NewFromUnstructured(rawResponse)

	// Update computed values
	model.APIVersion = types.StringValue(response.GetAPIVersion())
	model.Kind = types.StringValue(response.GetKind())
	model.Name = types.StringValue(response.GetName())

	if response.HasNamespace() {
		model.Namespace = types.StringValue(response.GetNamespace())
	} else {
		model.Namespace = types.StringNull()
	}

	// Update UID fields
	model.LiveUID = types.StringValue(string(response.GetUID()))

	// If UID is not set, initialize it (for imports)
	if model.UID.IsNull() || model.UID.IsUnknown() {
		model.UID = types.StringValue(string(response.GetUID()))
	}

	log.Printf("[DEBUG] %v read successfully", manifest)

	// Generate fingerprints for drift detection
	fingerprint := r.generateFingerprints(ctx, manifest, rawResponse, model)
	model.YAMLInCluster = types.StringValue(fingerprint)
	model.LiveManifestInCluster = types.StringValue(fingerprint)

	return nil
}

func (r *manifestResource) deleteManifest(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	yamlBody := model.YAMLBody.ValueString()

	// Parse YAML
	manifest, err := yaml.ParseYAML(yamlBody)
	if err != nil {
		return fmt.Errorf("failed to parse kubernetes resource: %w", err)
	}

	// Apply namespace override if provided
	if !model.OverrideNamespace.IsNull() {
		manifest.SetNamespace(model.OverrideNamespace.ValueString())
	}

	log.Printf("[INFO] %v deleting kubernetes resource", manifest)

	// Create REST client for this resource type
	restClient := util.GetRestClientFromUnstructured(
		ctx,
		manifest,
		r.providerData.MainClientset,
		r.providerData.RestConfig,
	)
	if restClient.Error != nil {
		return fmt.Errorf(
			"%v failed to create kubernetes rest client for resource: %w",
			manifest,
			restClient.Error,
		)
	}

	// Determine cascade mode
	cascadeMode := "background"
	if !model.DeleteCascade.IsNull() {
		cascadeMode = model.DeleteCascade.ValueString()
	}

	// Build delete options
	deleteOptions := meta_v1.DeleteOptions{}

	switch cascadeMode {
	case "foreground":
		propagationPolicy := meta_v1.DeletePropagationForeground
		deleteOptions.PropagationPolicy = &propagationPolicy
	case "background":
		propagationPolicy := meta_v1.DeletePropagationBackground
		deleteOptions.PropagationPolicy = &propagationPolicy
	case "orphan":
		propagationPolicy := meta_v1.DeletePropagationOrphan
		deleteOptions.PropagationPolicy = &propagationPolicy
	default:
		// Default to background
		propagationPolicy := meta_v1.DeletePropagationBackground
		deleteOptions.PropagationPolicy = &propagationPolicy
	}

	// Delete the resource
	err = restClient.ResourceInterface.Delete(ctx, manifest.GetName(), deleteOptions)
	if err != nil {
		if util.IsNotFoundError(err) {
			// Resource already deleted, not an error
			log.Printf("[INFO] %v resource already deleted", manifest)
			return nil
		}
		return fmt.Errorf("%v failed to delete resource: %w", manifest, err)
	}

	log.Printf("[INFO] %v resource deleted successfully", manifest)

	// Wait for deletion if wait is enabled (default true for safety with finalizers)
	// This ensures resources with finalizers are fully deleted before returning
	waitForDeletion := true
	if !model.Wait.IsNull() {
		waitForDeletion = model.Wait.ValueBool()
	}

	if waitForDeletion {
		// Get timeout for deletion (default 5 minutes)
		deleteTimeout, diags := model.Timeouts.Delete(ctx, 5*time.Minute)
		if diags.HasError() {
			// Don't fail on timeout parse error, deletion already initiated
			log.Printf("[WARN] Could not parse delete timeout, using 5 minute default")
			deleteTimeout = 5 * time.Minute
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, deleteTimeout)
		defer cancel()

		log.Printf(
			"[DEBUG] Waiting for %v to be fully deleted (timeout: %v)",
			manifest,
			deleteTimeout,
		)

		// Poll until resource is NotFound
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-timeoutCtx.Done():
				log.Printf(
					"[WARN] Timeout waiting for %v deletion, but delete was initiated",
					manifest,
				)
				return nil // Don't fail, deletion was initiated
			case <-ticker.C:
				_, err := restClient.ResourceInterface.Get(
					ctx,
					manifest.GetName(),
					meta_v1.GetOptions{},
				)
				if util.IsNotFoundError(err) {
					log.Printf("[INFO] %v confirmed deleted", manifest)
					return nil
				}
				if err != nil {
					log.Printf("[DEBUG] Error checking deletion status: %v", err)
				}
				log.Printf("[TRACE] %v still exists, waiting for deletion...", manifest)
			}
		}
	}

	return nil
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
	return util.IsNotFoundError(err)
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

// Helper methods for waiting on resource rollouts

func (r *manifestResource) waitForDeployment(
	ctx context.Context,
	namespace string,
	name string,
	timeout time.Duration,
) error {
	// Borrowed from: https://github.com/kubernetes/kubectl/blob/c4be63c54b7188502c1a63bb884a0b05fac51ebd/pkg/polymorphichelpers/rollout_status.go#L70

	timeoutSeconds := int64(timeout.Seconds())

	watcher, err := r.providerData.MainClientset.AppsV1().Deployments(namespace).Watch(
		ctx,
		meta_v1.ListOptions{
			Watch:          true,
			TimeoutSeconds: &timeoutSeconds,
			FieldSelector:  fields.OneTermEqualSelector("metadata.name", name).String(),
		},
	)
	if err != nil {
		return err
	}
	defer watcher.Stop()

	done := false
	for !done {
		select {
		case event := <-watcher.ResultChan():
			if event.Type == watch.Modified {
				deployment, ok := event.Object.(*apps_v1.Deployment)
				if !ok {
					return fmt.Errorf("%s could not cast to Deployment", name)
				}

				if deployment.Generation <= deployment.Status.ObservedGeneration {
					// Check if replicas are ready
					if deployment.Spec.Replicas != nil {
						if deployment.Status.UpdatedReplicas < *deployment.Spec.Replicas {
							continue
						}
						if deployment.Status.Replicas > deployment.Status.UpdatedReplicas {
							continue
						}
						if deployment.Status.AvailableReplicas < deployment.Status.UpdatedReplicas {
							continue
						}
					}

					done = true
				}
			}

		case <-ctx.Done():
			return fmt.Errorf("%s failed to rollout Deployment within timeout", name)
		}
	}

	return nil
}

func (r *manifestResource) waitForStatefulSet(
	ctx context.Context,
	namespace string,
	name string,
	timeout time.Duration,
) error {
	// Borrowed from: https://github.com/kubernetes/kubectl/blob/c4be63c54b7188502c1a63bb884a0b05fac51ebd/pkg/polymorphichelpers/rollout_status.go#L120

	timeoutSeconds := int64(timeout.Seconds())

	watcher, err := r.providerData.MainClientset.AppsV1().StatefulSets(namespace).Watch(
		ctx,
		meta_v1.ListOptions{
			Watch:          true,
			TimeoutSeconds: &timeoutSeconds,
			FieldSelector:  fields.OneTermEqualSelector("metadata.name", name).String(),
		},
	)
	if err != nil {
		return err
	}
	defer watcher.Stop()

	done := false
	for !done {
		select {
		case event := <-watcher.ResultChan():
			if event.Type == watch.Modified {
				sts, ok := event.Object.(*apps_v1.StatefulSet)
				if !ok {
					return fmt.Errorf("%s could not cast to StatefulSet", name)
				}

				if sts.Spec.UpdateStrategy.Type != apps_v1.RollingUpdateStatefulSetStrategyType {
					done = true
					continue
				}

				if sts.Status.ObservedGeneration == 0 ||
					sts.Generation > sts.Status.ObservedGeneration {
					continue
				}

				if sts.Spec.Replicas != nil && sts.Status.ReadyReplicas < *sts.Spec.Replicas {
					continue
				}

				if sts.Spec.UpdateStrategy.Type == apps_v1.RollingUpdateStatefulSetStrategyType &&
					sts.Spec.UpdateStrategy.RollingUpdate != nil {
					if sts.Spec.Replicas != nil &&
						sts.Spec.UpdateStrategy.RollingUpdate.Partition != nil {
						if sts.Status.UpdatedReplicas < (*sts.Spec.Replicas - *sts.Spec.UpdateStrategy.RollingUpdate.Partition) {
							continue
						}
					}

					done = true
					continue
				}

				if sts.Status.UpdateRevision != sts.Status.CurrentRevision {
					continue
				}

				done = true
			}

		case <-ctx.Done():
			return fmt.Errorf("%s failed to rollout StatefulSet within timeout", name)
		}
	}

	return nil
}

func (r *manifestResource) waitForDaemonSet(
	ctx context.Context,
	namespace string,
	name string,
	timeout time.Duration,
) error {
	// Borrowed from: https://github.com/kubernetes/kubectl/blob/c4be63c54b7188502c1a63bb884a0b05fac51ebd/pkg/polymorphichelpers/rollout_status.go#L95

	timeoutSeconds := int64(timeout.Seconds())

	watcher, err := r.providerData.MainClientset.AppsV1().DaemonSets(namespace).Watch(
		ctx,
		meta_v1.ListOptions{
			Watch:          true,
			TimeoutSeconds: &timeoutSeconds,
			FieldSelector:  fields.OneTermEqualSelector("metadata.name", name).String(),
		},
	)
	if err != nil {
		return err
	}
	defer watcher.Stop()

	done := false
	for !done {
		select {
		case event := <-watcher.ResultChan():
			if event.Type == watch.Modified {
				daemon, ok := event.Object.(*apps_v1.DaemonSet)
				if !ok {
					return fmt.Errorf("%s could not cast to DaemonSet", name)
				}

				if daemon.Spec.UpdateStrategy.Type != apps_v1.RollingUpdateDaemonSetStrategyType {
					done = true
					continue
				}

				if daemon.Generation <= daemon.Status.ObservedGeneration {
					if daemon.Status.UpdatedNumberScheduled < daemon.Status.DesiredNumberScheduled {
						continue
					}

					if daemon.Status.NumberAvailable < daemon.Status.DesiredNumberScheduled {
						continue
					}

					done = true
				}
			}

		case <-ctx.Done():
			return fmt.Errorf("%s failed to rollout DaemonSet within timeout", name)
		}
	}

	return nil
}

func (r *manifestResource) waitForConditions(
	ctx context.Context,
	restClient *util.RestClientResult,
	conditions []waitConditionModel,
	waitFields []waitFieldModel,
	name string,
	timeout time.Duration,
) error {
	timeoutSeconds := int64(timeout.Seconds())

	watcher, err := restClient.ResourceInterface.Watch(
		ctx,
		meta_v1.ListOptions{
			Watch:          true,
			TimeoutSeconds: &timeoutSeconds,
			FieldSelector:  fields.OneTermEqualSelector("metadata.name", name).String(),
		},
	)
	if err != nil {
		return err
	}
	defer watcher.Stop()

	done := false
	for !done {
		select {
		case event := <-watcher.ResultChan():
			log.Printf("[TRACE] Received event type %s for %s", event.Type, name)
			if event.Type == watch.Modified || event.Type == watch.Added {
				rawResponse, ok := event.Object.(*meta_v1_unstruct.Unstructured)
				if !ok {
					return fmt.Errorf("%s could not cast resource to unstructured", name)
				}

				totalConditions := len(conditions) + len(waitFields)
				totalMatches := 0

				yamlJson, err := rawResponse.MarshalJSON()
				if err != nil {
					return err
				}

				gq := gojsonq.New().FromString(string(yamlJson))

				// Check conditions
				for _, c := range conditions {
					condType := c.Type.ValueString()
					condStatus := c.Status.ValueString()

					// Find the conditions by status and type
					count := gq.Reset().From("status.conditions").
						Where("type", "=", condType).
						Where("status", "=", condStatus).Count()
					if count == 0 {
						log.Printf(
							"[TRACE] Condition %s with status %s not found in %s",
							condType,
							condStatus,
							name,
						)
						continue
					}
					log.Printf(
						"[TRACE] Condition %s with status %s found in %s",
						condType,
						condStatus,
						name,
					)
					totalMatches++
				}

				// Check fields
				for _, f := range waitFields {
					key := f.Key.ValueString()
					value := f.Value.ValueString()
					valueType := f.ValueType.ValueString()
					if valueType == "" {
						valueType = "eq"
					}

					// Find the key
					v := gq.Reset().Find(key)
					if v == nil {
						log.Printf("[TRACE] Key %s not found in %s", key, name)
						continue
					}

					// For the sake of comparison we will convert everything to a string
					stringVal := fmt.Sprintf("%v", v)
					switch valueType {
					case "regex":
						matched, err := regexp.Match(value, []byte(stringVal))
						if err != nil {
							return err
						}

						if !matched {
							log.Printf(
								"[TRACE] Value %s does not match regex %s in %s (key %s)",
								stringVal,
								value,
								name,
								key,
							)
							continue
						}

						log.Printf(
							"[TRACE] Value %s matches regex %s in %s (key %s)",
							stringVal,
							value,
							name,
							key,
						)
						totalMatches++

					case "eq":
						if stringVal != value {
							log.Printf(
								"[TRACE] Value %s does not match %s in %s (key %s)",
								stringVal,
								value,
								name,
								key,
							)
							continue
						}
						log.Printf(
							"[TRACE] Value %s matches %s in %s (key %s)",
							stringVal,
							value,
							name,
							key,
						)
						totalMatches++
					}
				}

				if totalMatches == totalConditions {
					log.Printf("[TRACE] All conditions met for %s", name)
					done = true
					continue
				}
				log.Printf(
					"[TRACE] %d/%d conditions met for %s. Waiting for next event",
					totalMatches,
					totalConditions,
					name,
				)
			}

		case <-ctx.Done():
			return fmt.Errorf("%s failed to wait for resource within timeout", name)
		}
	}

	return nil
}

// generateFingerprints creates a fingerprint of the live manifest for drift detection
// This is based on the SDK v2 implementation in kubernetes/resource_kubectl_manifest.go.
func (r *manifestResource) generateFingerprints(
	ctx context.Context,
	userProvided *yaml.Manifest,
	liveManifest *meta_v1_unstruct.Unstructured,
	model *manifestResourceModel,
) string {
	// Extract ignore fields from model
	var ignoreFields []string
	if !model.IgnoreFields.IsNull() && !model.IgnoreFields.IsUnknown() {
		model.IgnoreFields.ElementsAs(ctx, &ignoreFields, false)
	}

	// Handle Secret stringData special case
	// If user provided stringData, convert it to base64-encoded data for comparison
	if userProvided.GetKind() == "Secret" && userProvided.GetAPIVersion() == "v1" {
		if stringData, found := userProvided.Raw.Object["stringData"]; found {
			if stringDataMap, ok := stringData.(map[string]any); ok {
				// Move all stringData values to data as base64
				for k, v := range stringDataMap {
					encodedString := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%v", v)))
					meta_v1_unstruct.SetNestedField(
						userProvided.Raw.Object,
						encodedString,
						"data",
						k,
					)
				}
				// Remove stringData field
				meta_v1_unstruct.RemoveNestedField(userProvided.Raw.Object, "stringData")
			}
		}
	}

	// Flatten both manifests for comparison
	flattenedUser := flatten.Flatten(userProvided.Raw.Object)
	flattenedLive := flatten.Flatten(liveManifest.Object)

	// Remove control fields and ignore fields
	fieldsToTrim := append(kubernetesControlFields, ignoreFields...)
	for _, field := range fieldsToTrim {
		delete(flattenedUser, field)

		// Remove any nested fields that start with this prefix
		for k := range flattenedUser {
			if strings.HasPrefix(k, field+".") {
				delete(flattenedUser, k)
			}
		}
	}

	// Build fingerprint from user keys with live values
	var userKeys []string
	for userKey, userValue := range flattenedUser {
		normalizedUserValue := strings.TrimSpace(userValue)

		// Only include if key exists in live manifest
		if _, exists := flattenedLive[userKey]; exists {
			userKeys = append(userKeys, userKey)
			normalizedLiveValue := strings.TrimSpace(flattenedLive[userKey])
			if normalizedUserValue != normalizedLiveValue {
				log.Printf("[TRACE] yaml drift detected in %s for %s, was: %s now: %s",
					userProvided.GetSelfLink(), userKey, normalizedUserValue, normalizedLiveValue)
			}
			// Hash the live value
			flattenedUser[userKey] = getFingerprint(normalizedLiveValue)
		} else {
			if normalizedUserValue != "" {
				log.Printf("[TRACE] yaml drift detected in %s for %s, was %s now blank",
					userProvided.GetSelfLink(), userKey, normalizedUserValue)
			}
		}
	}

	// Sort keys for consistent fingerprint
	sort.Strings(userKeys)
	var returnedValues []string
	for _, k := range userKeys {
		returnedValues = append(returnedValues, fmt.Sprintf("%s=%s", k, flattenedUser[k]))
	}

	return strings.Join(returnedValues, "\n")
}

// getFingerprint generates a SHA256 hash of a string.
func getFingerprint(s string) string {
	fingerprint := sha256.New()
	fingerprint.Write([]byte(s))
	return fmt.Sprintf("%x", fingerprint.Sum(nil))
}

// kubernetesControlFields are fields managed by Kubernetes that should be ignored in drift detection.
var kubernetesControlFields = []string{
	"status",
	"metadata.finalizers",
	"metadata.initializers",
	"metadata.ownerReferences",
	"metadata.creationTimestamp",
	"metadata.generation",
	"metadata.resourceVersion",
	"metadata.uid",
	"metadata.annotations.kubectl.kubernetes.io/last-applied-configuration",
	"metadata.managedFields",
}
