package kubectl

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/alekc/terraform-provider-kubectl/yaml"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces
var _ datasource.DataSource = &fileDocumentsDataSource{}

// fileDocumentsDataSource defines the data source implementation
type fileDocumentsDataSource struct{}

// fileDocumentsDataSourceModel describes the data source data model
type fileDocumentsDataSourceModel struct {
	ID        types.String `tfsdk:"id"`
	Content   types.String `tfsdk:"content"`
	Documents types.List   `tfsdk:"documents"`
	Manifests types.Map    `tfsdk:"manifests"`
}

// NewFileDocumentsDataSource returns a new file documents data source
func NewFileDocumentsDataSource() datasource.DataSource {
	return &fileDocumentsDataSource{}
}

// Metadata returns the data source type name
func (d *fileDocumentsDataSource) Metadata(
	ctx context.Context,
	req datasource.MetadataRequest,
	resp *datasource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_file_documents"
}

// Schema defines the data source schema
func (d *fileDocumentsDataSource) Schema(
	ctx context.Context,
	req datasource.SchemaRequest,
	resp *datasource.SchemaResponse,
) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Parses a YAML file containing multiple Kubernetes manifests " +
			"(separated by `---`) and splits it into individual documents. " +
			"Returns both a list of documents and a map with resource-based keys.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique identifier for this data source (hash of content)",
			},
			"content": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Raw YAML content containing one or more Kubernetes manifests " +
					"separated by `---`.",
			},
			"documents": schema.ListAttribute{
				ElementType:         types.StringType,
				Computed:            true,
				MarkdownDescription: "List of individual YAML documents extracted from the content.",
			},
			"manifests": schema.MapAttribute{
				ElementType: types.StringType,
				Computed:    true,
				MarkdownDescription: "Map of manifest self-link to YAML content. " +
					"Key format: apiVersion_kind_namespace_name or apiVersion_kind_name for cluster-scoped resources.",
			},
		},
	}
}

// Read executes the data source logic
func (d *fileDocumentsDataSource) Read(
	ctx context.Context,
	req datasource.ReadRequest,
	resp *datasource.ReadResponse,
) {
	var data fileDocumentsDataSourceModel

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	content := data.Content.ValueString()

	// Split YAML into documents
	documents, err := yaml.SplitMultiDocumentYAML(content)
	if err != nil {
		resp.Diagnostics.AddError(
			"YAML Parse Error",
			fmt.Sprintf("Failed to split multi-document YAML: %s", err),
		)
		return
	}

	// Parse each document and build manifests map
	manifests := make(map[string]string)
	for _, doc := range documents {
		manifest, err := yaml.ParseYAML(doc)
		if err != nil {
			resp.Diagnostics.AddError(
				"Manifest Parse Error",
				fmt.Sprintf("Failed to parse YAML as Kubernetes manifest: %s", err),
			)
			return
		}

		parsed, err := manifest.AsYAML()
		if err != nil {
			resp.Diagnostics.AddError(
				"Manifest Conversion Error",
				fmt.Sprintf("Failed to convert manifest to YAML: %s", err),
			)
			return
		}

		selfLink := manifest.GetSelfLink()
		if _, exists := manifests[selfLink]; exists {
			resp.Diagnostics.AddError(
				"Duplicate Manifest",
				fmt.Sprintf("Duplicate manifest found with ID: %s", selfLink),
			)
			return
		}

		manifests[selfLink] = parsed
	}

	// Generate ID from hash of content
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))

	// Convert to Framework types
	docsList, diags := types.ListValueFrom(ctx, types.StringType, documents)
	resp.Diagnostics.Append(diags...)

	manifestsMap, diags := types.MapValueFrom(ctx, types.StringType, manifests)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Set computed values
	data.ID = types.StringValue(id)
	data.Documents = docsList
	data.Manifests = manifestsMap

	// Set state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}
