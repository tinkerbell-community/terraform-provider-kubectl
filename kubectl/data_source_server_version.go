package kubectl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces
var _ datasource.DataSource = &serverVersionDataSource{}
var _ datasource.DataSourceWithConfigure = &serverVersionDataSource{}

// serverVersionDataSource defines the data source implementation
type serverVersionDataSource struct {
	providerData *kubectlProviderData
}

// serverVersionDataSourceModel describes the data source data model
type serverVersionDataSourceModel struct {
	ID         types.String `tfsdk:"id"`
	Version    types.String `tfsdk:"version"`
	Major      types.String `tfsdk:"major"`
	Minor      types.String `tfsdk:"minor"`
	Patch      types.String `tfsdk:"patch"`
	GitVersion types.String `tfsdk:"git_version"`
	GitCommit  types.String `tfsdk:"git_commit"`
	BuildDate  types.String `tfsdk:"build_date"`
	Platform   types.String `tfsdk:"platform"`
}

// NewServerVersionDataSource returns a new server version data source
func NewServerVersionDataSource() datasource.DataSource {
	return &serverVersionDataSource{}
}

// Metadata returns the data source type name
func (d *serverVersionDataSource) Metadata(
	ctx context.Context,
	req datasource.MetadataRequest,
	resp *datasource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_server_version"
}

// Schema defines the data source schema
func (d *serverVersionDataSource) Schema(
	ctx context.Context,
	req datasource.SchemaRequest,
	resp *datasource.SchemaResponse,
) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves the Kubernetes server version information from the connected cluster.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique identifier for this data source (hash of version string)",
			},
			"version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Semantic version string (without pre-release suffix)",
			},
			"major": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Major version number",
			},
			"minor": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Minor version number",
			},
			"patch": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Patch version number",
			},
			"git_version": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Full git version string including pre-release information",
			},
			"git_commit": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Git commit SHA of the server build",
			},
			"build_date": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Build date of the server",
			},
			"platform": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Platform string (e.g., linux/amd64)",
			},
		},
	}
}

// Configure sets the provider data for the data source
func (d *serverVersionDataSource) Configure(
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

// Read executes the data source logic
func (d *serverVersionDataSource) Read(
	ctx context.Context,
	req datasource.ReadRequest,
	resp *datasource.ReadResponse,
) {
	var data serverVersionDataSourceModel

	// Get discovery client
	discoveryClient := d.providerData.MainClientset.Discovery()

	// Invalidate cache to get fresh version info
	discoveryClient.Invalidate()

	// Get server version
	serverVersion, err := discoveryClient.ServerVersion()
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to Get Server Version",
			fmt.Sprintf("Could not retrieve Kubernetes server version: %s", err),
		)
		return
	}

	// Parse semantic version components
	serverSemver := strings.Split(serverVersion.String(), ".")
	var major, minor, patch string

	if len(serverSemver) >= 3 {
		// Clean format: remove 'v' prefix from major, extract patch without pre-release
		major = strings.ReplaceAll(serverSemver[0], "v", "")
		minor = serverSemver[1]
		patch = strings.Split(serverSemver[2], "-")[0]
	} else {
		// Fallback to raw major/minor
		major = serverVersion.Major
		minor = serverVersion.Minor
		patch = ""
	}

	// Version without pre-release suffix
	version := strings.Split(serverVersion.String(), "-")[0]

	// Generate ID from hash of version string
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(serverVersion.String())))

	// Set computed values
	data.ID = types.StringValue(id)
	data.Version = types.StringValue(version)
	data.Major = types.StringValue(major)
	data.Minor = types.StringValue(minor)

	if patch != "" {
		data.Patch = types.StringValue(patch)
	} else {
		data.Patch = types.StringNull()
	}

	data.GitVersion = types.StringValue(serverVersion.GitVersion)
	data.GitCommit = types.StringValue(serverVersion.GitCommit)
	data.BuildDate = types.StringValue(serverVersion.BuildDate)
	data.Platform = types.StringValue(serverVersion.Platform)

	// Set state
	diags := resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}
