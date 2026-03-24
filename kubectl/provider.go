package kubectl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/hashicorp-oss/terraform-provider-kubectl/kubectl/api"
	"github.com/hashicorp-oss/terraform-provider-kubectl/kubectl/util"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sresource "k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// Ensure the implementation satisfies the provider interfaces.
var (
	_ provider.Provider              = &kubectlProvider{}
	_ provider.ProviderWithActions   = &kubectlProvider{}
	_ provider.ProviderWithFunctions = &kubectlProvider{}
)

// kubectlProvider defines the provider implementation.
type kubectlProvider struct {
	version string
}

// kubectlProviderData contains the configured Kubernetes clients and settings.
type kubectlProviderData struct {
	configData       util.ConfigData
	configFullyKnown bool
	ApplyRetryCount  int64
	terraformVersion string

	// Lazily initialized clients
	logger          hclog.Logger
	clientConfig    cache[clientcmd.ClientConfig]
	restConfig      cache[*restclient.Config]
	mainClientset   cache[*kubernetes.Clientset]
	dynamicClient   cache[dynamic.Interface]
	discoveryClient cache[discovery.DiscoveryInterface]
	restMapper      cache[meta.RESTMapper]
	restClient      cache[restclient.Interface]
	OAPIFoundry     cache[api.Foundry]
	crds            cache[[]unstructured.Unstructured]
}

// getClientConfig lazily initializes and returns the clientcmd.ClientConfig.
func (p *kubectlProviderData) getClientConfig() (clientcmd.ClientConfig, error) {
	return p.clientConfig.Get(func() (clientcmd.ClientConfig, error) {
		return util.InitializeConfiguration(context.Background(), p.configData)
	})
}

// getRestConfig lazily initializes and returns the Kubernetes REST config.
func (p *kubectlProviderData) getRestConfig() (*restclient.Config, error) {
	return p.restConfig.Get(func() (*restclient.Config, error) {
		cc, err := p.getClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load Kubernetes REST config: %w", err)
		}
		cfg, err := cc.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load Kubernetes REST config: %w", err)
		}
		cfg.QPS = 100.0
		cfg.Burst = 100
		cfg.UserAgent = fmt.Sprintf("HashiCorp/1.0 Terraform/%s", p.terraformVersion)
		return cfg, nil
	})
}

// getMainClientset lazily initializes and returns the Kubernetes clientset.
func (p *kubectlProviderData) getMainClientset() (*kubernetes.Clientset, error) {
	return p.mainClientset.Get(func() (*kubernetes.Clientset, error) {
		cfg, err := p.getRestConfig()
		if err != nil {
			return nil, err
		}
		return kubernetes.NewForConfig(cfg)
	})
}

// Implement k8sresource.RESTClientGetter interface for kubectlProviderData.
var _ k8sresource.RESTClientGetter = &kubectlProviderData{}

func (p *kubectlProviderData) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	cc, _ := p.getClientConfig()
	return cc
}

func (p *kubectlProviderData) ToRESTConfig() (*restclient.Config, error) {
	return p.getRestConfig()
}

func (p *kubectlProviderData) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	config, err := p.ToRESTConfig()
	if err != nil {
		return nil, err
	}

	return memory.NewMemCacheClient(
		discovery.NewDiscoveryClientForConfigOrDie(config),
	), nil
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
// Following the Helm provider pattern: resolve env-var defaults here, store
// the resolved config, and defer all client creation to lazy accessors.
func (p *kubectlProvider) Configure(
	ctx context.Context,
	req provider.ConfigureRequest,
	resp *provider.ConfigureResponse,
) {
	if req.ClientCapabilities.DeferralAllowed && !req.Config.Raw.IsFullyKnown() {
		resp.Deferred = &provider.Deferred{
			Reason: provider.DeferredReasonProviderConfigUnknown,
		}
	}

	// Read env-var defaults first (like Helm provider).
	kubeHost := os.Getenv("KUBE_HOST")
	kubeUser := os.Getenv("KUBE_USER")
	kubePassword := os.Getenv("KUBE_PASSWORD")
	kubeInsecureStr := os.Getenv("KUBE_INSECURE")
	kubeTLSServerName := os.Getenv("KUBE_TLS_SERVER_NAME")
	kubeClientCert := os.Getenv("KUBE_CLIENT_CERT_DATA")
	kubeClientKey := os.Getenv("KUBE_CLIENT_KEY_DATA")
	kubeCACert := os.Getenv("KUBE_CLUSTER_CA_CERT_DATA")
	kubeConfigPath := os.Getenv("KUBE_CONFIG_PATH")
	if kubeConfigPath == "" {
		kubeConfigPath = os.Getenv("KUBE_CONFIG")
	}
	if kubeConfigPath == "" {
		kubeConfigPath = os.Getenv("KUBECONFIG")
	}
	kubeConfigPaths := os.Getenv("KUBE_CONFIG_PATHS")
	kubeConfigContext := os.Getenv("KUBE_CTX")
	kubeConfigContextAuthInfo := os.Getenv("KUBE_CTX_AUTH_INFO")
	kubeConfigContextCluster := os.Getenv("KUBE_CTX_CLUSTER")
	kubeToken := os.Getenv("KUBE_TOKEN")
	kubeProxy := os.Getenv("KUBE_PROXY_URL")
	applyRetryCountStr := os.Getenv("KUBECTL_PROVIDER_APPLY_RETRY_COUNT")
	loadConfigFileStr := os.Getenv("KUBE_LOAD_CONFIG_FILE")

	var config util.ConfigData
	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Override env-var defaults with explicit config values (like Helm).
	// Using !IsNull() (not IsUnknown) — unknown values yield "" which
	// is the same as the env-var default, so they are harmless.
	if !config.Host.IsNull() {
		kubeHost = config.Host.ValueString()
	}
	if !config.Username.IsNull() {
		kubeUser = config.Username.ValueString()
	}
	if !config.Password.IsNull() {
		kubePassword = config.Password.ValueString()
	}
	if !config.Insecure.IsNull() {
		kubeInsecureStr = strconv.FormatBool(config.Insecure.ValueBool())
	}
	if !config.TLSServerName.IsNull() {
		kubeTLSServerName = config.TLSServerName.ValueString()
	}
	if !config.ClientCertificate.IsNull() {
		kubeClientCert = config.ClientCertificate.ValueString()
	}
	if !config.ClientKey.IsNull() {
		kubeClientKey = config.ClientKey.ValueString()
	}
	if !config.ClusterCACertificate.IsNull() {
		kubeCACert = config.ClusterCACertificate.ValueString()
	}
	if !config.ConfigPath.IsNull() {
		kubeConfigPath = config.ConfigPath.ValueString()
	}
	if !config.ConfigContext.IsNull() {
		kubeConfigContext = config.ConfigContext.ValueString()
	}
	if !config.ConfigContextAuthInfo.IsNull() {
		kubeConfigContextAuthInfo = config.ConfigContextAuthInfo.ValueString()
	}
	if !config.ConfigContextCluster.IsNull() {
		kubeConfigContextCluster = config.ConfigContextCluster.ValueString()
	}
	if !config.Token.IsNull() {
		kubeToken = config.Token.ValueString()
	}
	if !config.ProxyURL.IsNull() {
		kubeProxy = config.ProxyURL.ValueString()
	}
	if !config.LoadConfigFile.IsNull() {
		loadConfigFileStr = strconv.FormatBool(config.LoadConfigFile.ValueBool())
	}

	// Resolve apply_retry_count
	applyRetryCount := int64(1)
	if applyRetryCountStr != "" {
		if parsed, err := strconv.ParseInt(applyRetryCountStr, 10, 64); err == nil {
			applyRetryCount = parsed
		}
	}
	if !config.ApplyRetryCount.IsNull() {
		applyRetryCount = config.ApplyRetryCount.ValueInt64()
	}

	// Resolve insecure and load_config_file booleans
	var kubeInsecure bool
	if kubeInsecureStr != "" {
		if parsed, err := strconv.ParseBool(kubeInsecureStr); err == nil {
			kubeInsecure = parsed
		}
	}
	loadConfigFile := true
	if loadConfigFileStr != "" {
		if parsed, err := strconv.ParseBool(loadConfigFileStr); err == nil {
			loadConfigFile = parsed
		}
	}

	// Resolve config_paths list
	var kubeConfigPathsList []string
	if kubeConfigPaths != "" {
		kubeConfigPathsList = append(kubeConfigPathsList, filepath.SplitList(kubeConfigPaths)...)
	}
	if !config.ConfigPaths.IsNull() {
		var paths []string
		diags = config.ConfigPaths.ElementsAs(ctx, &paths, false)
		resp.Diagnostics.Append(diags...)
		kubeConfigPathsList = append(kubeConfigPathsList, paths...)
	}

	// Build the resolved ConfigData with plain Go values (like Helm's
	// kubernetesConfigObjectValue). Unknown values have already been
	// resolved to "" / false through the env-var overlay above.
	resolvedConfig := util.ConfigData{
		Host:                  types.StringValue(kubeHost),
		Username:              types.StringValue(kubeUser),
		Password:              types.StringValue(kubePassword),
		Insecure:              types.BoolValue(kubeInsecure),
		TLSServerName:         types.StringValue(kubeTLSServerName),
		ClientCertificate:     types.StringValue(kubeClientCert),
		ClientKey:             types.StringValue(kubeClientKey),
		ClusterCACertificate:  types.StringValue(kubeCACert),
		ConfigPath:            types.StringValue(kubeConfigPath),
		ConfigPaths:           util.StringListToFramework(ctx, kubeConfigPathsList),
		ConfigContext:         types.StringValue(kubeConfigContext),
		ConfigContextAuthInfo: types.StringValue(kubeConfigContextAuthInfo),
		ConfigContextCluster:  types.StringValue(kubeConfigContextCluster),
		Token:                 types.StringValue(kubeToken),
		ProxyURL:              types.StringValue(kubeProxy),
		LoadConfigFile:        types.BoolValue(loadConfigFile),
		Exec:                  config.Exec,
	}

	// Create provider data structure — clients are initialized lazily on first use
	providerData := &kubectlProviderData{
		configData:       resolvedConfig,
		configFullyKnown: req.Config.Raw.IsFullyKnown(),
		ApplyRetryCount:  applyRetryCount,
		terraformVersion: req.TerraformVersion,
		logger:           hclog.Default(),
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
