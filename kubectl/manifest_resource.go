//nolint:forcetypeassert
package kubectl

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/dynamicplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/thedevsaddam/gojsonq/v2"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/api"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/morph"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/payload"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/util"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                   = &manifestResource{}
	_ resource.ResourceWithConfigure      = &manifestResource{}
	_ resource.ResourceWithIdentity       = &manifestResource{}
	_ resource.ResourceWithImportState    = &manifestResource{}
	_ resource.ResourceWithModifyPlan     = &manifestResource{}
	_ resource.ResourceWithValidateConfig = &manifestResource{}
)

// manifestResource defines the resource implementation.
type manifestResource struct {
	providerData *kubectlProviderData
}

// manifestResourceModel describes the resource data model.
// manifestResourceModel describes the resource data model.
// The manifest attribute contains the full Kubernetes resource definition
// (apiVersion, kind, metadata, spec, data, etc.) as a single Dynamic value,
// aligning with the upstream hashicorp/terraform-provider-kubernetes pattern.
type manifestResourceModel struct {
	ID              types.String   `tfsdk:"id"`
	Manifest        types.Dynamic  `tfsdk:"manifest"`
	Status          types.Dynamic  `tfsdk:"status"`
	Object          types.Dynamic  `tfsdk:"object"`
	ComputedFields  types.List     `tfsdk:"computed_fields"`
	ImmutableFields types.List     `tfsdk:"immutable_fields"`
	ApplyOnly       types.Bool     `tfsdk:"apply_only"`
	DeleteCascade   types.String   `tfsdk:"delete_cascade"`
	Wait            types.Object   `tfsdk:"wait"`
	ErrorOn         types.Object   `tfsdk:"error_on"`
	FieldManager    types.Object   `tfsdk:"field_manager"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
}

// waitModel describes the wait block.
type waitModel struct {
	Rollout    types.Bool `tfsdk:"rollout"`
	Fields     types.List `tfsdk:"field"`
	Conditions types.List `tfsdk:"condition"`
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

// errorOnModel describes the error_on block.
// Error conditions are checked continuously while waiting for success conditions.
// If any error condition matches, the apply fails immediately.
type errorOnModel struct {
	Fields     types.List `tfsdk:"field"`
	Conditions types.List `tfsdk:"condition"`
}

// fieldManagerModel describes the field_manager block.
type fieldManagerModel struct {
	Name           types.String `tfsdk:"name"`
	ForceConflicts types.Bool   `tfsdk:"force_conflicts"`
}

// manifestIdentityModel describes the resource identity.
type manifestIdentityModel struct {
	APIVersion types.String `tfsdk:"api_version"`
	Kind       types.String `tfsdk:"kind"`
	Name       types.String `tfsdk:"name"`
	Namespace  types.String `tfsdk:"namespace"`
}

// waitBlockAttrTypes returns the attribute types map for the wait block.
func waitBlockAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"rollout": types.BoolType,
		"field": types.ListType{ElemType: types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"key":        types.StringType,
				"value":      types.StringType,
				"value_type": types.StringType,
			},
		}},
		"condition": types.ListType{ElemType: types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"type":   types.StringType,
				"status": types.StringType,
			},
		}},
	}
}

// errorOnBlockAttrTypes returns the attribute types map for the error_on block.
func errorOnBlockAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"field": types.ListType{ElemType: types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"key":        types.StringType,
				"value":      types.StringType,
				"value_type": types.StringType,
			},
		}},
		"condition": types.ListType{ElemType: types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"type":   types.StringType,
				"status": types.StringType,
			},
		}},
	}
}

// fieldManagerBlockAttrTypes returns the attribute types map for the field_manager block.
func fieldManagerBlockAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"name":            types.StringType,
		"force_conflicts": types.BoolType,
	}
}

// buildIdentityModel creates an identity model from a manifest resource model.
func buildIdentityModel(ctx context.Context, model *manifestResourceModel) manifestIdentityModel {
	identity := manifestIdentityModel{}

	apiVersion, _ := extractManifestField(ctx, model.Manifest, "apiVersion")
	kind, _ := extractManifestField(ctx, model.Manifest, "kind")
	identity.APIVersion = types.StringValue(fmt.Sprintf("%v", apiVersion))
	identity.Kind = types.StringValue(fmt.Sprintf("%v", kind))

	name, _ := extractManifestMetadataField(ctx, model.Manifest, "name")
	if name != "" {
		identity.Name = types.StringValue(name)
	}
	namespace, _ := extractManifestMetadataField(ctx, model.Manifest, "namespace")
	if namespace != "" {
		identity.Namespace = types.StringValue(namespace)
	} else {
		identity.Namespace = types.StringNull()
	}

	return identity
}

// setResponseIdentity sets the identity on a response if the identity field is populated.
func setResponseIdentity(
	ctx context.Context,
	identity *tfsdk.ResourceIdentity,
	model *manifestResourceModel,
	diagnostics *diag.Diagnostics,
) {
	if identity == nil {
		return
	}

	idModel := buildIdentityModel(ctx, model)
	diagnostics.Append(identity.Set(ctx, idModel)...)
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

// IdentitySchema returns the identity schema for this resource.
func (r *manifestResource) IdentitySchema(
	ctx context.Context,
	req resource.IdentitySchemaRequest,
	resp *resource.IdentitySchemaResponse,
) {
	resp.IdentitySchema = identityschema.Schema{
		Version: 1,
		Attributes: map[string]identityschema.Attribute{
			"api_version": identityschema.StringAttribute{
				RequiredForImport: true,
				Description:       "Kubernetes API version (e.g., v1, apps/v1).",
			},
			"kind": identityschema.StringAttribute{
				RequiredForImport: true,
				Description:       "Kubernetes resource kind (e.g., ConfigMap, Deployment).",
			},
			"name": identityschema.StringAttribute{
				RequiredForImport: true,
				Description:       "Name of the Kubernetes resource.",
			},
			"namespace": identityschema.StringAttribute{
				OptionalForImport: true,
				Description:       "Namespace of the Kubernetes resource. Empty for cluster-scoped resources.",
			},
		},
	}
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
				MarkdownDescription: "Kubernetes resource unique identifier (UID) assigned by the API server. " +
					"This is a read-only value and has no impact on the plan.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"manifest": schema.DynamicAttribute{
				Required: true,
				MarkdownDescription: "An object representation of the Kubernetes resource manifest. " +
					"Must contain `apiVersion`, `kind`, and `metadata` (with at least `name`). " +
					"Additional fields like `spec`, `data`, `stringData`, etc. depend on the resource kind.",
			},
			"status": schema.DynamicAttribute{
				Computed:            true,
				MarkdownDescription: "Resource status as reported by the Kubernetes API server.",
				PlanModifiers: []planmodifier.Dynamic{
					dynamicplanmodifier.UseStateForUnknown(),
				},
			},
			"object": schema.DynamicAttribute{
				Computed:            true,
				MarkdownDescription: "The full resource object as returned by the API server.",
				PlanModifiers: []planmodifier.Dynamic{
					dynamicplanmodifier.UseStateForUnknown(),
				},
			},
			"computed_fields": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				MarkdownDescription: "List of manifest fields whose values may be altered by the API server during apply. " +
					"Defaults to: `[\"metadata.annotations\", \"metadata.labels\"]`",
			},
			"immutable_fields": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				MarkdownDescription: "List of manifest field paths that are immutable after creation. " +
					"If any of these fields change, the resource will be replaced (destroyed and re-created). " +
					"Uses dot-separated paths (e.g., `spec.selector`).",
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
			"wait": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Configure waiter options. The apply will block until success conditions are met or the timeout is reached.",
				Attributes: map[string]schema.Attribute{
					"rollout": schema.BoolAttribute{
						Optional:            true,
						MarkdownDescription: "Wait for rollout to complete on resources that support `kubectl rollout status`.",
					},
					"field": schema.ListNestedAttribute{
						Optional: true,
						MarkdownDescription: "Wait for a resource field to reach an expected value. " +
							"Multiple entries can be specified; all must match.",
						NestedObject: schema.NestedAttributeObject{
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
					"condition": schema.ListNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Wait for status conditions to match.",
						NestedObject: schema.NestedAttributeObject{
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
				},
			},
			"error_on": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Define error conditions that are checked continuously while waiting for success conditions. " +
					"If any error condition matches, the apply fails immediately. " +
					"Use this to detect error states such as CrashLoopBackOff or Failed status.",
				Attributes: map[string]schema.Attribute{
					"field": schema.ListNestedAttribute{
						Optional: true,
						MarkdownDescription: "Fail if a resource field matches an error pattern. " +
							"Multiple entries can be specified; any match triggers failure.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"key": schema.StringAttribute{
									Required:            true,
									MarkdownDescription: "JSON path to the field to check (e.g., `status.containerStatuses.0.state.waiting.reason`).",
								},
								"value": schema.StringAttribute{
									Required:            true,
									MarkdownDescription: "Regex pattern to match against the field value. If matched, the apply fails immediately.",
								},
								"value_type": schema.StringAttribute{
									Optional:            true,
									Computed:            true,
									Default:             stringdefault.StaticString("regex"),
									MarkdownDescription: "Comparison type: `eq` for exact match or `regex` for regular expression matching (default).",
									Validators: []validator.String{
										stringvalidator.OneOf("eq", "regex"),
									},
								},
							},
						},
					},
					"condition": schema.ListNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Fail if a status condition matches. Any match triggers failure.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "The type of condition to check.",
								},
								"status": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "The condition status to match.",
								},
							},
						},
					},
				},
			},
			"field_manager": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Configure field manager options for server-side apply.",
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

	// Validate manifest has required fields: apiVersion, kind, metadata.name
	if !config.Manifest.IsNull() && !config.Manifest.IsUnknown() {
		manifestMap, d := dynamicToMap(ctx, config.Manifest)
		resp.Diagnostics.Append(d...)
		if !resp.Diagnostics.HasError() && manifestMap != nil {
			if _, ok := manifestMap["apiVersion"]; !ok {
				resp.Diagnostics.AddAttributeError(
					path.Root("manifest"),
					"Missing required field",
					"manifest must contain an 'apiVersion' field",
				)
			}
			if _, ok := manifestMap["kind"]; !ok {
				resp.Diagnostics.AddAttributeError(
					path.Root("manifest"),
					"Missing required field",
					"manifest must contain a 'kind' field",
				)
			}
			if meta, ok := manifestMap["metadata"]; ok {
				if metaMap, ok := meta.(map[string]any); ok {
					if _, ok := metaMap["name"]; !ok {
						resp.Diagnostics.AddAttributeError(
							path.Root("manifest"),
							"Missing required field",
							"manifest.metadata must contain a 'name' field",
						)
					}
				}
			} else {
				resp.Diagnostics.AddAttributeError(
					path.Root("manifest"),
					"Missing required field",
					"manifest must contain a 'metadata' field",
				)
			}
		}
	}

	// Validate wait block — only one waiter type allowed
	if !config.Wait.IsNull() && !config.Wait.IsUnknown() {
		var w waitModel
		d := config.Wait.As(ctx, &w, basetypes.ObjectAsOptions{})
		resp.Diagnostics.Append(d...)
		if !resp.Diagnostics.HasError() {
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

	// Set identity
	setResponseIdentity(ctx, resp.Identity, &plan, &resp.Diagnostics)
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

	// Save prior manifest for reconciliation after read
	priorManifest := state.Manifest

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

	// Reconcile manifest: keep only attributes from prior state to avoid
	// perpetual diffs from server-generated fields (uid, creationTimestamp, etc.)
	state.Manifest = reconcileDynamicWithPrior(ctx, priorManifest, state.Manifest)

	// Set state
	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)

	// Set identity
	setResponseIdentity(ctx, resp.Identity, &state, &resp.Diagnostics)
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

	// Set identity
	setResponseIdentity(ctx, resp.Identity, &plan, &resp.Diagnostics)
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
	var apiVersion, kind, name, namespace string

	// Support three import methods:
	// 1. String ID with key=value pairs: apiVersion=<v>,kind=<k>,name=<n>[,namespace=<ns>]
	// 2. String ID with // separated: apiVersion//kind//name[//namespace]
	// 3. Identity-based import (Terraform 1.12+ import block with identity attribute)
	// Try string ID first since identity schema is always defined and req.Identity
	// will be non-nil even when only ImportStateId is used.
	if req.ID != "" && strings.Contains(req.ID, "=") {
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
	} else if req.ID != "" {
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
	} else if req.Identity != nil {
		// Import via identity attributes (Terraform 1.12+)
		var identityModel manifestIdentityModel
		diags := req.Identity.Get(ctx, &identityModel)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		apiVersion = identityModel.APIVersion.ValueString()
		kind = identityModel.Kind.ValueString()
		name = identityModel.Name.ValueString()
		if !identityModel.Namespace.IsNull() {
			namespace = identityModel.Namespace.ValueString()
		}
	} else {
		resp.Diagnostics.AddError(
			"Invalid Import",
			"No import ID or identity attributes provided.",
		)
		return
	}

	// Build manifest from import ID
	manifestMap := map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]any{"name": name},
	}
	if namespace != "" {
		manifestMap["metadata"].(map[string]any)["namespace"] = namespace
	}

	manifestDynamic, diags := mapToDynamic(ctx, manifestMap)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	model := manifestResourceModel{
		ID:              types.StringValue(req.ID),
		Manifest:        manifestDynamic,
		Status:          types.DynamicNull(),
		Object:          types.DynamicNull(),
		ComputedFields:  types.ListNull(types.StringType),
		ImmutableFields: types.ListNull(types.StringType),
		ApplyOnly:       types.BoolValue(false),
		DeleteCascade:   types.StringNull(),
		Wait:            types.ObjectNull(waitBlockAttrTypes()),
		ErrorOn:         types.ObjectNull(errorOnBlockAttrTypes()),
		FieldManager:    types.ObjectNull(fieldManagerBlockAttrTypes()),
		Timeouts: timeouts.Value{
			Object: types.ObjectNull(map[string]attr.Type{
				"create": types.StringType,
				"update": types.StringType,
				"delete": types.StringType,
			}),
		},
	}

	// Attempt OpenAPI-aware import if we have provider data
	if r.providerData != nil {
		gvk := k8sschema.FromAPIVersionAndKind(apiVersion, kind)
		objectType, typeHints, err := r.providerData.TFTypeFromOpenAPI(ctx, gvk, false)
		if err == nil && objectType != nil {
			if importErr := r.readManifestWithOpenAPI(
				ctx,
				&model,
				objectType,
				typeHints,
			); importErr != nil {
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

			// Set identity
			setResponseIdentity(ctx, resp.Identity, &model, &resp.Diagnostics)

			// Mark as imported in private state for ModifyPlan
			if resp.Private != nil {
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, "IsImported", []byte(`true`))...)
			}
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

	// Set identity
	setResponseIdentity(ctx, resp.Identity, &model, &resp.Diagnostics)

	// Mark as imported in private state for ModifyPlan
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, "IsImported", []byte(`true`))...)
	}
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

	// Check if apiVersion, kind, or metadata.name/namespace changed — requires replacement
	planAPIVersion, _ := extractManifestField(ctx, plan.Manifest, "apiVersion")
	stateAPIVersion, _ := extractManifestField(ctx, state.Manifest, "apiVersion")
	planKind, _ := extractManifestField(ctx, plan.Manifest, "kind")
	stateKind, _ := extractManifestField(ctx, state.Manifest, "kind")
	planName, _ := extractManifestMetadataField(ctx, plan.Manifest, "name")
	stateName, _ := extractManifestMetadataField(ctx, state.Manifest, "name")
	planNamespace, _ := extractManifestMetadataField(ctx, plan.Manifest, "namespace")
	stateNamespace, _ := extractManifestMetadataField(ctx, state.Manifest, "namespace")

	if fmt.Sprintf("%v", planAPIVersion) != fmt.Sprintf("%v", stateAPIVersion) {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("manifest"))
	}
	if fmt.Sprintf("%v", planKind) != fmt.Sprintf("%v", stateKind) {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("manifest"))
	}
	if planName != stateName {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("manifest"))
	}

	// Check if this resource was imported — skip namespace RequiresReplace for imported resources
	// since the state namespace may not match the plan namespace until the first apply
	isImported := false
	if req.Private != nil {
		importedData, d := req.Private.GetKey(ctx, "IsImported")
		resp.Diagnostics.Append(d...)
		if len(importedData) > 0 && string(importedData) == "true" {
			isImported = true
		}
	}

	if planNamespace != stateNamespace && !isImported {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("manifest"))
	}

	// Clear the IsImported flag after handling it (so subsequent plans behave normally)
	if isImported && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, "IsImported", []byte(`false`))...)
	}

	// Check immutable_fields — if any listed field changed, require replacement
	if !plan.ImmutableFields.IsNull() && !plan.ImmutableFields.IsUnknown() {
		var immutablePaths []string
		plan.ImmutableFields.ElementsAs(ctx, &immutablePaths, false)
		if len(immutablePaths) > 0 {
			planMap, _ := dynamicToMap(ctx, plan.Manifest)
			stateMap, _ := dynamicToMap(ctx, state.Manifest)
			if planMap != nil && stateMap != nil {
				for _, fieldPath := range immutablePaths {
					atp, err := api.FieldPathToTftypesPath(fieldPath)
					if err != nil {
						resp.Diagnostics.AddAttributeWarning(
							path.Root("immutable_fields"),
							"Invalid immutable field path",
							fmt.Sprintf("Could not parse path %q: %s", fieldPath, err),
						)
						continue
					}
					planVal, planOk := walkMapByTFPath(planMap, atp)
					stateVal, stateOk := walkMapByTFPath(stateMap, atp)
					if planOk && stateOk &&
						fmt.Sprintf("%v", planVal) != fmt.Sprintf("%v", stateVal) {
						resp.RequiresReplace = append(resp.RequiresReplace, path.Root("manifest"))
						break
					}
				}
			}
		}
	}

	// Save original plan manifest before OpenAPI morphing for change detection.
	origManifest := plan.Manifest

	// Attempt OpenAPI type resolution for computed field handling
	apiVersionStr := fmt.Sprintf("%v", planAPIVersion)
	kindStr := fmt.Sprintf("%v", planKind)
	if r.providerData != nil && apiVersionStr != "" && kindStr != "" {
		r.modifyPlanWithOpenAPI(ctx, &plan, &state, resp)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Determine if user-provided manifest changed between plan and state.
	// If something changed, status/object must be Unknown (provider will produce new values).
	// If nothing changed, preserve state values to prevent perpetual diffs.
	hasChange := !origManifest.Equal(state.Manifest)

	if hasChange {
		plan.Status = types.DynamicUnknown()
		plan.Object = types.DynamicUnknown()
	} else {
		plan.Status = state.Status
		plan.Object = state.Object
	}

	diags = resp.Plan.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// modifyPlanWithOpenAPI uses OpenAPI spec to resolve the resource type and
// handle computed fields during plan modification.
// For Update plans, it performs tree traversal comparing prior manifest with proposed
// manifest and prior object to minimize "known after apply" noise.
func (r *manifestResource) modifyPlanWithOpenAPI(
	ctx context.Context,
	plan *manifestResourceModel,
	state *manifestResourceModel,
	resp *resource.ModifyPlanResponse,
) {
	// Extract apiVersion and kind from manifest
	manifestMap, d := dynamicToMap(ctx, plan.Manifest)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() || manifestMap == nil {
		return
	}

	apiVersion := fmt.Sprintf("%v", manifestMap["apiVersion"])
	kind := fmt.Sprintf("%v", manifestMap["kind"])
	gvk := k8sschema.FromAPIVersionAndKind(apiVersion, kind)

	// Try to resolve OpenAPI type
	objectType, hints, err := r.providerData.TFTypeFromOpenAPI(ctx, gvk, false)
	if err != nil {
		log.Printf("[DEBUG] Could not resolve OpenAPI type for %s: %v", gvk.String(), err)
		return
	}

	if !objectType.Is(tftypes.Object{}) {
		log.Printf(
			"[DEBUG] Non-structural type for %s, skipping OpenAPI plan modification",
			gvk.String(),
		)
		return
	}

	// Build proposed manifest map directly from the manifest attribute
	proposedManifestMap := manifestMap

	// Convert proposed manifest to tftypes.Value using OpenAPI type
	ppMan, err := payload.ToTFValue(
		proposedManifestMap,
		objectType,
		nil,
		tftypes.NewAttributePath(),
	)
	if err != nil {
		log.Printf("[DEBUG] Could not convert plan to tftypes.Value: %v", err)
		return
	}

	// Morph to OpenAPI type
	morphedPlan, morphDiags := morph.ValueToType(ppMan, objectType, tftypes.NewAttributePath())
	if len(morphDiags) > 0 {
		log.Printf("[DEBUG] Could not morph plan to OpenAPI type: %v", morphDiags)
		return
	}

	// DeepUnknown to mark unspecified fields
	completePlan, err := morph.DeepUnknown(objectType, morphedPlan, tftypes.NewAttributePath())
	if err != nil {
		log.Printf("[DEBUG] Could not apply DeepUnknown: %v", err)
		return
	}

	// Build computed fields map
	computedFields := make(map[string]*tftypes.AttributePath)
	if !plan.ComputedFields.IsNull() && !plan.ComputedFields.IsUnknown() {
		var cfList []string
		plan.ComputedFields.ElementsAs(ctx, &cfList, false)
		for _, cf := range cfList {
			atp, err := api.FieldPathToTftypesPath(cf)
			if err != nil {
				log.Printf("[DEBUG] Could not parse computed_fields path %s: %v", cf, err)
				continue
			}
			computedFields[atp.String()] = atp
		}
	}
	if len(computedFields) == 0 {
		atp := tftypes.NewAttributePath().
			WithAttributeName("metadata").
			WithAttributeName("annotations")
		computedFields[atp.String()] = atp
		atp = tftypes.NewAttributePath().WithAttributeName("metadata").WithAttributeName("labels")
		computedFields[atp.String()] = atp
	}

	// Build prior manifest and prior object tftypes.Values for Update comparison
	var priorMan tftypes.Value
	var priorObj tftypes.Value
	hasPrior := false

	if !state.Object.IsNull() && !state.Object.IsUnknown() {
		// Build prior manifest from state
		stateManifestMap, d := dynamicToMap(ctx, state.Manifest)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		if stateManifestMap == nil {
			return
		}

		priorManTfValue, err := payload.ToTFValue(
			stateManifestMap, objectType, nil, tftypes.NewAttributePath(),
		)
		if err == nil {
			priorMan = priorManTfValue
		}

		// Build prior object from state's full object
		objectMap, d := dynamicToMap(ctx, state.Object)
		resp.Diagnostics.Append(d...)
		if !resp.Diagnostics.HasError() && objectMap != nil {
			priorObjTfValue, err := payload.ToTFValue(
				objectMap, objectType, hints, tftypes.NewAttributePath(),
			)
			if err == nil {
				hasPrior = true
				priorObj = priorObjTfValue
			}
		}
	}

	if hasPrior {
		// Update plan: compare prior manifest with proposed manifest and use prior object values
		completePlan, err = tftypes.Transform(
			completePlan,
			func(ap *tftypes.AttributePath, v tftypes.Value) (tftypes.Value, error) {
				_, isComputed := computedFields[ap.String()]

				if v.IsKnown() {
					// This is a value from current configuration — include it in the plan
					hasChanged := false

					// Check if value changed between prior and proposed manifest
					wasCfg, restPath, walkErr := tftypes.WalkAttributePath(priorMan, ap)
					if walkErr != nil && len(restPath.Steps()) != 0 {
						// New field not in prior config
						hasChanged = true
					}
					if nowCfg, restPath, walkErr := tftypes.WalkAttributePath(
						ppMan,
						ap,
					); walkErr == nil {
						hasChanged = len(restPath.Steps()) == 0 &&
							wasCfg.(tftypes.Value).IsKnown() &&
							!wasCfg.(tftypes.Value).Equal(nowCfg.(tftypes.Value))
						if hasChanged {
							h, ok := hints[morph.ValueToTypePath(ap).String()]
							if ok && h == api.PreserveUnknownFieldsLabel {
								resp.Diagnostics.AddWarning(
									fmt.Sprintf(
										"The attribute path %v value's type is an x-kubernetes-preserve-unknown-field",
										morph.ValueToTypePath(ap).String(),
									),
									"Changes to the type may cause some unexpected behavior.",
								)
							}
						}
					}

					if isComputed {
						if hasChanged {
							// Computed field changed — mark unknown for API to fill
							return tftypes.NewValue(v.Type(), tftypes.UnknownValue), nil
						}
						// Computed field not changed — carry forward from prior object
						nowVal, restPath, walkErr := tftypes.WalkAttributePath(priorObj, ap)
						if walkErr == nil && len(restPath.Steps()) == 0 {
							return nowVal.(tftypes.Value), nil
						}
					}
					return v, nil
				}

				// Unknown value: check if it was in prior manifest (removed from config)
				wasVal, restPath, walkErr := tftypes.WalkAttributePath(priorMan, ap)
				if walkErr == nil && len(restPath.Steps()) == 0 &&
					wasVal.(tftypes.Value).IsKnown() {
					// Attribute was previously set in config and has now been removed
					// Return unknown to give the API a chance to set a default
					return v, nil
				}

				// Check for default value from prior state object
				priorAttrVal, restPath, walkErr := tftypes.WalkAttributePath(priorObj, ap)
				if walkErr != nil {
					if len(restPath.Steps()) > 0 {
						// Attribute wasn't fully present — use proposed value
						return v, nil
					}
					// Path totally foreign — keep as-is
					return v, nil
				}
				if len(restPath.Steps()) > 0 {
					log.Printf(
						"[WARN] Unexpected missing attribute from state at %s + %s",
						ap.String(), restPath.String(),
					)
				}
				return priorAttrVal.(tftypes.Value), nil
			},
		)
		if err != nil {
			log.Printf("[DEBUG] Could not apply Update tree traversal: %v", err)
			return
		}
	} else {
		// Create plan (no prior state): just mark computed_fields as unknown
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
			return
		}
	}

	// Convert back to map[string]any for framework
	resultMap, err := payload.FromTFValue(completePlan, nil, tftypes.NewAttributePath())
	if err != nil {
		log.Printf("[DEBUG] Could not convert morphed plan back to map: %v", err)
		return
	}

	resultContent, ok := resultMap.(map[string]any)
	if !ok {
		return
	}

	// Update plan manifest from morphed result (writing back the entire manifest)
	manifestDynamic, d := mapToDynamic(ctx, resultContent)
	resp.Diagnostics.Append(d...)
	if !resp.Diagnostics.HasError() {
		plan.Manifest = manifestDynamic
	}

	// NOTE: status and object are handled by the centralized change detection
	// in ModifyPlan, not here.
}

// applyManifest applies the manifest to Kubernetes using server-side apply,
// then handles wait conditions. error_on conditions are checked continuously
// while waiting for success conditions to be met.
func (r *manifestResource) applyManifest(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// Save user-provided manifest before readManifest overwrites it.
	// The API response includes server-generated fields (uid, creationTimestamp, etc.)
	// which would change the types.Dynamic object type and cause Terraform's
	// "wrong final value type" consistency check to fail.
	plannedManifest := model.Manifest

	// Delegate to applyManifestV2 for the actual apply
	if err := r.applyManifestV2(ctx, model); err != nil {
		return err
	}

	// Read back to populate computed fields (ID, status, object) from server response
	if err := r.readManifest(ctx, model); err != nil {
		return fmt.Errorf("failed to read manifest after apply: %w", err)
	}

	// Restore user-provided manifest to maintain type compatibility with plan
	model.Manifest = plannedManifest

	// Parse error_on conditions from top-level block
	var errorOnFields []waitFieldModel
	var errorOnConditions []waitConditionModel
	hasErrorOn := false
	if !model.ErrorOn.IsNull() && !model.ErrorOn.IsUnknown() {
		var errOn errorOnModel
		d := model.ErrorOn.As(ctx, &errOn, basetypes.ObjectAsOptions{})
		if !d.HasError() {
			if !errOn.Fields.IsNull() && !errOn.Fields.IsUnknown() {
				errOn.Fields.ElementsAs(ctx, &errorOnFields, false)
			}
			if !errOn.Conditions.IsNull() && !errOn.Conditions.IsUnknown() {
				errOn.Conditions.ElementsAs(ctx, &errorOnConditions, false)
			}
			hasErrorOn = len(errorOnFields) > 0 || len(errorOnConditions) > 0
		}
	}

	// Parse wait block if specified
	hasWait := false
	var wait waitModel
	if !model.Wait.IsNull() && !model.Wait.IsUnknown() {
		d := model.Wait.As(ctx, &wait, basetypes.ObjectAsOptions{})
		if !d.HasError() {
			hasWait = true
		}
	}

	// Nothing to wait for or check
	if !hasWait && !hasErrorOn {
		return nil
	}

	createTimeout, d := model.Timeouts.Create(ctx, 10*time.Minute)
	if d.HasError() {
		return fmt.Errorf("failed to get create timeout: %v", d)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	// Get name/namespace for log messages
	name, _ := extractManifestMetadataField(ctx, model.Manifest, "name")
	namespace, _ := extractManifestMetadataField(ctx, model.Manifest, "namespace")
	apiVersionAny, _ := extractManifestField(ctx, model.Manifest, "apiVersion")
	kindAny, _ := extractManifestField(ctx, model.Manifest, "kind")
	kind := fmt.Sprintf("%v", kindAny)
	apiVersion := fmt.Sprintf("%v", apiVersionAny)

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
	if hasWait && !wait.Rollout.IsNull() && wait.Rollout.ValueBool() {
		log.Printf("[INFO] Waiting for rollout of %s/%s", kind, name)

		rs, err := getResourceInterface()
		if err != nil {
			return fmt.Errorf("failed to set up rollout wait: %w", err)
		}

		waiter := &api.RolloutWaiter{
			Resource:     rs,
			ResourceName: name,
			Logger:       r.providerData.logger,
		}

		if hasErrorOn {
			if err := r.waitWithErrorCheck(
				timeoutCtx,
				rs,
				waiter,
				name,
				errorOnFields,
				errorOnConditions,
			); err != nil {
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
	var waitFields []waitFieldModel
	if hasWait {
		if !wait.Conditions.IsNull() {
			wait.Conditions.ElementsAs(ctx, &conditions, false)
		}
		if !wait.Fields.IsNull() {
			wait.Fields.ElementsAs(ctx, &waitFields, false)
		}
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
		var fieldMatchers []api.FieldMatcher
		for _, f := range waitFields {
			valueType := "eq"
			if !f.ValueType.IsNull() && f.ValueType.ValueString() != "" {
				valueType = f.ValueType.ValueString()
			}

			p, pErr := api.FieldPathToTftypesPath(f.Key.ValueString())
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

			fieldMatchers = append(fieldMatchers, api.FieldMatcher{
				Path:         p,
				ValueMatcher: re,
			})
		}

		// Build condition matchers
		var conditionMatchers []api.ConditionMatcher
		for _, c := range conditions {
			conditionMatchers = append(conditionMatchers, api.ConditionMatcher{
				Type:   c.Type.ValueString(),
				Status: c.Status.ValueString(),
			})
		}

		// Create waiter
		waiter := api.NewResourceWaiterFromConfig(
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
		if hasErrorOn {
			err = r.waitWithErrorCheck(
				timeoutCtx,
				rs,
				waiter,
				name,
				errorOnFields,
				errorOnConditions,
			)
		} else {
			err = waiter.Wait(timeoutCtx)
		}

		if err != nil {
			return fmt.Errorf("failed to wait for conditions: %w", err)
		}

		log.Printf("[INFO] Conditions/fields met for %s/%s", kind, name)
	} else if hasErrorOn {
		// error_on conditions without any wait block — check once
		rs, err := getResourceInterface()
		if err != nil {
			return fmt.Errorf("failed to set up error_on check: %w", err)
		}

		if err := checkErrorOnConditions(
			ctx,
			rs,
			name,
			errorOnFields,
			errorOnConditions,
		); err != nil {
			return err
		}
	}

	return nil
}

// checkErrorOnConditions checks error_on field and condition matchers against the current resource state.
// Returns an error if any error condition is matched.
func checkErrorOnConditions(
	ctx context.Context,
	rs dynamic.ResourceInterface,
	name string,
	errorFields []waitFieldModel,
	errorConditions []waitConditionModel,
) error {
	res, err := rs.Get(ctx, name, meta_v1.GetOptions{})
	if err != nil {
		// Can't check, don't fail
		return nil //nolint:nilerr
	}

	yamlJSON, err := res.MarshalJSON()
	if err != nil {
		// Can't check, don't fail
		return nil //nolint:nilerr
	}

	jsonStr := string(yamlJSON)

	// Check field matchers
	for _, f := range errorFields {
		key := f.Key.ValueString()
		value := f.Value.ValueString()
		valueType := "regex"
		if !f.ValueType.IsNull() && f.ValueType.ValueString() != "" {
			valueType = f.ValueType.ValueString()
		}

		v := gojsonq.New().FromString(jsonStr).Find(key)
		if v == nil {
			continue
		}

		stringVal := fmt.Sprintf("%v", v)
		var matched bool
		if valueType == "eq" {
			matched = stringVal == value
		} else {
			matched, err = regexp.MatchString(value, stringVal)
			if err != nil {
				return fmt.Errorf("error_on: invalid regex %q: %w", value, err)
			}
		}

		if matched {
			return fmt.Errorf(
				"error condition met for %s: field %s=%s matched pattern %s",
				name, key, stringVal, value,
			)
		}
	}

	// Check condition matchers
	for _, c := range errorConditions {
		condType := c.Type.ValueString()
		condStatus := c.Status.ValueString()

		conditionsVal := gojsonq.New().FromString(jsonStr).Find("status.conditions")
		if conditionsVal == nil {
			continue
		}

		condSlice, ok := conditionsVal.([]any)
		if !ok {
			continue
		}

		for _, cond := range condSlice {
			condMap, ok := cond.(map[string]any)
			if !ok {
				continue
			}
			t, _ := condMap["type"].(string)
			s, _ := condMap["status"].(string)
			if (condType == "" || t == condType) && (condStatus == "" || s == condStatus) {
				reason, _ := condMap["reason"].(string)
				message, _ := condMap["message"].(string)
				return fmt.Errorf(
					"error condition met for %s: condition type=%s status=%s reason=%s message=%s",
					name, t, s, reason, message,
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
	waiter api.Waiter,
	name string,
	errorFields []waitFieldModel,
	errorConditions []waitConditionModel,
) error {
	errCh := make(chan error, 1)

	// Run the waiter in a goroutine
	go func() {
		errCh <- waiter.Wait(ctx)
	}()

	// Check error_on conditions periodically while waiter runs
	ticker := time.NewTicker(api.WaiterSleepTime)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			return err
		case <-ticker.C:
			if err := checkErrorOnConditions(
				ctx,
				rs,
				name,
				errorFields,
				errorConditions,
			); err != nil {
				return err
			}
		case <-ctx.Done():
			return fmt.Errorf("%s timed out waiting for resource", name)
		}
	}
}

// readManifest reads a resource from Kubernetes and populates model state.
// It tries OpenAPI-aware reading first for better type fidelity, falling back
// to basic reading if OpenAPI resolution fails.
func (r *manifestResource) readManifest(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// Try OpenAPI-aware read if provider data is available
	if r.providerData != nil {
		apiVersionAny, _ := extractManifestField(ctx, model.Manifest, "apiVersion")
		kindAny, _ := extractManifestField(ctx, model.Manifest, "kind")
		apiVersion := fmt.Sprintf("%v", apiVersionAny)
		kind := fmt.Sprintf("%v", kindAny)
		if apiVersion != "" && kind != "" {
			gvk := k8sschema.FromAPIVersionAndKind(apiVersion, kind)

			objectType, typeHints, err := r.providerData.TFTypeFromOpenAPI(ctx, gvk, false)
			if err == nil && objectType != nil {
				if readErr := r.readManifestWithOpenAPI(
					ctx,
					model,
					objectType,
					typeHints,
				); readErr != nil {
					// If OpenAPI read fails, fall back to basic read
					log.Printf(
						"[DEBUG] OpenAPI read failed, falling back to basic read: %v",
						readErr,
					)
					return r.readManifestV2(ctx, model)
				}
				return nil
			}
			log.Printf("[DEBUG] Could not resolve OpenAPI type for %s: %v", gvk.String(), err)
		}
	}

	return r.readManifestV2(ctx, model)
}

// readManifestWithOpenAPI reads a resource using OpenAPI type resolution for better type fidelity.
func (r *manifestResource) readManifestWithOpenAPI(
	ctx context.Context,
	model *manifestResourceModel,
	objectType tftypes.Type,
	typeHints map[string]string,
) error {
	// Extract name and namespace from manifest
	name, err := extractManifestMetadataField(ctx, model.Manifest, "name")
	if err != nil || name == "" {
		return fmt.Errorf("failed to extract name from manifest.metadata: %w", err)
	}
	namespace, _ := extractManifestMetadataField(ctx, model.Manifest, "namespace")

	// Get GVK
	apiVersionAny, _ := extractManifestField(ctx, model.Manifest, "apiVersion")
	kindAny, _ := extractManifestField(ctx, model.Manifest, "kind")
	apiVersion := fmt.Sprintf("%v", apiVersionAny)
	kind := fmt.Sprintf("%v", kindAny)
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
	// Set ID to Kubernetes UID
	namespace := rawObj.GetNamespace()
	name := rawObj.GetName()
	uid := string(rawObj.GetUID())
	if uid != "" {
		model.ID = types.StringValue(uid)
	} else {
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
	}

	var diags diag.Diagnostics

	// Build manifest from cleaned content (without status and server-side fields)
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
	name, _ := extractManifestMetadataField(ctx, model.Manifest, "name")
	namespace, _ := extractManifestMetadataField(ctx, model.Manifest, "namespace")
	apiVersionAny, _ := extractManifestField(ctx, model.Manifest, "apiVersion")
	kindAny, _ := extractManifestField(ctx, model.Manifest, "kind")
	apiVersion := fmt.Sprintf("%v", apiVersionAny)
	kind := fmt.Sprintf("%v", kindAny)

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
	return api.IsNotFoundError(err)
}
