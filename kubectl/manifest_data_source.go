package kubectl

import (
	"context"
	"fmt"
	"log"

	"github.com/alekc/terraform-provider-kubectl/kubectl/morph"
	"github.com/alekc/terraform-provider-kubectl/kubectl/payload"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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

	// Fetch the resource from the API server
	var res *metav1.PartialObjectMetadata
	_ = res // suppress unused warning

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

// convertToObject converts a raw Kubernetes object map to a types.Dynamic value
// using OpenAPI type information for proper type fidelity.
func (d *manifestDataSource) convertToObject(
	ctx context.Context,
	raw map[string]any,
	objectType tftypes.Type,
	typeHints map[string]string,
) *types.Dynamic {
	// Convert the raw object to a tftypes.Value using OpenAPI type
	tfValue, err := payload.ToTFValue(raw, objectType, typeHints, tftypes.NewAttributePath())
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

	result, diags := mapToDynamic(ctx, resultContent)
	if diags.HasError() {
		return nil
	}
	return &result
}
