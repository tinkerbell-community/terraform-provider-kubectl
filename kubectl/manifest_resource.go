//nolint:forcetypeassert
package kubectl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp-oss/terraform-provider-kubectl/kubectl/api"
	"github.com/hashicorp-oss/terraform-provider-kubectl/kubectl/morph"
	"github.com/hashicorp-oss/terraform-provider-kubectl/kubectl/payload"
	"github.com/hashicorp-oss/terraform-provider-kubectl/kubectl/util"
	"github.com/hashicorp-oss/terraform-provider-kubectl/kubectl/yaml"
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
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                    = &manifestResource{}
	_ resource.ResourceWithConfigure       = &manifestResource{}
	_ resource.ResourceWithIdentity        = &manifestResource{}
	_ resource.ResourceWithImportState     = &manifestResource{}
	_ resource.ResourceWithModifyPlan      = &manifestResource{}
	_ resource.ResourceWithUpgradeIdentity = &manifestResource{}
	_ resource.ResourceWithValidateConfig  = &manifestResource{}
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
	ID           types.String   `tfsdk:"id"`
	Manifest     types.Dynamic  `tfsdk:"manifest"`
	ManifestWo   types.Dynamic  `tfsdk:"manifest_wo"`
	Status       types.Dynamic  `tfsdk:"status"`
	Object       types.Dynamic  `tfsdk:"object"`
	Fields       types.Object   `tfsdk:"fields"`
	Delete       types.Object   `tfsdk:"delete"`
	Wait         types.Object   `tfsdk:"wait"`
	Error        types.Object   `tfsdk:"error"`
	FieldManager types.Object   `tfsdk:"field_manager"`
	Timeouts     timeouts.Value `tfsdk:"timeouts"`
}

// waitModel describes the wait attribute.
type waitModel struct {
	Rollout    types.Bool `tfsdk:"rollout"`
	Fields     types.List `tfsdk:"fields"`
	Conditions types.List `tfsdk:"conditions"`
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

// errorModel describes the error attribute.
// Error conditions are checked continuously while waiting for success conditions.
// If any error condition matches, the apply fails immediately.
type errorModel struct {
	Fields     types.List `tfsdk:"fields"`
	Conditions types.List `tfsdk:"conditions"`
}

// fieldsModel describes the fields attribute.
type fieldsModel struct {
	Computed  types.List `tfsdk:"computed"`
	Immutable types.List `tfsdk:"immutable"`
}

// deleteModel describes the delete attribute.
type deleteModel struct {
	Skip    types.Bool   `tfsdk:"skip"`
	Cascade types.String `tfsdk:"cascade"`
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

// waitBlockAttrTypes returns the attribute types map for the wait attribute.
func waitBlockAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"rollout": types.BoolType,
		"fields": types.ListType{ElemType: types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"key":        types.StringType,
				"value":      types.StringType,
				"value_type": types.StringType,
			},
		}},
		"conditions": types.ListType{ElemType: types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"type":   types.StringType,
				"status": types.StringType,
			},
		}},
	}
}

// errorAttrTypes returns the attribute types map for the error attribute.
func errorAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"fields": types.ListType{ElemType: types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"key":        types.StringType,
				"value":      types.StringType,
				"value_type": types.StringType,
			},
		}},
		"conditions": types.ListType{ElemType: types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"type":   types.StringType,
				"status": types.StringType,
			},
		}},
	}
}

// fieldsAttrTypes returns the attribute types map for the fields attribute.
func fieldsAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"computed":  types.ListType{ElemType: types.StringType},
		"immutable": types.ListType{ElemType: types.StringType},
	}
}

// deleteAttrTypes returns the attribute types map for the delete attribute.
func deleteAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"skip":    types.BoolType,
		"cascade": types.StringType,
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

// UpgradeIdentity returns identity upgraders for prior identity schema versions.
// Version 0 represents the default state before identity was introduced.
func (r *manifestResource) UpgradeIdentity(
	ctx context.Context,
) map[int64]resource.IdentityUpgrader {
	return map[int64]resource.IdentityUpgrader{
		0: {
			IdentityUpgrader: func(ctx context.Context, req resource.UpgradeIdentityRequest, resp *resource.UpgradeIdentityResponse) {
				// Version 0 had no identity data. Set empty strings so the
				// framework sees a non-null Raw value. The next Read will
				// overwrite these with real values via setResponseIdentity.
				resp.Diagnostics.Append(resp.Identity.Set(ctx, manifestIdentityModel{
					APIVersion: types.StringValue(""),
					Kind:       types.StringValue(""),
					Name:       types.StringValue(""),
					Namespace:  types.StringValue(""),
				})...)
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
			"fields": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Configure field tracking options.",
				Attributes: map[string]schema.Attribute{
					"computed": schema.ListAttribute{
						ElementType: types.StringType,
						Optional:    true,
						MarkdownDescription: "List of manifest fields whose values may be altered by the API server during apply. " +
							"Defaults to: `[\"metadata.annotations\", \"metadata.labels\"]`",
					},
					"immutable": schema.ListAttribute{
						ElementType: types.StringType,
						Optional:    true,
						MarkdownDescription: "List of manifest field paths that are immutable after creation. " +
							"If any of these fields change, the resource will be replaced (destroyed and re-created). " +
							"Uses dot-separated paths (e.g., `spec.selector`).",
					},
				},
			},
			"manifest_wo": schema.DynamicAttribute{
				Optional:  true,
				WriteOnly: true,
				MarkdownDescription: "Write-only manifest overrides that are deep merged into `manifest` before applying to the Kubernetes API. " +
					"Values are not persisted in Terraform state. Use the same structure as `manifest` — " +
					"only include the fields you want to inject as write-only (e.g., secrets, passwords). " +
					"Example: `manifest_wo = { data = { password = base64encode(\"secret\") } }`",
			},
			"delete": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Configure deletion behavior.",
				Attributes: map[string]schema.Attribute{
					"skip": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(false),
						MarkdownDescription: "If true, skip deletion of the resource when destroying. Default: false",
					},
					"cascade": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Cascade mode for deletion: Background or Foreground. Default: Background",
						Validators: []validator.String{
							stringvalidator.OneOf(
								string(meta_v1.DeletePropagationBackground),
								string(meta_v1.DeletePropagationForeground),
							),
						},
					},
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
					"fields": schema.ListNestedAttribute{
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
					"conditions": schema.ListNestedAttribute{
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
			"error": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Define error conditions that are checked continuously while waiting for success conditions. " +
					"If any error condition matches, the apply fails immediately. " +
					"Use this to detect error states such as CrashLoopBackOff or Failed status.",
				Attributes: map[string]schema.Attribute{
					"fields": schema.ListNestedAttribute{
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
					"conditions": schema.ListNestedAttribute{
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
					"You may only set one of rollout, fields, or conditions in a wait block.",
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

	// Read manifest_wo from config (write-only attributes must be read from config).
	var manifestWoMap map[string]any
	var woKeys []string
	manifestWo := extractManifestWoFromConfig(ctx, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !manifestWo.IsNull() && !manifestWo.IsUnknown() {
		manifestWoMap, _ = dynamicToMap(ctx, manifestWo)
		if manifestWoMap != nil {
			woKeys = extractLeafPaths(manifestWoMap, "")
		}
	}

	// Persist the WO key paths in private state so Read can mask them from object.
	if len(woKeys) > 0 {
		keysJSON, err := json.Marshal(woKeys)
		if err == nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, "fields_wo_keys", keysJSON)...)
		}
	}

	// Store a checksum of manifest_wo so ModifyPlan can detect value changes.
	if manifestWoMap != nil {
		if checksum := computeManifestWoChecksum(manifestWoMap); checksum != "" {
			checksumJSON, _ := json.Marshal(checksum)
			resp.Diagnostics.Append(
				resp.Private.SetKey(ctx, "manifest_wo_checksum", checksumJSON)...)
		}
	}
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
		err := r.applyManifest(createCtx, &plan, manifestWoMap)
		var ece *MatchingConditionError
		if errors.As(err, &ece) {
			return backoff.Permanent(err)
		}
		return err
	}, backoffStrategy)
	if err != nil {
		// If the failure is an error condition match, save partial state so
		// that the resource is tracked and ModifyPlan can schedule replacement
		// on the next run.
		var ece *MatchingConditionError
		if errors.As(err, &ece) {
			diags = resp.State.Set(ctx, plan)
			resp.Diagnostics.Append(diags...)
			resp.Diagnostics.Append(
				resp.Private.SetKey(ctx, "error_condition_met", []byte("true"))...)
		}
		resp.Diagnostics.AddError(
			"Failed to Create Resource",
			fmt.Sprintf("Could not apply manifest: %s", err),
		)
		return
	}

	// Mask write-only field paths from object so sensitive values don't persist in state.
	if len(woKeys) > 0 {
		if err := maskFieldsWoPaths(ctx, &plan, woKeys); err != nil {
			log.Printf("[WARN] Failed to mask manifest_wo paths in object: %v", err)
		}
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
	if req.ClientCapabilities.DeferralAllowed {
		resp.Deferred = &resource.Deferred{}
	}

	var state manifestResourceModel

	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Load WO key paths from private state so we can mask them in object.
	var woKeys []string
	keysJSON, d := req.Private.GetKey(ctx, "fields_wo_keys")
	resp.Diagnostics.Append(d...)
	if len(keysJSON) > 0 {
		if err := json.Unmarshal(keysJSON, &woKeys); err != nil {
			log.Printf("[WARN] Failed to unmarshal fields_wo_keys from private state: %v", err)
			woKeys = nil
		}
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

	// Mask write-only paths in object so sensitive values don't leak into state.
	if len(woKeys) > 0 {
		if err := maskFieldsWoPaths(ctx, &state, woKeys); err != nil {
			log.Printf("[WARN] Failed to mask fields_wo paths in object: %v", err)
		}
	}

	// Reconcile manifest: keep only attributes from prior state to avoid
	// perpetual diffs from server-generated fields (uid, creationTimestamp, etc.)
	state.Manifest = reconcileDynamicWithPrior(ctx, priorManifest, state.Manifest)

	// For immutable fields, restore the prior config value (from before Read)
	// instead of keeping the API server's current value. This ensures that
	// ModifyPlan's immutable check compares the user's current config against
	// their PRIOR config — not against the server value. Without this,
	// external changes to immutable fields (e.g., a controller scaling
	// replicas) would cause a false RequiresReplace because the plan value
	// (from config) differs from the state value (from server).
	preserveImmutableFieldsFromPrior(ctx, &state, priorManifest)

	// Check error conditions from the error block. If any error condition
	// matches the live resource state, mark the resource for replacement on
	// the next plan via a private state flag.
	errorConditionMet := false
	if r.providerData != nil && !state.Error.IsNull() && !state.Error.IsUnknown() {
		var errOn errorModel
		if d := state.Error.As(ctx, &errOn, basetypes.ObjectAsOptions{}); !d.HasError() {
			var errorOnFields []waitFieldModel
			var errorOnConditions []waitConditionModel
			if !errOn.Fields.IsNull() && !errOn.Fields.IsUnknown() {
				errOn.Fields.ElementsAs(ctx, &errorOnFields, false)
			}
			if !errOn.Conditions.IsNull() && !errOn.Conditions.IsUnknown() {
				errOn.Conditions.ElementsAs(ctx, &errorOnConditions, false)
			}
			if len(errorOnFields) > 0 || len(errorOnConditions) > 0 {
				apiVersionAny, _ := extractManifestField(ctx, state.Manifest, "apiVersion")
				kindAny, _ := extractManifestField(ctx, state.Manifest, "kind")
				name, _ := extractManifestMetadataField(ctx, state.Manifest, "name")
				namespace, _ := extractManifestMetadataField(ctx, state.Manifest, "namespace")

				rs, err := r.getResourceInterface(
					ctx,
					fmt.Sprintf("%v", apiVersionAny),
					fmt.Sprintf("%v", kindAny),
					namespace,
				)
				if err == nil {
					if err := checkErrorOnConditions(
						ctx,
						rs,
						name,
						errorOnFields,
						errorOnConditions,
					); err != nil {
						errorConditionMet = true
						resp.Diagnostics.AddWarning(
							"Error Condition Detected",
							fmt.Sprintf("Resource will be replaced on next apply: %s", err),
						)
					}
				}
			}
		}
	}

	// Set state
	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)

	// Propagate private state (WO keys must survive across refreshes).
	if len(keysJSON) > 0 {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, "fields_wo_keys", keysJSON)...)
	}

	// Store error_condition_met flag in private state for ModifyPlan.
	if errorConditionMet {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, "error_condition_met", []byte("true"))...)
	} else {
		// Clear any previously set flag so the resource is no longer replaced.
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, "error_condition_met", []byte("false"))...)
	}

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

	// Read manifest_wo from config (write-only attributes must be read from config).
	var manifestWoMap map[string]any
	var woKeys []string
	manifestWo := extractManifestWoFromConfig(ctx, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !manifestWo.IsNull() && !manifestWo.IsUnknown() {
		manifestWoMap, _ = dynamicToMap(ctx, manifestWo)
		if manifestWoMap != nil {
			woKeys = extractLeafPaths(manifestWoMap, "")
		}
	}

	// Persist the WO key paths in private state so Read can mask them from object.
	if len(woKeys) > 0 {
		keysJSON, err := json.Marshal(woKeys)
		if err == nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, "fields_wo_keys", keysJSON)...)
		}
	}

	// Store a checksum of manifest_wo so ModifyPlan can detect value changes.
	if manifestWoMap != nil {
		if checksum := computeManifestWoChecksum(manifestWoMap); checksum != "" {
			checksumJSON, _ := json.Marshal(checksum)
			resp.Diagnostics.Append(
				resp.Private.SetKey(ctx, "manifest_wo_checksum", checksumJSON)...)
		}
	}
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
		err := r.applyManifest(updateCtx, &plan, manifestWoMap)
		var ece *MatchingConditionError
		if errors.As(err, &ece) {
			return backoff.Permanent(err)
		}
		return err
	}, backoffStrategy)
	if err != nil {
		// If the failure is an error condition match, save the current state so
		// that the resource is tracked and ModifyPlan can schedule replacement
		// on the next run.
		var ece *MatchingConditionError
		if errors.As(err, &ece) {
			diags = resp.State.Set(ctx, plan)
			resp.Diagnostics.Append(diags...)
			resp.Diagnostics.Append(
				resp.Private.SetKey(ctx, "error_condition_met", []byte("true"))...)
		}
		resp.Diagnostics.AddError(
			"Failed to Update Resource",
			fmt.Sprintf("Could not apply manifest: %s", err),
		)
		return
	}

	// Mask write-only field paths from object so sensitive values don't persist in state.
	if len(woKeys) > 0 {
		if err := maskFieldsWoPaths(ctx, &plan, woKeys); err != nil {
			log.Printf("[WARN] Failed to mask manifest_wo paths in object: %v", err)
		}
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

	// Check if delete.skip mode
	if !state.Delete.IsNull() && !state.Delete.IsUnknown() {
		var del deleteModel
		d := state.Delete.As(ctx, &del, basetypes.ObjectAsOptions{})
		if !d.HasError() && !del.Skip.IsNull() && del.Skip.ValueBool() {
			log.Printf("[INFO] delete.skip is set, skipping deletion")
			return
		}
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
		ID:           types.StringValue(req.ID),
		Manifest:     manifestDynamic,
		ManifestWo:   types.DynamicNull(),
		Status:       types.DynamicNull(),
		Object:       types.DynamicNull(),
		Fields:       types.ObjectNull(fieldsAttrTypes()),
		Delete:       types.ObjectNull(deleteAttrTypes()),
		Wait:         types.ObjectNull(waitBlockAttrTypes()),
		Error:        types.ObjectNull(errorAttrTypes()),
		FieldManager: types.ObjectNull(fieldManagerBlockAttrTypes()),
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

	// If Read detected an error condition on the live resource, force replacement.
	if req.Private != nil {
		ecmData, d := req.Private.GetKey(ctx, "error_condition_met")
		resp.Diagnostics.Append(d...)
		if len(ecmData) > 0 && string(ecmData) == "true" {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("manifest"))
			// Clear the flag so it doesn't persist after the replacement plan is executed.
			if resp.Private != nil {
				resp.Diagnostics.Append(
					resp.Private.SetKey(ctx, "error_condition_met", []byte("false"))...)
			}
		}
	}

	// Check fields.immutable — if any listed field changed, require replacement
	if !plan.Fields.IsNull() && !plan.Fields.IsUnknown() {
		var fm fieldsModel
		if d := plan.Fields.As(ctx, &fm, basetypes.ObjectAsOptions{}); !d.HasError() {
			if !fm.Immutable.IsNull() && !fm.Immutable.IsUnknown() {
				var immutablePaths []string
				fm.Immutable.ElementsAs(ctx, &immutablePaths, false)
				if len(immutablePaths) > 0 {
					planMap, _ := dynamicToMap(ctx, plan.Manifest)
					stateMap, _ := dynamicToMap(ctx, state.Manifest)
					if planMap != nil && stateMap != nil {
						for _, fieldPath := range immutablePaths {
							atp, err := api.FieldPathToTftypesPath(fieldPath)
							if err != nil {
								resp.Diagnostics.AddAttributeWarning(
									path.Root("fields"),
									"Invalid immutable field path",
									fmt.Sprintf("Could not parse path %q: %s", fieldPath, err),
								)
								continue
							}
							planVal, planOk := walkMapByTFPath(planMap, atp)
							stateVal, stateOk := walkMapByTFPath(stateMap, atp)
							if planOk && stateOk &&
								fmt.Sprintf("%v", planVal) != fmt.Sprintf("%v", stateVal) {
								resp.RequiresReplace = append(
									resp.RequiresReplace,
									path.Root("manifest"),
								)
								break
							}
						}
					}
				}
			}
		}
	}

	// Attempt OpenAPI type resolution for computed field handling
	apiVersionStr := fmt.Sprintf("%v", planAPIVersion)
	kindStr := fmt.Sprintf("%v", planKind)
	if r.providerData != nil && apiVersionStr != "" && kindStr != "" {
		r.modifyPlanWithOpenAPI(ctx, &plan, &state, resp)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Reconcile computed fields at the map level. This is a fallback that
	// works regardless of whether OpenAPI type resolution succeeded. When
	// modifyPlanWithOpenAPI returned early (CRD not yet in discovery cache,
	// non-structural type, priorObj conversion failure, etc.), plan.Manifest
	// still has raw config values for computed fields. Replacing them with
	// state values prevents false diffs and payload overwrites.
	reconcileComputedFieldsInPlan(ctx, &plan, &state)

	// Determine if user-provided manifest changed between plan and state.
	// Compare at the map[string]any level so that container-type differences
	// (ObjectValue vs MapValue, TupleValue vs ListValue) don't cause false
	// positives. types.Dynamic.Equal is type-sensitive — two Dynamics with
	// identical content but different container wrappers are not Equal.
	// dynamicToMap normalizes both sides to plain Go types, making the
	// comparison purely semantic.
	planMap, planDiags := dynamicToMap(ctx, plan.Manifest)
	stateMap, stateDiags := dynamicToMap(ctx, state.Manifest)
	var hasChange bool
	if planDiags.HasError() || stateDiags.HasError() {
		hasChange = true // cannot compare — assume changed
	} else {
		hasChange = !reflect.DeepEqual(planMap, stateMap)
	}

	// Detect manifest_wo value changes via checksum comparison. Write-only values
	// are absent from plan/state, so we hash config values and compare with the
	// checksum stored in private state during the last apply.
	if !hasChange && req.Private != nil {
		var woDiags diag.Diagnostics
		manifestWo := extractManifestWoFromConfig(ctx, req.Config, &woDiags)
		if !woDiags.HasError() {
			var newChecksum string
			if !manifestWo.IsNull() && !manifestWo.IsUnknown() {
				woMap, _ := dynamicToMap(ctx, manifestWo)
				if woMap != nil {
					newChecksum = computeManifestWoChecksum(woMap)
				}
			}
			storedChecksumJSON, d := req.Private.GetKey(ctx, "manifest_wo_checksum")
			resp.Diagnostics.Append(d...)
			var storedChecksum string
			if len(storedChecksumJSON) > 0 {
				_ = json.Unmarshal(storedChecksumJSON, &storedChecksum)
			}
			if newChecksum != storedChecksum {
				hasChange = true
			}
		}
	}

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

// reconcileComputedFieldsInPlan replaces computed field values in the plan
// manifest with values from the state manifest. The state carries server
// values (via reconcileDynamicWithPrior), so this effectively makes computed
// fields "fire and forget." This is a map-level fallback that works
// regardless of whether modifyPlanWithOpenAPI succeeded.
//
// Immutable fields are NOT reconciled here — they are handled differently.
// preserveImmutableFieldsFromPrior (called in Read) keeps prior config
// values in state, so plan == state naturally when the user hasn't changed
// their config. Reconciling immutable fields here would overwrite user
// config changes and cause Terraform to reject the plan.
//
// The operation is idempotent: if modifyPlanWithOpenAPI already replaced the
// values, plan and state already match and nothing changes.
func reconcileComputedFieldsInPlan(
	ctx context.Context,
	plan *manifestResourceModel,
	state *manifestResourceModel,
) {
	// Only applies on Update — on Create there is no prior state.
	if state.Manifest.IsNull() || state.Manifest.IsUnknown() {
		return
	}

	// Parse computed field paths from fields config.
	var computedPaths [][]string
	if !plan.Fields.IsNull() && !plan.Fields.IsUnknown() {
		var fm fieldsModel
		if d := plan.Fields.As(ctx, &fm, basetypes.ObjectAsOptions{}); !d.HasError() {
			if !fm.Computed.IsNull() && !fm.Computed.IsUnknown() {
				var cfList []string
				fm.Computed.ElementsAs(ctx, &cfList, false)
				for _, cf := range cfList {
					computedPaths = append(computedPaths, strings.Split(cf, "."))
				}
			}
		}
	}
	// Apply default computed fields when none specified.
	if len(computedPaths) == 0 {
		computedPaths = [][]string{
			{"metadata", "annotations"},
			{"metadata", "labels"},
		}
	}

	planMap, planDiags := dynamicToMap(ctx, plan.Manifest)
	if planDiags.HasError() || planMap == nil {
		return
	}
	stateMap, stateDiags := dynamicToMap(ctx, state.Manifest)
	if stateDiags.HasError() || stateMap == nil {
		return
	}

	modified := false
	for _, p := range computedPaths {
		stateVal, ok := getNestedMapValue(stateMap, p)
		if !ok {
			continue
		}
		planVal, ok := getNestedMapValue(planMap, p)
		if !ok {
			continue
		}
		if !reflect.DeepEqual(planVal, stateVal) {
			setNestedMapValue(planMap, p, stateVal)
			modified = true
		}
	}

	if !modified {
		return
	}

	// Only apply the reconciliation if it makes the plan match state entirely.
	// If non-computed fields also differ (e.g., user added/changed other
	// attributes in the same cycle), applying the reconciliation would create
	// a hybrid plan that matches neither config nor prior, causing Terraform
	// to reject it with "Provider produced invalid plan."
	if !reflect.DeepEqual(planMap, stateMap) {
		return
	}

	dyn, d := mapToDynamicPreservingTypes(ctx, planMap, plan.Manifest)
	if !d.HasError() {
		plan.Manifest = dyn
	}
}

// preserveImmutableFieldsFromPrior restores prior config values for immutable
// fields in state.Manifest after reconcileDynamicWithPrior has replaced them
// with API server values. This ensures ModifyPlan's immutable check compares
// the user's CURRENT config against their PRIOR config — not against the
// server value — so external changes to immutable fields don't cause a false
// RequiresReplace.
func preserveImmutableFieldsFromPrior(
	ctx context.Context,
	state *manifestResourceModel,
	priorManifest types.Dynamic,
) {
	if state.Fields.IsNull() || state.Fields.IsUnknown() {
		return
	}
	if priorManifest.IsNull() || priorManifest.IsUnknown() {
		return
	}

	var fm fieldsModel
	if d := state.Fields.As(ctx, &fm, basetypes.ObjectAsOptions{}); d.HasError() {
		return
	}
	if fm.Immutable.IsNull() || fm.Immutable.IsUnknown() {
		return
	}

	var immutablePaths []string
	fm.Immutable.ElementsAs(ctx, &immutablePaths, false)
	if len(immutablePaths) == 0 {
		return
	}

	priorMap, d := dynamicToMap(ctx, priorManifest)
	if d.HasError() || priorMap == nil {
		return
	}
	stateMap, d := dynamicToMap(ctx, state.Manifest)
	if d.HasError() || stateMap == nil {
		return
	}

	modified := false
	for _, fp := range immutablePaths {
		parts := strings.Split(fp, ".")
		priorVal, ok := getNestedMapValue(priorMap, parts)
		if !ok {
			continue
		}
		stateVal, ok := getNestedMapValue(stateMap, parts)
		if !ok {
			continue
		}
		if !reflect.DeepEqual(priorVal, stateVal) {
			setNestedMapValue(stateMap, parts, priorVal)
			modified = true
		}
	}

	if !modified {
		return
	}

	dyn, d := mapToDynamicPreservingTypes(ctx, stateMap, state.Manifest)
	if !d.HasError() {
		state.Manifest = dyn
	}
}

// getNestedMapValue retrieves a value from a nested map following the given
// path segments (e.g. ["spec", "userData"]).
func getNestedMapValue(m map[string]any, path []string) (any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	current := any(m)
	for i, key := range path {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		val, exists := cm[key]
		if !exists {
			return nil, false
		}
		if i == len(path)-1 {
			return val, true
		}
		current = val
	}
	return nil, false
}

// setNestedMapValue sets a value in a nested map at the given path.
func setNestedMapValue(m map[string]any, path []string, value any) {
	current := m
	for _, key := range path[:len(path)-1] {
		next, ok := current[key]
		if !ok {
			return
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			return
		}
		current = nextMap
	}
	current[path[len(path)-1]] = value
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
	if !plan.Fields.IsNull() && !plan.Fields.IsUnknown() {
		var fm fieldsModel
		if d := plan.Fields.As(ctx, &fm, basetypes.ObjectAsOptions{}); !d.HasError() {
			if !fm.Computed.IsNull() && !fm.Computed.IsUnknown() {
				var cfList []string
				fm.Computed.ElementsAs(ctx, &cfList, false)
				for _, cf := range cfList {
					atp, err := api.FieldPathToTftypesPath(cf)
					if err != nil {
						log.Printf("[DEBUG] Could not parse fields.computed path %s: %v", cf, err)
						continue
					}
					computedFields[atp.String()] = atp
				}
			}
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
			// Strip metadata.managedFields before OpenAPI conversion.
			// managedFields entries have heterogeneous fieldsV1 schemas (each field
			// manager tracks different fields), causing sliceToTFListValue to fail
			// on type-homogeneity checks. It is not needed for plan diffing.
			objMapCopy := make(map[string]any, len(objectMap))
			for k, v := range objectMap {
				objMapCopy[k] = v
			}
			if meta, ok := objMapCopy["metadata"].(map[string]any); ok {
				metaCopy := make(map[string]any, len(meta))
				for k, v := range meta {
					metaCopy[k] = v
				}
				delete(metaCopy, "managedFields")
				objMapCopy["metadata"] = metaCopy
			}

			priorObjTfValue, err := payload.ToTFValue(
				objMapCopy, objectType, hints, tftypes.NewAttributePath(),
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
					// Computed fields: always carry forward the server value
					// from the prior object. The purpose of marking a field as
					// computed is to let the API server own it — remote changes
					// must never trigger an update diff.
					if isComputed {
						nowVal, restPath, walkErr := tftypes.WalkAttributePath(priorObj, ap)
						if walkErr == nil && len(restPath.Steps()) == 0 {
							return nowVal.(tftypes.Value), nil
						}
						// Not in prior object — keep config value as-is
						return v, nil
					}

					// This is a value from current configuration — include it in the plan
					hasChanged := false

					// Check if value changed between prior and proposed manifest
					wasCfg, _, _ := tftypes.WalkAttributePath(priorMan, ap)
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

	// Replace unknowns with null before converting back to map.
	// FromTFValue cannot handle unknown values (it returns an error), so computed
	// fields that were marked UnknownValue by the transform above must be nulled
	// here. Kubernetes null fields are stripped by api.MapRemoveNulls during apply,
	// so no computed-field value is incorrectly sent to the server.
	completePlan = morph.UnknownToNull(completePlan)

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

	// Reconcile back to the user-configured (compact) form.
	//
	// resultContent contains all OpenAPI fields — computed fields are null (from
	// UnknownToNull above) and in the hasPrior path, computed fields carry forward
	// their actual server values. If we stored this full map in plan.Manifest:
	//
	//   - state.Manifest (restored from plannedManifest in applyManifest) = null for
	//     computed fields
	//   - plan.Manifest on the next Update = actual priorObj values for computed fields
	//   - hasChange = always true, even with no user changes → status/object always
	//     "(known after apply)"
	//
	// By filtering to only the keys the user explicitly configured (proposedManifestMap),
	// plan.Manifest and state.Manifest stay in the same compact shape across
	// Plan → Apply → Plan cycles, making hasChange a stable comparison.
	//
	// Fields the user didn't configure (annotations, labels, server-injected fields)
	// are available via the `object` computed attribute on the resource.
	filteredContent := deepReconcileMaps(proposedManifestMap, resultContent)

	// Guard: when the result differs from config, verify it matches prior
	// state. Carrying forward computed server values while adding or removing
	// non-computed fields creates a hybrid plan matching neither config nor
	// state — which Terraform rejects with "Provider produced invalid plan."
	// In that case, skip the modification entirely and let the plan stay as
	// config. The reconcileComputedFieldsInPlan fallback (which has its own
	// guard) will also skip, so the plan safely equals config.
	if !reflect.DeepEqual(filteredContent, proposedManifestMap) {
		stateManifestMap, _ := dynamicToMap(ctx, state.Manifest)
		if stateManifestMap == nil || !reflect.DeepEqual(filteredContent, stateManifestMap) {
			return
		}
	}

	// Update plan manifest from reconciled (compact) result, preserving the
	// original config types (Map vs Object, List vs Tuple) so the planned
	// value matches the config value for Terraform's plan validation.
	manifestDynamic, d := mapToDynamicPreservingTypes(ctx, filteredContent, plan.Manifest)
	resp.Diagnostics.Append(d...)
	if !resp.Diagnostics.HasError() {
		plan.Manifest = manifestDynamic
	}

	// NOTE: status and object are handled by the centralized change detection
	// in ModifyPlan, not here.
}

// getResourceInterface returns a dynamic.ResourceInterface for the resource
// identified by apiVersion, kind, and optional namespace. Reusable in Read,
// applyManifest, and anywhere else a dynamic client handle is needed.
func (r *manifestResource) getResourceInterface(
	_ context.Context,
	apiVersion, kind, namespace string,
) (dynamic.ResourceInterface, error) {
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

// applyManifest applies the manifest to Kubernetes using server-side apply,
// then handles wait conditions. error_on conditions are checked continuously
// while waiting for success conditions to be met.
func (r *manifestResource) applyManifest(
	ctx context.Context,
	model *manifestResourceModel,
	manifestWoMap map[string]any,
) error {
	// Save user-provided manifest before readManifest overwrites it.
	// The API response includes server-generated fields (uid, creationTimestamp, etc.)
	// which would change the types.Dynamic object type and cause Terraform's
	// "wrong final value type" consistency check to fail.
	plannedManifest := model.Manifest

	// Deep merge manifest_wo into manifest before applying.
	if len(manifestWoMap) > 0 {
		manifestMap, d := dynamicToMap(ctx, model.Manifest)
		if d.HasError() {
			return fmt.Errorf("failed to convert manifest to map for manifest_wo merge: %v", d)
		}
		deepMergeMaps(manifestMap, manifestWoMap)
		injected, d := mapToDynamic(ctx, manifestMap)
		if d.HasError() {
			return fmt.Errorf("failed to convert manifest back to dynamic after merge: %v", d)
		}
		model.Manifest = injected
	}

	// Build unstructured object from Dynamic attributes
	uo, diags := buildUnstructured(
		ctx,
		model,
	)
	if diags.HasError() {
		return fmt.Errorf("failed to build unstructured: %v", diags)
	}

	log.Printf("[DEBUG] Applying Kubernetes resource: %s/%s", uo.GetKind(), uo.GetName())

	// Get field manager configuration
	fieldManagerName := "Terraform"
	forceConflicts := false

	if !model.FieldManager.IsNull() {
		var fm fieldManagerModel
		diags := model.FieldManager.As(ctx, &fm, basetypes.ObjectAsOptions{})
		if diags.HasError() {
			return fmt.Errorf("failed to parse field_manager: %v", diags)
		}
		if !fm.Name.IsNull() {
			fieldManagerName = fm.Name.ValueString()
		}
		if !fm.ForceConflicts.IsNull() {
			forceConflicts = fm.ForceConflicts.ValueBool()
		}
	}

	// Create REST client for this resource type
	manifest := yaml.NewFromUnstructured(uo)
	mainClientset, err := r.providerData.getMainClientset()
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	restCfg, err := r.providerData.getRestConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes REST config: %w", err)
	}
	restClient := api.GetRestClientFromUnstructured(
		ctx,
		manifest,
		mainClientset,
		restCfg,
	)
	if restClient.Error != nil {
		return fmt.Errorf("failed to create kubernetes rest client: %w", restClient.Error)
	}

	// Remove nulls from the object before applying
	content := uo.UnstructuredContent()
	cleanedContent := api.MapRemoveNulls(content)
	uo.SetUnstructuredContent(cleanedContent)

	// Marshal to JSON for server-side apply
	jsonData, err := uo.MarshalJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal to JSON: %w", err)
	}

	// Apply using server-side apply (Patch with ApplyPatchType)
	result, err := restClient.ResourceInterface.Patch(
		ctx,
		uo.GetName(),
		k8stypes.ApplyPatchType,
		jsonData,
		meta_v1.PatchOptions{
			FieldManager: fieldManagerName,
			Force:        &forceConflicts,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to apply manifest: %w", err)
	}

	log.Printf("[DEBUG] Successfully applied resource: %s/%s (UID: %s)",
		result.GetKind(), result.GetName(), result.GetUID())

	// Read back to populate computed fields (ID, status, object) from server response
	if err := r.readManifest(ctx, model); err != nil {
		return fmt.Errorf("failed to read manifest after apply: %w", err)
	}

	// Restore user-provided manifest to maintain type compatibility with plan
	model.Manifest = plannedManifest

	// Parse error conditions from top-level attribute
	var errorOnFields []waitFieldModel
	var errorOnConditions []waitConditionModel
	hasErrorOn := false
	if !model.Error.IsNull() && !model.Error.IsUnknown() {
		var errOn errorModel
		d := model.Error.As(ctx, &errOn, basetypes.ObjectAsOptions{})
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

	// Handle rollout wait using RolloutWaiter (polymorphichelpers)
	if hasWait && !wait.Rollout.IsNull() && wait.Rollout.ValueBool() {
		log.Printf("[INFO] Waiting for rollout of %s/%s", kind, name)

		rs, err := r.getResourceInterface(ctx, apiVersion, kind, namespace)
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

		rs, err := r.getResourceInterface(ctx, apiVersion, kind, namespace)
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
		rs, err := r.getResourceInterface(ctx, apiVersion, kind, namespace)
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
			return &MatchingConditionError{
				Msg: fmt.Sprintf(
					"error condition met for %s: field %s=%s matched pattern %s",
					name, key, stringVal, value,
				),
			}
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
				return &MatchingConditionError{
					Msg: fmt.Sprintf(
						"error condition met for %s: condition type=%s status=%s reason=%s message=%s",
						name,
						t,
						s,
						reason,
						message,
					),
				}
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

// readManifestV2 reads a Kubernetes resource and populates the state using Dynamic attributes.
func (r *manifestResource) readManifestV2(
	ctx context.Context,
	model *manifestResourceModel,
) error {
	// Extract name and namespace from manifest.metadata
	name, err := extractManifestMetadataField(ctx, model.Manifest, "name")
	if err != nil || name == "" {
		return fmt.Errorf("failed to extract name from manifest.metadata: %w", err)
	}

	namespace, _ := extractManifestMetadataField(ctx, model.Manifest, "namespace")

	// Extract apiVersion and kind from manifest
	apiVersionAny, _ := extractManifestField(ctx, model.Manifest, "apiVersion")
	kindAny, _ := extractManifestField(ctx, model.Manifest, "kind")
	apiVersion := fmt.Sprintf("%v", apiVersionAny)
	kind := fmt.Sprintf("%v", kindAny)

	log.Printf("[DEBUG] Reading Kubernetes resource: %s/%s (namespace: %s)",
		kind, name, namespace)

	// Create REST client
	// Build a minimal unstructured object for the REST client
	tempUo := &meta_v1_unstruct.Unstructured{}
	tempUo.SetAPIVersion(apiVersion)
	tempUo.SetKind(kind)
	tempUo.SetName(name)
	if namespace != "" {
		tempUo.SetNamespace(namespace)
	}

	manifest := yaml.NewFromUnstructured(tempUo)
	mainClientset, err := r.providerData.getMainClientset()
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	restCfg, err := r.providerData.getRestConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes REST config: %w", err)
	}
	restClient := api.GetRestClientFromUnstructured(
		ctx,
		manifest,
		mainClientset,
		restCfg,
	)
	if restClient.Error != nil {
		return fmt.Errorf("failed to create kubernetes rest client: %w", restClient.Error)
	}

	// Get the resource from Kubernetes
	result, err := restClient.ResourceInterface.Get(
		ctx,
		name,
		meta_v1.GetOptions{},
	)
	if err != nil {
		return err
	}

	log.Printf("[DEBUG] Successfully read resource: %s/%s (UID: %s)",
		result.GetKind(), result.GetName(), result.GetUID())

	// Populate state from the resource
	diags := setStateFromUnstructured(ctx, result, model)
	if diags.HasError() {
		return fmt.Errorf("failed to set state: %v", diags)
	}

	return nil
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
	ns, err := util.IsResourceNamespaced(gvk, rm)
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

	// Deep copy the result so that RemoveServerSideFields (which mutates in-place)
	// does not strip computed metadata from the original object. The original is
	// passed to setStateFromOpenAPIResult to populate model.Object with the full
	// server response including uid, creationTimestamp, generation, resourceVersion, etc.
	fullResult := result.DeepCopy()

	// Remove server-side fields from the copy for manifest processing
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

	// Pass fullResult (unmutated) so object retains computed metadata fields
	return r.setStateFromOpenAPIResult(ctx, resultContent, fullResult, model)
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
	// Extract name and namespace from manifest.metadata
	name, err := extractManifestMetadataField(ctx, model.Manifest, "name")
	if err != nil || name == "" {
		return fmt.Errorf("failed to extract name from manifest.metadata: %w", err)
	}

	namespace, _ := extractManifestMetadataField(ctx, model.Manifest, "namespace")

	// Extract apiVersion and kind from manifest
	apiVersionAny, _ := extractManifestField(ctx, model.Manifest, "apiVersion")
	kindAny, _ := extractManifestField(ctx, model.Manifest, "kind")
	apiVersion := fmt.Sprintf("%v", apiVersionAny)
	kind := fmt.Sprintf("%v", kindAny)

	log.Printf("[DEBUG] Deleting Kubernetes resource: %s/%s (namespace: %s)",
		kind, name, namespace)

	// Build minimal unstructured for REST client
	uo := &meta_v1_unstruct.Unstructured{}
	uo.SetAPIVersion(apiVersion)
	uo.SetKind(kind)
	uo.SetName(name)
	if namespace != "" {
		uo.SetNamespace(namespace)
	}

	manifest := yaml.NewFromUnstructured(uo)
	mainClientset, err := r.providerData.getMainClientset()
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	restCfg, err := r.providerData.getRestConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes REST config: %w", err)
	}
	restClient := api.GetRestClientFromUnstructured(
		ctx,
		manifest,
		mainClientset,
		restCfg,
	)
	if restClient.Error != nil {
		return fmt.Errorf("failed to create kubernetes rest client: %w", restClient.Error)
	}

	// Determine delete propagation policy
	propagationPolicy := meta_v1.DeletePropagationBackground
	if !model.Delete.IsNull() && !model.Delete.IsUnknown() {
		var del deleteModel
		if d := model.Delete.As(ctx, &del, basetypes.ObjectAsOptions{}); !d.HasError() {
			if !del.Cascade.IsNull() {
				switch del.Cascade.ValueString() {
				case string(meta_v1.DeletePropagationForeground):
					propagationPolicy = meta_v1.DeletePropagationForeground
				case string(meta_v1.DeletePropagationBackground):
					propagationPolicy = meta_v1.DeletePropagationBackground
				}
			}
		}
	}

	// Delete the resource
	err = restClient.ResourceInterface.Delete(
		ctx,
		name,
		meta_v1.DeleteOptions{
			PropagationPolicy: &propagationPolicy,
		},
	)

	// Ignore NotFound errors (resource already deleted)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete manifest: %w", err)
	}

	if k8s_errors.IsNotFound(err) {
		log.Printf("[DEBUG] Resource already deleted: %s/%s", kind, name)
	} else {
		log.Printf("[DEBUG] Successfully deleted resource: %s/%s", kind, name)
	}

	// Wait for deletion to complete
	name, _ = extractManifestMetadataField(ctx, model.Manifest, "name")
	namespace, _ = extractManifestMetadataField(ctx, model.Manifest, "namespace")
	apiVersionAny, _ = extractManifestField(ctx, model.Manifest, "apiVersion")
	kindAny, _ = extractManifestField(ctx, model.Manifest, "kind")
	apiVersion = fmt.Sprintf("%v", apiVersionAny)
	kind = fmt.Sprintf("%v", kindAny)

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

// MatchingConditionError is a sentinel error indicating that an error condition
// (from the error block) was matched. It wraps the original message so callers
// can use errors.As to distinguish it from other failures.
type MatchingConditionError struct {
	Msg string
}

func (e *MatchingConditionError) Error() string {
	return e.Msg
}

func isNotFoundError(err error) bool {
	return api.IsNotFoundError(err)
}
