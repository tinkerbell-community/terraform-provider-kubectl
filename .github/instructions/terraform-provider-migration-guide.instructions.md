# Terraform Provider Migration: SDK v2 to Plugin Framework

## Overview

This guide provides detailed instructions for migrating terraform-provider-kubectl from SDK v2 to the Plugin Framework. This migration enables improved type safety, better validation patterns, and enhanced state management while maintaining backward compatibility with existing Terraform configurations.

## Prerequisites

- **Current Framework**: Terraform Plugin SDK v2
- **Target Framework**: Terraform Plugin Framework v1.4+
- **Go Version**: 1.22+
- **Kubernetes Client**: kubernetes v0.31.0

## Key Documentation References

- [Plugin Framework Overview](https://developer.hashicorp.com/terraform/plugin/framework)
- [Migration Guide from SDK v2](https://developer.hashicorp.com/terraform/plugin/framework/migrating)
- [Provider Framework Tutorial](https://developer.hashicorp.com/terraform/tutorials/providers-plugin-framework)
- [Schema Concepts](https://developer.hashicorp.com/terraform/plugin/framework/handling-data/schemas)
- [Resource Implementation](https://developer.hashicorp.com/terraform/plugin/framework/resources)
- [Data Source Implementation](https://developer.hashicorp.com/terraform/plugin/framework/data-sources)

## Migration Strategy

Given the kubectl provider's unique characteristics (YAML-based manifest management, dynamic resource handling), we recommend a **phased migration approach** using provider muxing:

1. **Phase 1**: Infrastructure setup with muxing support
2. **Phase 2**: Migrate data sources (lower risk)
3. **Phase 3**: Migrate `kubectl_manifest` resource (core functionality)
4. **Phase 4**: Complete transition and remove SDK v2 dependencies

## Project Structure Reorganization

### Current Structure (SDK v2):
```
terraform-provider-kubectl/
├── kubernetes/
│   ├── provider.go
│   ├── resource_kubectl_manifest.go
│   ├── data_source_*.go
│   └── structures.go
├── yaml/
│   ├── manifest.go
│   ├── parser.go
│   └── splitter.go
├── flatten/
└── main.go
```

### Target Structure (Plugin Framework):
```
terraform-provider-kubectl/
├── kubectl/                           # NEW: Framework provider
│   ├── provider.go                    # Framework provider implementation
│   ├── provider_model.go              # Provider configuration model
│   ├── resource_manifest.go           # Framework manifest resource
│   ├── resource_server_version.go     # Framework server version resource
│   ├── data_source_file_documents.go  # Framework data sources
│   ├── data_source_path_documents.go
│   ├── data_source_filename_list.go
│   ├── data_source_server_version.go
│   └── util/
│       ├── conversion.go              # Type conversion helpers
│       └── kubernetes.go              # Kubernetes client utilities
├── kubernetes/                        # EXISTING: SDK v2 (during transition)
│   └── ... (existing files)
├── yaml/                              # SHARED: Keep existing
├── flatten/                           # SHARED: Keep existing
└── main.go                            # UPDATED: Mux configuration
```

## Step 1: Update Dependencies

Update `go.mod` to include Plugin Framework packages:

```go
module github.com/alekc/terraform-provider-kubectl

go 1.22.0

require (
    // NEW: Plugin Framework dependencies
    github.com/hashicorp/terraform-plugin-framework v1.4.2
    github.com/hashicorp/terraform-plugin-framework-validators v0.12.0
    github.com/hashicorp/terraform-plugin-go v0.19.0
    github.com/hashicorp/terraform-plugin-mux v0.12.0
    
    // EXISTING: Keep during transition
    github.com/hashicorp/terraform-plugin-sdk/v2 v2.29.0
    
    // EXISTING: Keep all kubernetes dependencies
    k8s.io/api v0.31.0
    k8s.io/apimachinery v0.31.0
    k8s.io/cli-runtime v0.31.0
    k8s.io/client-go v0.31.0
    k8s.io/kube-aggregator v0.31.0
    k8s.io/kubectl v0.31.0
    
    // ... other existing dependencies
)
```

Run:
```bash
go get github.com/hashicorp/terraform-plugin-framework@v1.4.2
go get github.com/hashicorp/terraform-plugin-framework-validators@v0.12.0
go get github.com/hashicorp/terraform-plugin-mux@v0.12.0
go mod tidy
```

## Step 2: Create Muxed Provider Entry Point

Update `main.go` to support both SDK v2 and Framework providers simultaneously:

```go
package main

import (
    "context"
    "flag"
    "log"

    "github.com/hashicorp/terraform-plugin-framework/providerserver"
    "github.com/hashicorp/terraform-plugin-mux/tf5to6server"
    "github.com/hashicorp/terraform-plugin-mux/tf6muxserver"
    
    // SDK v2 provider (existing)
    sdkProvider "github.com/alekc/terraform-provider-kubectl/kubernetes"
    // Plugin Framework provider (new)
    frameworkProvider "github.com/alekc/terraform-provider-kubectl/kubectl"
)

//go:generate go tool github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate -provider-name kubectl

func main() {
    var debug bool
    
    flag.BoolVar(
        &debug,
        "debug",
        false,
        "set to true to run the provider with support for debuggers like delve",
    )
    flag.Parse()

    ctx := context.Background()

    // Upgrade SDK v2 provider to protocol version 6
    upgradedSdkProvider, err := tf5to6server.UpgradeServer(
        ctx,
        sdkProvider.Provider().GRPCProvider,
    )
    if err != nil {
        log.Fatal(err)
    }

    // Create muxed provider combining SDK v2 and Framework
    muxServer, err := tf6muxserver.NewMuxServer(
        ctx,
        upgradedSdkProvider,
        providerserver.NewProtocol6(frameworkProvider.New()),
    )
    if err != nil {
        log.Fatal(err)
    }

    // Serve the muxed provider
    err = muxServer.Serve(
        ctx,
        &tf6muxserver.ServeOpts{
            Address: "registry.terraform.io/alekc/kubectl",
            Debug:   debug,
        },
    )
    if err != nil {
        log.Fatal(err)
    }
}
```

## Step 3: Create Framework Provider Foundation

### 3.1 Provider Configuration Model

Create `kubectl/provider_model.go`:

```go
package kubectl

import (
    "github.com/hashicorp/terraform-plugin-framework/types"
)

// providerModel describes the provider configuration data model
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

// execModel describes the exec configuration nested block
type execModel struct {
    APIVersion types.String `tfsdk:"api_version"`
    Command    types.String `tfsdk:"command"`
    Env        types.Map    `tfsdk:"env"`
    Args       types.List   `tfsdk:"args"`
}
```

### 3.2 Provider Implementation

Create `kubectl/provider.go`:

```go
package kubectl

import (
    "context"
    "fmt"

    "github.com/hashicorp/terraform-plugin-framework/datasource"
    "github.com/hashicorp/terraform-plugin-framework/provider"
    "github.com/hashicorp/terraform-plugin-framework/provider/schema"
    "github.com/hashicorp/terraform-plugin-framework/resource"
    "github.com/hashicorp/terraform-plugin-framework/types"
    
    "k8s.io/client-go/kubernetes"
    restclient "k8s.io/client-go/rest"
    aggregator "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
)

// Ensure the implementation satisfies the provider.Provider interface
var _ provider.Provider = &kubectlProvider{}

// kubectlProvider defines the provider implementation
type kubectlProvider struct {
    version string
}

// kubectlProviderData contains the configured Kubernetes clients
type kubectlProviderData struct {
    MainClientset       *kubernetes.Clientset
    RestConfig          *restclient.Config
    AggregatorClientset *aggregator.Clientset
    ApplyRetryCount     int64
}

// New returns a new provider instance
func New() provider.Provider {
    return &kubectlProvider{}
}

// Metadata returns the provider type name
func (p *kubectlProvider) Metadata(
    ctx context.Context,
    req provider.MetadataRequest,
    resp *provider.MetadataResponse,
) {
    resp.TypeName = "kubectl"
    resp.Version = p.version
}

// Schema defines the provider-level schema for configuration data
func (p *kubectlProvider) Schema(
    ctx context.Context,
    req provider.SchemaRequest,
    resp *provider.SchemaResponse,
) {
    resp.Schema = schema.Schema{
        Description: "The kubectl provider enables Terraform to deploy and manage " +
            "Kubernetes resources using YAML manifests.",
        Attributes: map[string]schema.Attribute{
            "apply_retry_count": schema.Int64Attribute{
                Optional: true,
                Description: "Defines the number of attempts any create/update " +
                    "action will take. Defaults to 1.",
            },
            "host": schema.StringAttribute{
                Optional: true,
                Description: "The hostname (in form of URI) of Kubernetes master. " +
                    "Can be set with KUBE_HOST environment variable.",
            },
            "username": schema.StringAttribute{
                Optional: true,
                Description: "The username to use for HTTP basic authentication " +
                    "when accessing the Kubernetes master endpoint. " +
                    "Can be set with KUBE_USER environment variable.",
            },
            "password": schema.StringAttribute{
                Optional:  true,
                Sensitive: true,
                Description: "The password to use for HTTP basic authentication " +
                    "when accessing the Kubernetes master endpoint. " +
                    "Can be set with KUBE_PASSWORD environment variable.",
            },
            "insecure": schema.BoolAttribute{
                Optional: true,
                Description: "Whether server should be accessed without verifying " +
                    "the TLS certificate. Can be set with KUBE_INSECURE environment variable.",
            },
            "client_certificate": schema.StringAttribute{
                Optional: true,
                Description: "PEM-encoded client certificate for TLS authentication. " +
                    "Can be set with KUBE_CLIENT_CERT_DATA environment variable.",
            },
            "client_key": schema.StringAttribute{
                Optional:  true,
                Sensitive: true,
                Description: "PEM-encoded client certificate key for TLS authentication. " +
                    "Can be set with KUBE_CLIENT_KEY_DATA environment variable.",
            },
            "cluster_ca_certificate": schema.StringAttribute{
                Optional: true,
                Description: "PEM-encoded root certificates bundle for TLS authentication. " +
                    "Can be set with KUBE_CLUSTER_CA_CERT_DATA environment variable.",
            },
            "config_path": schema.StringAttribute{
                Optional: true,
                Description: "Path to the kube config file. Defaults to ~/.kube/config. " +
                    "Can be set with KUBE_CONFIG, KUBECONFIG, or KUBE_CONFIG_PATH environment variables.",
            },
            "config_paths": schema.ListAttribute{
                ElementType: types.StringType,
                Optional:    true,
                Description: "A list of paths to kube config files. " +
                    "Can be set with KUBE_CONFIG_PATHS environment variable.",
            },
            "config_context": schema.StringAttribute{
                Optional: true,
                Description: "Context to use in kubeconfig. " +
                    "Can be set with KUBE_CTX environment variable.",
            },
            "config_context_auth_info": schema.StringAttribute{
                Optional: true,
                Description: "Authentication info context of the kube config. " +
                    "Can be set with KUBE_CTX_AUTH_INFO environment variable.",
            },
            "config_context_cluster": schema.StringAttribute{
                Optional: true,
                Description: "Cluster context of the kube config. " +
                    "Can be set with KUBE_CTX_CLUSTER environment variable.",
            },
            "token": schema.StringAttribute{
                Optional:  true,
                Sensitive: true,
                Description: "Token to authenticate a service account. " +
                    "Can be set with KUBE_TOKEN environment variable.",
            },
            "proxy_url": schema.StringAttribute{
                Optional: true,
                Description: "URL to the proxy to be used for all API requests. " +
                    "Can be set with KUBE_PROXY_URL environment variable.",
            },
            "load_config_file": schema.BoolAttribute{
                Optional: true,
                Description: "Load local kubeconfig. " +
                    "Can be set with KUBE_LOAD_CONFIG_FILE environment variable. Defaults to true.",
            },
            "tls_server_name": schema.StringAttribute{
                Optional: true,
                Description: "Server name passed to the server for SNI and is used " +
                    "in the client to check server certificates against. " +
                    "Can be set with KUBE_TLS_SERVER_NAME environment variable.",
            },
        },
        Blocks: map[string]schema.Block{
            "exec": schema.ListNestedBlock{
                Description: "Configuration for exec-based authentication to Kubernetes API.",
                NestedObject: schema.NestedBlockObject{
                    Attributes: map[string]schema.Attribute{
                        "api_version": schema.StringAttribute{
                            Required:    true,
                            Description: "API version to use for exec authentication.",
                        },
                        "command": schema.StringAttribute{
                            Required:    true,
                            Description: "Command to execute for authentication.",
                        },
                        "env": schema.MapAttribute{
                            ElementType: types.StringType,
                            Optional:    true,
                            Description: "Environment variables to set when executing the command.",
                        },
                        "args": schema.ListAttribute{
                            ElementType: types.StringType,
                            Optional:    true,
                            Description: "Arguments to pass to the command.",
                        },
                    },
                },
            },
        },
    }
}

// Configure prepares the Kubernetes client for data sources and resources
func (p *kubectlProvider) Configure(
    ctx context.Context,
    req provider.ConfigureRequest,
    resp *provider.ConfigureResponse,
) {
    var config providerModel

    diags := req.Config.Get(ctx, &config)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    // Initialize Kubernetes configuration
    // This would call a utility function similar to the SDK v2 version
    cfg, err := initializeKubernetesConfig(ctx, config)
    if err != nil {
        resp.Diagnostics.AddError(
            "Unable to Create Kubernetes Client",
            fmt.Sprintf("Failed to initialize Kubernetes configuration: %s", err),
        )
        return
    }

    // Set QPS and Burst for better performance
    cfg.QPS = 100.0
    cfg.Burst = 100

    // Create Kubernetes clientset
    k8sClient, err := kubernetes.NewForConfig(cfg)
    if err != nil {
        resp.Diagnostics.AddError(
            "Unable to Create Kubernetes Client",
            fmt.Sprintf("Failed to create Kubernetes clientset: %s", err),
        )
        return
    }

    // Create aggregator clientset
    aggClient, err := aggregator.NewForConfig(cfg)
    if err != nil {
        resp.Diagnostics.AddError(
            "Unable to Create Aggregator Client",
            fmt.Sprintf("Failed to create aggregator clientset: %s", err),
        )
        return
    }

    // Determine retry count
    retryCount := int64(1)
    if !config.ApplyRetryCount.IsNull() {
        retryCount = config.ApplyRetryCount.ValueInt64()
    }

    // Create provider data structure
    providerData := &kubectlProviderData{
        MainClientset:       k8sClient,
        RestConfig:          cfg,
        AggregatorClientset: aggClient,
        ApplyRetryCount:     retryCount,
    }

    // Make provider data available to resources and data sources
    resp.DataSourceData = providerData
    resp.ResourceData = providerData
}

// Resources returns the resources implemented by this provider
func (p *kubectlProvider) Resources(ctx context.Context) []func() resource.Resource {
    return []func() resource.Resource{
        NewManifestResource,
        NewServerVersionResource,
    }
}

// DataSources returns the data sources implemented by this provider
func (p *kubectlProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
    return []func() datasource.DataSource{
        NewFileDocumentsDataSource,
        NewPathDocumentsDataSource,
        NewFilenameListDataSource,
        NewServerVersionDataSource,
    }
}
```

### 3.3 Kubernetes Configuration Utility

Create `kubectl/util/kubernetes.go`:

```go
package util

import (
    "bytes"
    "context"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "github.com/hashicorp/terraform-plugin-framework/types"
    "github.com/mitchellh/go-homedir"
    
    restclient "k8s.io/client-go/rest"
    "k8s.io/client-go/tools/clientcmd"
    clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
    apimachineryschema "k8s.io/apimachinery/pkg/runtime/schema"
)

// InitializeKubernetesConfig creates a Kubernetes REST config from provider configuration
func InitializeKubernetesConfig(ctx context.Context, config interface{}) (*restclient.Config, error) {
    // Implementation similar to SDK v2 version but using Framework types
    // This should handle:
    // - Config file loading
    // - Context overrides
    // - Direct authentication (token, username/password, certificates)
    // - Exec-based authentication
    // - Environment variable fallbacks
    
    overrides := &clientcmd.ConfigOverrides{}
    loader := &clientcmd.ClientConfigLoadingRules{}
    
    // Parse config paths
    // Handle exec authentication
    // Apply overrides for host, certs, tokens, etc.
    // ...
    
    cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, overrides)
    cfg, err := cc.ClientConfig()
    if err != nil {
        return nil, fmt.Errorf("failed to load kubernetes config: %w", err)
    }
    
    return cfg, nil
}
```

## Step 4: Migrate kubectl_manifest Resource

This is the core resource. Create `kubectl/resource_manifest.go`:

```go
package kubectl

import (
    "context"
    "fmt"
    "time"

    "github.com/hashicorp/terraform-plugin-framework/resource"
    "github.com/hashicorp/terraform-plugin-framework/resource/schema"
    "github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
    "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
    "github.com/hashicorp/terraform-plugin-framework/types"
    
    "github.com/alekc/terraform-provider-kubectl/yaml"
)

// Ensure provider defined types fully satisfy framework interfaces
var (
    _ resource.Resource                = &manifestResource{}
    _ resource.ResourceWithConfigure   = &manifestResource{}
    _ resource.ResourceWithImportState = &manifestResource{}
)

// manifestResource defines the resource implementation
type manifestResource struct {
    providerData *kubectlProviderData
}

// manifestResourceModel describes the resource data model
type manifestResourceModel struct {
    ID                    types.String `tfsdk:"id"`
    YAMLBody              types.String `tfsdk:"yaml_body"`
    APIVersion            types.String `tfsdk:"api_version"`
    Kind                  types.String `tfsdk:"kind"`
    Name                  types.String `tfsdk:"name"`
    Namespace             types.String `tfsdk:"namespace"`
    UID                   types.String `tfsdk:"uid"`
    ResourceVersion       types.String `tfsdk:"resource_version"`
    LiveManifestFields    types.Map    `tfsdk:"live_manifest_fields"`
    WaitForRollout        types.Bool   `tfsdk:"wait_for_rollout"`
    ServerSideApply       types.Bool   `tfsdk:"server_side_apply"`
    ForceConflicts        types.Bool   `tfsdk:"force_conflicts"`
    ForceNew              types.Bool   `tfsdk:"force_new"`
    IgnoreFields          types.List   `tfsdk:"ignore_fields"`
    OverrideNamespace     types.String `tfsdk:"override_namespace"`
    ValidateSchema        types.Bool   `tfsdk:"validate_schema"`
}

// NewManifestResource returns a new manifest resource
func NewManifestResource() resource.Resource {
    return &manifestResource{}
}

// Metadata returns the resource type name
func (r *manifestResource) Metadata(
    ctx context.Context,
    req resource.MetadataRequest,
    resp *resource.MetadataResponse,
) {
    resp.TypeName = req.ProviderTypeName + "_manifest"
}

// Schema defines the resource schema
func (r *manifestResource) Schema(
    ctx context.Context,
    req resource.SchemaRequest,
    resp *resource.SchemaResponse,
) {
    resp.Schema = schema.Schema{
        Description: "Deploy and manage any Kubernetes resource using YAML manifests. " +
            "This resource applies and manages the full lifecycle of Kubernetes resources, " +
            "including creation, updates, and deletion, with drift detection.",
        Attributes: map[string]schema.Attribute{
            "id": schema.StringAttribute{
                Computed: true,
                Description: "Unique identifier for the resource in the format: " +
                    "apiVersion//kind//namespace//name",
                PlanModifiers: []planmodifier.String{
                    stringplanmodifier.UseStateForUnknown(),
                },
            },
            "yaml_body": schema.StringAttribute{
                Required: true,
                Description: "YAML manifest for the Kubernetes resource. " +
                    "This should be a valid Kubernetes resource definition.",
            },
            "api_version": schema.StringAttribute{
                Computed: true,
                Description: "The API version of the resource (extracted from YAML)",
            },
            "kind": schema.StringAttribute{
                Computed: true,
                Description: "The kind of the resource (extracted from YAML)",
            },
            "name": schema.StringAttribute{
                Computed: true,
                Description: "The name of the resource (extracted from YAML)",
            },
            "namespace": schema.StringAttribute{
                Computed: true,
                Description: "The namespace of the resource (extracted from YAML). " +
                    "Empty for cluster-scoped resources.",
            },
            "uid": schema.StringAttribute{
                Computed: true,
                Description: "The UID of the resource as assigned by Kubernetes",
            },
            "resource_version": schema.StringAttribute{
                Computed: true,
                Description: "The resource version as assigned by Kubernetes",
            },
            "live_manifest_fields": schema.MapAttribute{
                ElementType: types.StringType,
                Computed:    true,
                Description: "Additional fields from the live manifest not present in YAML",
            },
            "wait_for_rollout": schema.BoolAttribute{
                Optional: true,
                Description: "Wait for the resource rollout to complete. " +
                    "Useful for Deployments, StatefulSets, and DaemonSets.",
            },
            "server_side_apply": schema.BoolAttribute{
                Optional: true,
                Description: "Use server-side apply instead of client-side apply. " +
                    "Server-side apply is more robust but requires Kubernetes 1.16+.",
            },
            "force_conflicts": schema.BoolAttribute{
                Optional: true,
                Description: "Force apply even if there are conflicts. " +
                    "Only used with server_side_apply.",
            },
            "force_new": schema.BoolAttribute{
                Optional: true,
                Description: "Force recreation of the resource on updates",
            },
            "ignore_fields": schema.ListAttribute{
                ElementType: types.StringType,
                Optional:    true,
                Description: "List of JSON paths to ignore when comparing resources. " +
                    "Example: ['metadata.annotations', 'spec.replicas']",
            },
            "override_namespace": schema.StringAttribute{
                Optional: true,
                Description: "Override the namespace specified in the YAML manifest",
            },
            "validate_schema": schema.BoolAttribute{
                Optional: true,
                Description: "Validate the resource against the Kubernetes OpenAPI schema. " +
                    "Defaults to true.",
            },
        },
    }
}

// Configure sets the provider data for the resource
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

// Create creates a new Kubernetes resource
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

    // Parse YAML
    yamlBody := plan.YAMLBody.ValueString()
    parsedManifest, err := yaml.ParseYAML(yamlBody)
    if err != nil {
        resp.Diagnostics.AddError(
            "Failed to Parse YAML",
            fmt.Sprintf("Could not parse yaml_body: %s", err),
        )
        return
    }

    // Apply retry logic
    var applyErr error
    retryCount := r.providerData.ApplyRetryCount
    for attempt := int64(0); attempt < retryCount; attempt++ {
        if attempt > 0 {
            time.Sleep(time.Duration(attempt*3) * time.Second)
        }
        
        applyErr = r.applyManifest(ctx, &plan, parsedManifest)
        if applyErr == nil {
            break
        }
    }
    
    if applyErr != nil {
        resp.Diagnostics.AddError(
            "Failed to Create Resource",
            fmt.Sprintf("Could not apply manifest: %s", applyErr),
        )
        return
    }

    // Read back to get computed values
    if err := r.readManifest(ctx, &plan); err != nil {
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

// Read reads the current state of the resource
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
        // If resource is not found, remove from state
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

    // CRITICAL: Handle null vs empty values consistently
    // This prevents import state verification failures
    if state.Namespace.ValueString() == "" {
        state.Namespace = types.StringNull()
    }
    
    // Set state
    diags = resp.State.Set(ctx, state)
    resp.Diagnostics.Append(diags...)
}

// Update updates an existing resource
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

    // Parse YAML
    yamlBody := plan.YAMLBody.ValueString()
    parsedManifest, err := yaml.ParseYAML(yamlBody)
    if err != nil {
        resp.Diagnostics.AddError(
            "Failed to Parse YAML",
            fmt.Sprintf("Could not parse yaml_body: %s", err),
        )
        return
    }

    // Apply with retry
    var applyErr error
    retryCount := r.providerData.ApplyRetryCount
    for attempt := int64(0); attempt < retryCount; attempt++ {
        if attempt > 0 {
            time.Sleep(time.Duration(attempt*3) * time.Second)
        }
        
        applyErr = r.applyManifest(ctx, &plan, parsedManifest)
        if applyErr == nil {
            break
        }
    }
    
    if applyErr != nil {
        resp.Diagnostics.AddError(
            "Failed to Update Resource",
            fmt.Sprintf("Could not apply manifest: %s", applyErr),
        )
        return
    }

    // Read back
    if err := r.readManifest(ctx, &plan); err != nil {
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

// Delete removes the resource
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

    // Delete the resource from Kubernetes
    if err := r.deleteManifest(ctx, &state); err != nil {
        resp.Diagnostics.AddError(
            "Failed to Delete Resource",
            fmt.Sprintf("Could not delete manifest: %s", err),
        )
        return
    }
}

// ImportState imports an existing resource
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

    // Create model
    model := manifestResourceModel{
        YAMLBody: types.StringValue(yamlBody),
    }

    // Read from Kubernetes to populate state
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

// Helper methods

func (r *manifestResource) applyManifest(
    ctx context.Context,
    model *manifestResourceModel,
    manifest interface{},
) error {
    // Implementation: Apply manifest to Kubernetes
    // - Use server-side apply if enabled
    // - Handle force_conflicts
    // - Wait for rollout if enabled
    // - Extract computed fields
    return nil
}

func (r *manifestResource) readManifest(
    ctx context.Context,
    model *manifestResourceModel,
) error {
    // Implementation: Read manifest from Kubernetes
    // - Get resource from API
    // - Update computed fields
    // - Handle drift detection
    return nil
}

func (r *manifestResource) deleteManifest(
    ctx context.Context,
    model *manifestResourceModel,
) error {
    // Implementation: Delete manifest from Kubernetes
    // - Use dynamic client
    // - Handle finalizers
    return nil
}

func isNotFoundError(err error) bool {
    // Check if error is Kubernetes NotFound error
    return false
}
```

## Step 5: Migrate Data Sources

### Example: File Documents Data Source

Create `kubectl/data_source_file_documents.go`:

```go
package kubectl

import (
    "context"
    "fmt"

    "github.com/hashicorp/terraform-plugin-framework/datasource"
    "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
    "github.com/hashicorp/terraform-plugin-framework/types"
    
    "github.com/alekc/terraform-provider-kubectl/yaml"
)

// Ensure provider defined types fully satisfy framework interfaces
var _ datasource.DataSource = &fileDocumentsDataSource{}

// fileDocumentsDataSource defines the data source implementation
type fileDocumentsDataSource struct{}

// fileDocumentsDataSourceModel describes the data source data model
type fileDocumentsDataSourceModel struct {
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
        Description: "Parses a YAML file containing multiple Kubernetes manifests " +
            "and splits it into individual documents.",
        Attributes: map[string]schema.Attribute{
            "content": schema.StringAttribute{
                Required: true,
                Description: "Raw YAML content containing one or more Kubernetes manifests " +
                    "separated by '---'",
            },
            "documents": schema.ListAttribute{
                ElementType: types.StringType,
                Computed:    true,
                Description: "List of individual YAML documents extracted from the content",
            },
            "manifests": schema.MapAttribute{
                ElementType: types.StringType,
                Computed:    true,
                Description: "Map of manifest name to YAML content. " +
                    "Key format: kind.namespace.name or kind.name for cluster-scoped resources",
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

    // Split YAML into documents
    content := data.Content.ValueString()
    documents, err := yaml.SplitYAML(content)
    if err != nil {
        resp.Diagnostics.AddError(
            "Failed to Parse YAML",
            fmt.Sprintf("Could not split YAML content: %s", err),
        )
        return
    }

    // Convert to Framework types
    docList := make([]string, len(documents))
    manifestMap := make(map[string]string)
    
    for i, doc := range documents {
        docList[i] = doc
        
        // Parse to extract metadata for key
        parsed, err := yaml.ParseYAML(doc)
        if err != nil {
            continue // Skip invalid documents
        }
        
        // Extract kind, namespace, name
        key := generateManifestKey(parsed)
        manifestMap[key] = doc
    }

    // Set computed values
    docListValue, diags := types.ListValueFrom(ctx, types.StringType, docList)
    resp.Diagnostics.Append(diags...)
    
    manifestMapValue, diags := types.MapValueFrom(ctx, types.StringType, manifestMap)
    resp.Diagnostics.Append(diags...)
    
    data.Documents = docListValue
    data.Manifests = manifestMapValue

    // Set state
    diags = resp.State.Set(ctx, &data)
    resp.Diagnostics.Append(diags...)
}

func generateManifestKey(manifest interface{}) string {
    // Extract kind, namespace, name and generate key
    return "default.example"
}
```

## Step 6: Testing Strategy

### Acceptance Test Example

Create `kubectl/resource_manifest_test.go`:

```go
package kubectl_test

import (
    "testing"

    "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccManifestResource_basic(t *testing.T) {
    resource.Test(t, resource.TestCase{
        PreCheck:                 func() { testAccPreCheck(t) },
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            // Create and Read testing
            {
                Config: testAccManifestResourceConfig_basic(),
                Check: resource.ComposeTestCheckFunc(
                    resource.TestCheckResourceAttr("kubectl_manifest.test", "kind", "ConfigMap"),
                    resource.TestCheckResourceAttr("kubectl_manifest.test", "name", "test-config"),
                    resource.TestCheckResourceAttrSet("kubectl_manifest.test", "uid"),
                ),
            },
            // ImportState testing
            {
                ResourceName:      "kubectl_manifest.test",
                ImportState:       true,
                ImportStateVerify: true,
                ImportStateId:     "v1//ConfigMap//test-config//default",
            },
            // Update testing
            {
                Config: testAccManifestResourceConfig_updated(),
                Check: resource.ComposeTestCheckFunc(
                    resource.TestCheckResourceAttr("kubectl_manifest.test", "kind", "ConfigMap"),
                ),
            },
        },
    })
}

func testAccManifestResourceConfig_basic() string {
    return `
provider "kubectl" {}

resource "kubectl_manifest" "test" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: test-config
      namespace: default
    data:
      key1: value1
  YAML
}
`
}
```

## Critical Migration Considerations

### 1. State Compatibility

**Issue**: Framework handles null/unknown differently than SDK v2

**Solution**: Consistent null handling patterns

```go
// ✅ CORRECT: Handle empty strings as null
if apiResponse.Namespace == "" {
    model.Namespace = types.StringNull()
} else {
    model.Namespace = types.StringValue(apiResponse.Namespace)
}

// ❌ WRONG: Always setting empty strings
model.Namespace = types.StringValue(apiResponse.Namespace) // Creates state drift
```

### 2. List and Map Handling

```go
// ✅ CORRECT: Handle empty lists as null
if len(items) > 0 {
    itemValues := make([]attr.Value, len(items))
    for i, item := range items {
        itemValues[i] = types.StringValue(item)
    }
    model.Items, diags = types.ListValue(types.StringType, itemValues)
} else {
    model.Items = types.ListNull(types.StringType)
}

// ❌ WRONG: Creating empty list
model.Items, _ = types.ListValue(types.StringType, []attr.Value{})
```

### 3. Import State Verification

```go
// Always test import with verification
{
    ResourceName:      "kubectl_manifest.test",
    ImportState:       true,
    ImportStateVerify: true,
    // Only ignore truly computed fields that change
    ImportStateVerifyIgnore: []string{"resource_version"},
}
```

### 4. Schema Attribute Types

```go
// Required: User MUST provide
"yaml_body": schema.StringAttribute{
    Required: true,
}

// Computed: API provides, user cannot set
"uid": schema.StringAttribute{
    Computed: true,
}

// Optional + Computed: User CAN provide, API has default
"namespace": schema.StringAttribute{
    Optional: true,
    Computed: true,
}
```

### 5. Sensitive Data Handling

```go
// Mark sensitive fields appropriately
"password": schema.StringAttribute{
    Optional:  true,
    Sensitive: true,
}
```

## Kubectl-Specific Considerations

### 1. YAML Parsing

The `yaml` package is shared between SDK v2 and Framework implementations. Ensure:
- Reuse existing parsing logic
- Maintain compatibility with existing YAML structures
- Handle multi-document YAML files

### 2. Dynamic Resource Handling

kubectl_manifest handles ANY Kubernetes resource dynamically:
- Use `unstructured.Unstructured` for dynamic resources
- Leverage `dynamic.Interface` for API operations
- Support custom resources (CRDs)

### 3. Server-Side Apply vs Client-Side Apply

```go
if model.ServerSideApply.ValueBool() {
    // Use Patch with server-side apply
    // Handle field managers
    // Respect force_conflicts
} else {
    // Traditional kubectl apply logic
}
```

### 4. Wait for Rollout

```go
if model.WaitForRollout.ValueBool() {
    // Wait for:
    // - Deployments to reach desired replica count
    // - StatefulSets to roll out
    // - DaemonSets to be ready
    // - Jobs to complete
}
```

### 5. Namespace Handling

```go
// Handle namespace override
effectiveNamespace := extractNamespace(manifest)
if !model.OverrideNamespace.IsNull() {
    effectiveNamespace = model.OverrideNamespace.ValueString()
}
```

## Migration Phases

### Phase 1: Infrastructure (Week 1)
- [ ] Update dependencies
- [ ] Create kubectl/ directory structure
- [ ] Implement muxed main.go
- [ ] Create provider.go with schema
- [ ] Implement Configure() with Kubernetes client setup

### Phase 2: Data Sources (Week 2)
- [ ] Migrate kubectl_file_documents
- [ ] Migrate kubectl_path_documents
- [ ] Migrate kubectl_filename_list
- [ ] Migrate kubectl_server_version
- [ ] Write acceptance tests

### Phase 3: Core Resource (Weeks 3-4)
- [ ] Migrate kubectl_manifest schema
- [ ] Implement Create/Read/Update/Delete
- [ ] Implement import functionality
- [ ] Add server-side apply support
- [ ] Add wait-for-rollout functionality
- [ ] Write comprehensive tests

### Phase 4: Validation & Cleanup (Week 5)
- [ ] Test state compatibility
- [ ] Verify import scenarios
- [ ] Performance testing
- [ ] Documentation updates
- [ ] Remove SDK v2 code (if fully migrated)

## Testing Checklist

- [ ] Basic CRUD operations work
- [ ] Import with verification passes
- [ ] No-op plans after apply
- [ ] No-op plans after import
- [ ] Server-side apply works
- [ ] Wait for rollout works
- [ ] Namespace override works
- [ ] Multi-document YAML handled
- [ ] Custom resources (CRDs) work
- [ ] Existing states migrate cleanly

## Common Pitfalls to Avoid

### 1. Inconsistent Null Handling
❌ Setting empty strings instead of null values
✅ Use types.StringNull() for empty/missing values

### 2. Import State Mismatches
❌ Not testing import verification
✅ Always test with ImportStateVerify: true

### 3. Plan Modifiers
❌ Missing UseStateForUnknown for computed IDs
✅ Add appropriate plan modifiers

### 4. Error Messages
❌ Generic error messages
✅ Specific, actionable error messages with context

### 5. Provider Data Nil Checks
❌ Assuming provider data is always set
✅ Check for nil in Configure() methods

## Resources

- [Ironic Provider Reference](https://github.com/metal3-community/terraform-provider-ironic)
- [Framework Migration Guide](https://developer.hashicorp.com/terraform/plugin/framework/migrating)
- [Framework Resources](https://developer.hashicorp.com/terraform/plugin/framework/resources)
- [Framework Data Sources](https://developer.hashicorp.com/terraform/plugin/framework/data-sources)
- [Framework Testing](https://developer.hashicorp.com/terraform/plugin/framework/testing)
- [Provider Mux Documentation](https://developer.hashicorp.com/terraform/plugin/mux)

## Success Criteria

✅ All resources and data sources migrated
✅ Existing Terraform states work without modification
✅ Import functionality maintains compatibility
✅ No-op plans after operations
✅ Acceptance tests pass at 100%
✅ Performance maintained or improved
✅ Documentation complete and accurate

This migration enables kubectl provider to leverage modern Plugin Framework features while maintaining full backward compatibility with existing user configurations.
