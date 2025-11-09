package kubectl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource              = &serverVersionResource{}
	_ resource.ResourceWithConfigure = &serverVersionResource{}
)

// serverVersionResource defines the resource implementation.
type serverVersionResource struct {
	providerData *kubectlProviderData
}

// serverVersionResourceModel describes the resource data model.
type serverVersionResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Triggers   types.Map    `tfsdk:"triggers"`
	Version    types.String `tfsdk:"version"`
	Major      types.String `tfsdk:"major"`
	Minor      types.String `tfsdk:"minor"`
	Patch      types.String `tfsdk:"patch"`
	GitVersion types.String `tfsdk:"git_version"`
	GitCommit  types.String `tfsdk:"git_commit"`
	BuildDate  types.String `tfsdk:"build_date"`
	Platform   types.String `tfsdk:"platform"`
}

// NewServerVersionResource returns a new server version resource.
func NewServerVersionResource() resource.Resource {
	return &serverVersionResource{}
}

// Metadata returns the resource type name.
func (r *serverVersionResource) Metadata(
	ctx context.Context,
	req resource.MetadataRequest,
	resp *resource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_server_version"
}

// Schema defines the resource schema.
func (r *serverVersionResource) Schema(
	ctx context.Context,
	req resource.SchemaRequest,
	resp *resource.SchemaResponse,
) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A resource that retrieves and stores Kubernetes server version information. " +
			"This is a \"null resource\" pattern - it doesn't create anything in Kubernetes but provides " +
			"a way to trigger actions when the server version changes.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique identifier for this resource (hash of version string)",
			},
			"triggers": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
				MarkdownDescription: "Arbitrary map of values that, when changed, will force the resource " +
					"to be recreated. Useful for triggering updates in dependent resources.",
				PlanModifiers: []planmodifier.Map{
					mapplanmodifier.RequiresReplace(),
				},
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

// Configure sets the provider data for the resource.
func (r *serverVersionResource) Configure(
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

// Create creates a new resource (reads server version).
func (r *serverVersionResource) Create(
	ctx context.Context,
	req resource.CreateRequest,
	resp *resource.CreateResponse,
) {
	var plan serverVersionResourceModel

	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read server version
	if err := r.readServerVersion(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Read Server Version",
			fmt.Sprintf("Could not retrieve Kubernetes server version: %s", err),
		)
		return
	}

	// Set state
	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// Read reads the current state of the resource.
func (r *serverVersionResource) Read(
	ctx context.Context,
	req resource.ReadRequest,
	resp *resource.ReadResponse,
) {
	var state serverVersionResourceModel

	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read server version
	if err := r.readServerVersion(ctx, &state); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Read Server Version",
			fmt.Sprintf("Could not retrieve Kubernetes server version: %s", err),
		)
		return
	}

	// Set state
	diags = resp.State.Set(ctx, state)
	resp.Diagnostics.Append(diags...)
}

// Update updates the resource (just reads new version).
func (r *serverVersionResource) Update(
	ctx context.Context,
	req resource.UpdateRequest,
	resp *resource.UpdateResponse,
) {
	var plan serverVersionResourceModel

	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read server version
	if err := r.readServerVersion(ctx, &plan); err != nil {
		resp.Diagnostics.AddError(
			"Failed to Read Server Version",
			fmt.Sprintf("Could not retrieve Kubernetes server version: %s", err),
		)
		return
	}

	// Set state
	diags = resp.State.Set(ctx, plan)
	resp.Diagnostics.Append(diags...)
}

// Delete removes the resource (just clears state, nothing in cluster).
func (r *serverVersionResource) Delete(
	ctx context.Context,
	req resource.DeleteRequest,
	resp *resource.DeleteResponse,
) {
	// No-op: this is a "null resource" pattern
	// State is automatically removed by the framework
}

// readServerVersion is a helper that reads server version and updates the model.
func (r *serverVersionResource) readServerVersion(
	ctx context.Context,
	model *serverVersionResourceModel,
) error {
	// Get discovery client
	discoveryClient := r.providerData.MainClientset.Discovery()

	// Get server version
	serverVersion, err := discoveryClient.ServerVersion()
	if err != nil {
		return err
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
	model.ID = types.StringValue(id)
	model.Version = types.StringValue(version)
	model.Major = types.StringValue(major)
	model.Minor = types.StringValue(minor)

	if patch != "" {
		model.Patch = types.StringValue(patch)
	} else {
		model.Patch = types.StringNull()
	}

	model.GitVersion = types.StringValue(serverVersion.GitVersion)
	model.GitCommit = types.StringValue(serverVersion.GitCommit)
	model.BuildDate = types.StringValue(serverVersion.BuildDate)
	model.Platform = types.StringValue(serverVersion.Platform)

	return nil
}
