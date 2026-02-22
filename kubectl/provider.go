package kubectl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/alekc/terraform-provider-kubectl/kubectl/api"
	"github.com/alekc/terraform-provider-kubectl/kubectl/util"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/mitchellh/go-homedir"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sresource "k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/discovery"
	diskcached "k8s.io/client-go/discovery/cached/disk"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	aggregator "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
)

// Ensure the implementation satisfies the provider interfaces.
var (
	_ provider.Provider            = &kubectlProvider{}
	_ provider.ProviderWithActions = &kubectlProvider{}
)

// kubectlProvider defines the provider implementation.
type kubectlProvider struct {
	version string
}

// kubectlProviderData contains the configured Kubernetes clients and settings.
type kubectlProviderData struct {
	MainClientset       *kubernetes.Clientset
	RestConfig          *restclient.Config
	AggregatorClientset *aggregator.Clientset
	ApplyRetryCount     int64

	// Cached clients for OpenAPI-aware operations
	logger          hclog.Logger
	dynamicClient   cache[dynamic.Interface]
	discoveryClient cache[discovery.DiscoveryInterface]
	restMapper      cache[meta.RESTMapper]
	restClient      cache[restclient.Interface]
	OAPIFoundry     cache[api.Foundry]
	crds            cache[[]unstructured.Unstructured]
}

// Implement k8sresource.RESTClientGetter interface for kubectlProviderData.
var _ k8sresource.RESTClientGetter = &kubectlProviderData{}

func (p *kubectlProviderData) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return nil
}

func (p *kubectlProviderData) ToRESTConfig() (*restclient.Config, error) {
	return p.RestConfig, nil
}

func (p *kubectlProviderData) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	home, _ := homedir.Dir()
	httpCacheDir := filepath.Join(home, ".kube", "http-cache")
	discoveryCacheDir := util.ComputeDiscoverCacheDir(
		filepath.Join(home, ".kube", "cache", "discovery"),
		p.RestConfig.Host,
	)
	return diskcached.NewCachedDiscoveryClientForConfig(
		p.RestConfig,
		discoveryCacheDir,
		httpCacheDir,
		10*time.Minute,
	)
}

func (p *kubectlProviderData) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := p.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	expander := restmapper.NewShortcutExpander(mapper, discoveryClient, func(msg string) {
		// Log warnings silently
	})
	return expander, nil
}

// New returns a new provider instance.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &kubectlProvider{
			version: version,
		}
	}
}

// Metadata returns the provider type name.
func (p *kubectlProvider) Metadata(
	ctx context.Context,
	req provider.MetadataRequest,
	resp *provider.MetadataResponse,
) {
	resp.TypeName = "kubectl"
	resp.Version = p.version
}

// Schema defines the provider-level schema for configuration data.
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
				Description: "Defines the number of attempts any create/update action will take. " +
					"Defaults to 1. Can be set with KUBECTL_PROVIDER_APPLY_RETRY_COUNT environment variable.",
			},
			"host": schema.StringAttribute{
				Optional: true,
				Description: "The hostname (in form of URI) of Kubernetes master. " +
					"Can be set with KUBE_HOST environment variable.",
			},
			"username": schema.StringAttribute{
				Optional: true,
				Description: "The username to use for HTTP basic authentication when accessing " +
					"the Kubernetes master endpoint. Can be set with KUBE_USER environment variable.",
			},
			"password": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "The password to use for HTTP basic authentication when accessing " +
					"the Kubernetes master endpoint. Can be set with KUBE_PASSWORD environment variable.",
			},
			"insecure": schema.BoolAttribute{
				Optional: true,
				Description: "Whether server should be accessed without verifying the TLS certificate. " +
					"Can be set with KUBE_INSECURE environment variable.",
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
				Description: "Load local kubeconfig. Defaults to true. " +
					"Can be set with KUBE_LOAD_CONFIG_FILE environment variable.",
			},
			"tls_server_name": schema.StringAttribute{
				Optional: true,
				Description: "Server name passed to the server for SNI and is used in the client " +
					"to check server certificates against. Can be set with KUBE_TLS_SERVER_NAME environment variable.",
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

// Configure prepares the Kubernetes client for data sources and resources.
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

	// Apply environment variable defaults following the precedence pattern:
	// configuration value > environment variable > default value

	// Handle apply_retry_count
	applyRetryCount := int64(1)
	if !config.ApplyRetryCount.IsNull() && !config.ApplyRetryCount.IsUnknown() {
		applyRetryCount = config.ApplyRetryCount.ValueInt64()
	} else if envValue := os.Getenv("KUBECTL_PROVIDER_APPLY_RETRY_COUNT"); envValue != "" {
		if parsed, err := strconv.ParseInt(envValue, 10, 64); err == nil {
			applyRetryCount = parsed
		}
	}

	// Handle host
	if config.Host.IsNull() || config.Host.IsUnknown() {
		if envValue := os.Getenv("KUBE_HOST"); envValue != "" {
			config.Host = types.StringValue(envValue)
		}
	}

	// Handle username
	if config.Username.IsNull() || config.Username.IsUnknown() {
		if envValue := os.Getenv("KUBE_USER"); envValue != "" {
			config.Username = types.StringValue(envValue)
		}
	}

	// Handle password
	if config.Password.IsNull() || config.Password.IsUnknown() {
		if envValue := os.Getenv("KUBE_PASSWORD"); envValue != "" {
			config.Password = types.StringValue(envValue)
		}
	}

	// Handle insecure
	if config.Insecure.IsNull() || config.Insecure.IsUnknown() {
		if envValue := os.Getenv("KUBE_INSECURE"); envValue != "" {
			if parsed, err := strconv.ParseBool(envValue); err == nil {
				config.Insecure = types.BoolValue(parsed)
			}
		} else {
			config.Insecure = types.BoolValue(false)
		}
	}

	// Handle client_certificate
	if config.ClientCertificate.IsNull() || config.ClientCertificate.IsUnknown() {
		if envValue := os.Getenv("KUBE_CLIENT_CERT_DATA"); envValue != "" {
			config.ClientCertificate = types.StringValue(envValue)
		}
	}

	// Handle client_key
	if config.ClientKey.IsNull() || config.ClientKey.IsUnknown() {
		if envValue := os.Getenv("KUBE_CLIENT_KEY_DATA"); envValue != "" {
			config.ClientKey = types.StringValue(envValue)
		}
	}

	// Handle cluster_ca_certificate
	if config.ClusterCACertificate.IsNull() || config.ClusterCACertificate.IsUnknown() {
		if envValue := os.Getenv("KUBE_CLUSTER_CA_CERT_DATA"); envValue != "" {
			config.ClusterCACertificate = types.StringValue(envValue)
		}
	}

	// Handle config_path with multiple environment variable options
	if config.ConfigPath.IsNull() || config.ConfigPath.IsUnknown() {
		configPath := ""
		for _, envVar := range []string{"KUBE_CONFIG", "KUBECONFIG", "KUBE_CONFIG_PATH"} {
			if envValue := os.Getenv(envVar); envValue != "" {
				configPath = envValue
				break
			}
		}
		if configPath == "" {
			configPath = "~/.kube/config"
		}
		config.ConfigPath = types.StringValue(configPath)
	}

	// Handle config_context
	if config.ConfigContext.IsNull() || config.ConfigContext.IsUnknown() {
		if envValue := os.Getenv("KUBE_CTX"); envValue != "" {
			config.ConfigContext = types.StringValue(envValue)
		}
	}

	// Handle config_context_auth_info
	if config.ConfigContextAuthInfo.IsNull() || config.ConfigContextAuthInfo.IsUnknown() {
		if envValue := os.Getenv("KUBE_CTX_AUTH_INFO"); envValue != "" {
			config.ConfigContextAuthInfo = types.StringValue(envValue)
		}
	}

	// Handle config_context_cluster
	if config.ConfigContextCluster.IsNull() || config.ConfigContextCluster.IsUnknown() {
		if envValue := os.Getenv("KUBE_CTX_CLUSTER"); envValue != "" {
			config.ConfigContextCluster = types.StringValue(envValue)
		}
	}

	// Handle token
	if config.Token.IsNull() || config.Token.IsUnknown() {
		if envValue := os.Getenv("KUBE_TOKEN"); envValue != "" {
			config.Token = types.StringValue(envValue)
		}
	}

	// Handle proxy_url
	if config.ProxyURL.IsNull() || config.ProxyURL.IsUnknown() {
		if envValue := os.Getenv("KUBE_PROXY_URL"); envValue != "" {
			config.ProxyURL = types.StringValue(envValue)
		}
	}

	// Handle load_config_file
	if config.LoadConfigFile.IsNull() || config.LoadConfigFile.IsUnknown() {
		if envValue := os.Getenv("KUBE_LOAD_CONFIG_FILE"); envValue != "" {
			if parsed, err := strconv.ParseBool(envValue); err == nil {
				config.LoadConfigFile = types.BoolValue(parsed)
			}
		} else {
			config.LoadConfigFile = types.BoolValue(true)
		}
	}

	// Handle tls_server_name
	if config.TLSServerName.IsNull() || config.TLSServerName.IsUnknown() {
		if envValue := os.Getenv("KUBE_TLS_SERVER_NAME"); envValue != "" {
			config.TLSServerName = types.StringValue(envValue)
		}
	}

	// Initialize Kubernetes configuration
	configData := util.ConfigData{
		Host:                  config.Host,
		Username:              config.Username,
		Password:              config.Password,
		Insecure:              config.Insecure,
		ClientCertificate:     config.ClientCertificate,
		ClientKey:             config.ClientKey,
		ClusterCACertificate:  config.ClusterCACertificate,
		ConfigPath:            config.ConfigPath,
		ConfigPaths:           config.ConfigPaths,
		ConfigContext:         config.ConfigContext,
		ConfigContextAuthInfo: config.ConfigContextAuthInfo,
		ConfigContextCluster:  config.ConfigContextCluster,
		Token:                 config.Token,
		ProxyURL:              config.ProxyURL,
		LoadConfigFile:        config.LoadConfigFile,
		TLSServerName:         config.TLSServerName,
		Exec:                  config.Exec,
	}

	cfg, err := util.InitializeConfiguration(ctx, configData)
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

	// Set user agent
	cfg.UserAgent = fmt.Sprintf("HashiCorp/1.0 Terraform/%s", req.TerraformVersion)

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

	// Create provider data structure
	providerData := &kubectlProviderData{
		MainClientset:       k8sClient,
		RestConfig:          cfg,
		AggregatorClientset: aggClient,
		ApplyRetryCount:     applyRetryCount,
		logger:              hclog.Default(),
	}

	// Make provider data available to resources, data sources, and actions
	resp.DataSourceData = providerData
	resp.ResourceData = providerData
	resp.ActionData = providerData
}

// Actions returns the actions implemented by this provider.
func (p *kubectlProvider) Actions(ctx context.Context) []func() action.Action {
	return []func() action.Action{
		NewPatchAction,
	}
}

// Resources returns the resources implemented by this provider.
func (p *kubectlProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewManifestResource,
		NewPatchResource,
	}
}

// DataSources returns the data sources implemented by this provider.
func (p *kubectlProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewManifestDataSource,
	}
}

// Functions returns the provider-defined functions implemented by this provider.
func (p *kubectlProvider) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{
		NewManifestDecodeFunction,
		NewManifestDecodeMultiFunction,
		NewManifestEncodeFunction,
	}
}
