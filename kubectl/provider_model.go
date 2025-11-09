package kubectl

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// providerModel describes the provider configuration data model.
type providerModel struct {
	ApplyRetryCount       types.Int64  `tfsdk:"apply_retry_count"`
	Host                  types.String `tfsdk:"host"`
	Username              types.String `tfsdk:"username"`
	Password              types.String `tfsdk:"password"`
	Insecure              types.Bool   `tfsdk:"insecure"`
	ClientCertificate     types.String `tfsdk:"client_certificate"`
	ClientKey             types.String `tfsdk:"client_key"`
	ClusterCACertificate  types.String `tfsdk:"cluster_ca_certificate"`
	ConfigPath            types.String `tfsdk:"config_path"`
	ConfigPaths           types.List   `tfsdk:"config_paths"`
	ConfigContext         types.String `tfsdk:"config_context"`
	ConfigContextAuthInfo types.String `tfsdk:"config_context_auth_info"`
	ConfigContextCluster  types.String `tfsdk:"config_context_cluster"`
	Token                 types.String `tfsdk:"token"`
	ProxyURL              types.String `tfsdk:"proxy_url"`
	LoadConfigFile        types.Bool   `tfsdk:"load_config_file"`
	TLSServerName         types.String `tfsdk:"tls_server_name"`
	Exec                  types.List   `tfsdk:"exec"` // List of execModel objects
}

// execModel describes the exec configuration nested block.
type execModel struct {
	APIVersion types.String `tfsdk:"api_version"`
	Command    types.String `tfsdk:"command"`
	Env        types.Map    `tfsdk:"env"`
	Args       types.List   `tfsdk:"args"`
}
