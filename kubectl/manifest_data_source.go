package kubectl

import (
	"context"
	"fmt"
	"log"
	"maps"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/morph"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/payload"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ datasource.DataSource              = &manifestDataSource{}
	_ datasource.DataSourceWithConfigure = &manifestDataSource{}
)

// manifestDataSource defines the data source implementation.
type manifestDataSource struct {
	providerData *kubectlProviderData
}

// manifestDataSourceModel describes the data source data model.
type manifestDataSourceModel struct {
	APIVersion types.String  `tfsdk:"api_version"`
	Kind       types.String  `tfsdk:"kind"`
	Name       types.String  `tfsdk:"name"`
	Namespace  types.String  `tfsdk:"namespace"`
	Object     types.Dynamic `tfsdk:"object"`
}

// NewManifestDataSource returns a new resource data source.
func NewManifestDataSource() datasource.DataSource {
	return &manifestDataSource{}
}

// Metadata returns the data source type name.
func (d *manifestDataSource) Metadata(
	ctx context.Context,
	req datasource.MetadataRequest,
	resp *datasource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_manifest"
}

// Schema defines the data source schema.
func (d *manifestDataSource) Schema(
	ctx context.Context,
	req datasource.SchemaRequest,
	resp *datasource.SchemaResponse,
) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read an existing Kubernetes resource and expose its attributes. " +
			"This data source retrieves the full object from the API server, including server-populated fields.",
		Attributes: map[string]schema.Attribute{
			"api_version": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The resource apiVersion (e.g., `v1`, `apps/v1`).",
			},
			"kind": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The resource kind (e.g., `ConfigMap`, `Deployment`).",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The resource name.",
			},
			"namespace": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The resource namespace. Defaults to `default` for namespaced resources.",
			},
			"object": schema.DynamicAttribute{
				Computed:            true,
				MarkdownDescription: "The full resource object as returned by the API server.",
			},
		},
	}
}

// Configure sets the provider data for the data source.
func (d *manifestDataSource) Configure(
	ctx context.Context,
	req datasource.ConfigureRequest,
	resp *datasource.ConfigureResponse,
) {
	if req.ProviderData == nil {
		return
	}

	providerData, ok := req.ProviderData.(*kubectlProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf(
				"Expected *kubectlProviderData, got: %T. Please report this issue to the provider developers.",
				req.ProviderData,
			),
		)
		return
	}

	d.providerData = providerData
}

// Read executes the data source logic.
func (d *manifestDataSource) Read(
	ctx context.Context,
	req datasource.ReadRequest,
	resp *datasource.ReadResponse,
) {
	var model manifestDataSourceModel

	// Read configuration
	diags := req.Config.Get(ctx, &model)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiVersion := model.APIVersion.ValueString()
	kind := model.Kind.ValueString()
	name := model.Name.ValueString()
	namespace := model.Namespace.ValueString()

	// Get REST mapper and dynamic client
	rm, err := d.providerData.getRestMapper()
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to get RESTMapper client",
			err.Error(),
		)
		return
	}

	client, err := d.providerData.getDynamicClient()
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to get dynamic client",
			err.Error(),
		)
		return
	}

	// Resolve GVR from apiVersion and kind
	gvk := k8sschema.FromAPIVersionAndKind(apiVersion, kind)
	rmapping, err := rm.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to determine resource GroupVersion",
			fmt.Sprintf("Could not resolve REST mapping for %s/%s: %s", apiVersion, kind, err),
		)
		return
	}
	gvr := rmapping.Resource

	// Determine if resource is namespaced
	ns, err := IsResourceNamespaced(gvk, rm)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to determine if resource is namespaced",
			err.Error(),
		)
		return
	}

	rcl := client.Resource(gvr)

	// Resolve OpenAPI type for the resource
	objectType, th, err := d.providerData.TFTypeFromOpenAPI(ctx, gvk, true)
	if err != nil {
		log.Printf(
			"[WARN] Failed to resolve OpenAPI type for %s/%s: %v; falling back to DynamicPseudoType",
			apiVersion,
			kind,
			err,
		)
		objectType = tftypes.DynamicPseudoType
		th = map[string]string{}
	}

	// Merge top-level PartialObjectMetadata fields into objectType so that
	// apiVersion, kind, and metadata are always present regardless of how
	// complete the resource-specific OpenAPI schema is (e.g. CRDs that only
	// define spec). The resource-specific OpenAPI types take precedence.
	if obj, ok := objectType.(tftypes.Object); ok {
		atts := partialObjectMetaTFTypes()
		maps.Copy(atts, obj.AttributeTypes)
		objectType = tftypes.Object{AttributeTypes: atts}
	}

	if ns {
		if namespace == "" {
			namespace = "default"
		}
		result, getErr := rcl.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			resp.Diagnostics.AddError(
				"Failed to read resource",
				fmt.Sprintf(
					"Could not get %s/%s %q in namespace %q: %s",
					apiVersion,
					kind,
					name,
					namespace,
					getErr,
				),
			)
			return
		}

		objectDynamic := d.convertToObject(ctx, result.Object, objectType, th)
		if objectDynamic == nil {
			resp.Diagnostics.AddError(
				"Failed to convert API response",
				fmt.Sprintf(
					"Could not convert %s/%s %q to Terraform value",
					apiVersion,
					kind,
					name,
				),
			)
			return
		}
		model.Object = *objectDynamic
	} else {
		result, getErr := rcl.Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			resp.Diagnostics.AddError(
				"Failed to read resource",
				fmt.Sprintf("Could not get %s/%s %q: %s", apiVersion, kind, name, getErr),
			)
			return
		}

		objectDynamic := d.convertToObject(ctx, result.Object, objectType, th)
		if objectDynamic == nil {
			resp.Diagnostics.AddError(
				"Failed to convert API response",
				fmt.Sprintf(
					"Could not convert %s/%s %q to Terraform value",
					apiVersion,
					kind,
					name,
				),
			)
			return
		}
		model.Object = *objectDynamic
	}

	// Set state
	diags = resp.State.Set(ctx, &model)
	resp.Diagnostics.Append(diags...)
}

// objectMetaTFTypes returns the tftypes.Type representation of metav1.ObjectMeta.
// Fields mirror the JSON serialization of the ObjectMeta struct.
func objectMetaTFTypes() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"name":                       tftypes.String,
		"generateName":               tftypes.String,
		"namespace":                  tftypes.String,
		"selfLink":                   tftypes.String,
		"uid":                        tftypes.String,
		"resourceVersion":            tftypes.String,
		"generation":                 tftypes.Number,
		"creationTimestamp":          tftypes.String,
		"deletionTimestamp":          tftypes.String,
		"deletionGracePeriodSeconds": tftypes.Number,
		"labels":                     tftypes.Map{ElementType: tftypes.String},
		"annotations":                tftypes.Map{ElementType: tftypes.String},
		"ownerReferences": tftypes.List{ElementType: tftypes.Object{
			AttributeTypes: map[string]tftypes.Type{
				"apiVersion":         tftypes.String,
				"blockOwnerDeletion": tftypes.Bool,
				"controller":         tftypes.Bool,
				"kind":               tftypes.String,
				"name":               tftypes.String,
				"uid":                tftypes.String,
			},
		}},
		"finalizers":  tftypes.List{ElementType: tftypes.String},
		"clusterName": tftypes.String,
		"managedFields": tftypes.List{ElementType: tftypes.Object{
			AttributeTypes: map[string]tftypes.Type{
				"manager":    tftypes.String,
				"operation":  tftypes.String,
				"apiVersion": tftypes.String,
				"time":       tftypes.String,
				"fieldsType": tftypes.String,
				"fieldsV1":   tftypes.DynamicPseudoType,
			},
		}},
	}}
}

// partialObjectMetaTFTypes returns the tftypes.Type map for the top-level
// fields of metav1.PartialObjectMetadata (TypeMeta inline + ObjectMeta).
// This is used as a base when merging with a resource-specific OpenAPI type
// to ensure apiVersion, kind, and metadata are always present.
func partialObjectMetaTFTypes() map[string]tftypes.Type {
	return map[string]tftypes.Type{
		"apiVersion": tftypes.String,
		"kind":       tftypes.String,
		"metadata":   objectMetaTFTypes(),
	}
}

// convertToObject converts a raw Kubernetes object map to a types.Dynamic value
// using OpenAPI type information for proper type fidelity.
func (d *manifestDataSource) convertToObject(
	ctx context.Context,
	raw map[string]any,
	objectType tftypes.Type,
	typeHints map[string]string,
) *types.Dynamic {
	// metadata.managedFields has heterogeneous element types across entries
	// (each field manager's fieldsV1 object has different keys) which breaks
	// OpenAPI-based list conversion. Strip it from a shallow copy before typed
	// conversion; we'll re-add it afterwards as an untyped value.
	convRaw := make(map[string]any, len(raw))
	for k, v := range raw {
		convRaw[k] = v
	}
	var savedManagedFields any
	if meta, ok := convRaw["metadata"].(map[string]any); ok {
		metaCopy := make(map[string]any, len(meta))
		for k, v := range meta {
			metaCopy[k] = v
		}
		savedManagedFields = metaCopy["managedFields"]
		delete(metaCopy, "managedFields")
		convRaw["metadata"] = metaCopy
	}

	// Convert the raw object to a tftypes.Value using OpenAPI type
	tfValue, err := payload.ToTFValue(convRaw, objectType, typeHints, tftypes.NewAttributePath())
	if err != nil {
		log.Printf(
			"[WARN] Failed to convert API response with OpenAPI type: %v; falling back to untyped",
			err,
		)
		// Fall back to untyped conversion
		result, d := mapToDynamic(ctx, raw)
		if d.HasError() {
			return nil
		}
		return &result
	}

	// Apply morph.DeepUnknown to fill in unknown values where the schema has them
	tfValue, err = morph.DeepUnknown(objectType, tfValue, tftypes.NewAttributePath())
	if err != nil {
		log.Printf("[WARN] Failed to apply DeepUnknown: %v; falling back to untyped", err)
		result, d := mapToDynamic(ctx, raw)
		if d.HasError() {
			return nil
		}
		return &result
	}

	// Convert unknown values to null (data source reads should have concrete values)
	tfValue = morph.UnknownToNull(tfValue)

	// Convert the tftypes.Value back to a map, then to types.Dynamic
	resultMap, err := payload.FromTFValue(tfValue, nil, tftypes.NewAttributePath())
	if err != nil {
		log.Printf(
			"[WARN] Failed to convert tftypes.Value back to map: %v; falling back to untyped",
			err,
		)
		result, d := mapToDynamic(ctx, raw)
		if d.HasError() {
			return nil
		}
		return &result
	}

	resultContent, ok := resultMap.(map[string]any)
	if !ok {
		log.Printf("[WARN] Expected map[string]any from payload.FromTFValue, got %T", resultMap)
		result, d := mapToDynamic(ctx, raw)
		if d.HasError() {
			return nil
		}
		return &result
	}

	// Re-add managedFields to the result so the attribute is still accessible.
	// It is stored as a raw []any and will be encoded as a Tuple by mapToDynamic
	// since each entry has a different fieldsV1 schema.
	if savedManagedFields != nil {
		if resultMeta, ok := resultContent["metadata"].(map[string]any); ok {
			resultMeta["managedFields"] = savedManagedFields
		}
	}

	result, diags := mapToDynamic(ctx, resultContent)
	if diags.HasError() {
		return nil
	}
	return &result
}
