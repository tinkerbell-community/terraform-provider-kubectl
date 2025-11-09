package kubectl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &filenameListDataSource{}

// filenameListDataSource defines the data source implementation.
type filenameListDataSource struct{}

// filenameListDataSourceModel describes the data source data model.
type filenameListDataSourceModel struct {
	ID        types.String `tfsdk:"id"`
	Pattern   types.String `tfsdk:"pattern"`
	Matches   types.List   `tfsdk:"matches"`
	Basenames types.List   `tfsdk:"basenames"`
}

// NewFilenameListDataSource returns a new filename list data source.
func NewFilenameListDataSource() datasource.DataSource {
	return &filenameListDataSource{}
}

// Metadata returns the data source type name.
func (d *filenameListDataSource) Metadata(
	ctx context.Context,
	req datasource.MetadataRequest,
	resp *datasource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_filename_list"
}

// Schema defines the data source schema.
func (d *filenameListDataSource) Schema(
	ctx context.Context,
	req datasource.SchemaRequest,
	resp *datasource.SchemaResponse,
) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "This data source uses a file glob pattern to find matching files and returns " +
			"their full paths and basenames as lists.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique identifier for this data source (hash of matched files)",
			},
			"pattern": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Glob pattern to match files (e.g., `manifests/*.yaml`). " +
					"Uses filepath.Glob syntax.",
			},
			"matches": schema.ListAttribute{
				ElementType:         types.StringType,
				Computed:            true,
				MarkdownDescription: "List of file paths that matched the glob pattern, sorted alphabetically.",
			},
			"basenames": schema.ListAttribute{
				ElementType: types.StringType,
				Computed:    true,
				MarkdownDescription: "List of basenames (filenames without directory path) for matched files, " +
					"sorted alphabetically.",
			},
		},
	}
}

// Read executes the data source logic.
func (d *filenameListDataSource) Read(
	ctx context.Context,
	req datasource.ReadRequest,
	resp *datasource.ReadResponse,
) {
	var data filenameListDataSourceModel

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	pattern := data.Pattern.ValueString()

	// Find files matching the glob pattern
	items, err := filepath.Glob(pattern)
	if err != nil {
		resp.Diagnostics.AddError(
			"Glob Pattern Error",
			fmt.Sprintf("Failed to evaluate glob pattern '%s': %s", pattern, err),
		)
		return
	}

	// Sort items for consistent ordering
	sort.Strings(items)

	// Build basenames list and ID hash
	var elemhash string
	basenames := make([]string, len(items))
	for i, s := range items {
		elemhash += strconv.Itoa(i) + s
		basenames[i] = filepath.Base(s)
	}

	// Generate ID from hash of all matched files
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(elemhash)))

	// Convert to Framework types
	matchesList, diags := types.ListValueFrom(ctx, types.StringType, items)
	resp.Diagnostics.Append(diags...)

	basenamesList, diags := types.ListValueFrom(ctx, types.StringType, basenames)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Set computed values
	data.ID = types.StringValue(id)
	data.Matches = matchesList
	data.Basenames = basenamesList

	// Set state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}
