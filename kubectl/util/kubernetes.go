package util

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/mitchellh/go-homedir"
	apimachineryschema "k8s.io/apimachinery/pkg/runtime/schema"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ConfigData represents the provider configuration data for Kubernetes initialization.
type ConfigData struct {
	Host                  types.String
	Username              types.String
	Password              types.String
	Insecure              types.Bool
	ClientCertificate     types.String
	ClientKey             types.String
	ClusterCACertificate  types.String
	ConfigPath            types.String
	ConfigPaths           types.List
	ConfigContext         types.String
	ConfigContextAuthInfo types.String
	ConfigContextCluster  types.String
	Token                 types.String
	ProxyURL              types.String
	LoadConfigFile        types.Bool
	TLSServerName         types.String
	Exec                  types.List
}

// ExecConfigData represents exec authentication configuration.
type ExecConfigData struct {
	APIVersion types.String `tfsdk:"api_version"`
	Command    types.String `tfsdk:"command"`
	Env        types.Map    `tfsdk:"env"`
	Args       types.List   `tfsdk:"args"`
}

// InitializeConfiguration creates a Kubernetes REST config from provider configuration
// This is adapted from the SDK v2 version to work with Plugin Framework types.
func InitializeConfiguration(ctx context.Context, config ConfigData) (*restclient.Config, error) {
	overrides := &clientcmd.ConfigOverrides{}
	loader := &clientcmd.ClientConfigLoadingRules{}

	// Handle config_path
	var configPaths []string
	if !config.ConfigPath.IsNull() && !config.ConfigPath.IsUnknown() {
		configPaths = []string{config.ConfigPath.ValueString()}
	} else if !config.ConfigPaths.IsNull() && !config.ConfigPaths.IsUnknown() {
		// Handle config_paths
		var paths []string
		diags := config.ConfigPaths.ElementsAs(ctx, &paths, false)
		if !diags.HasError() && len(paths) > 0 {
			configPaths = paths
		}
	} else if v := os.Getenv("KUBE_CONFIG_PATHS"); v != "" {
		configPaths = filepath.SplitList(v)
	}

	// Handle exec authentication
	if !config.Exec.IsNull() && !config.Exec.IsUnknown() {
		var execConfigs []ExecConfigData
		diags := config.Exec.ElementsAs(ctx, &execConfigs, false)
		if !diags.HasError() && len(execConfigs) > 0 {
			execConfig := execConfigs[0]
			exec := &clientcmdapi.ExecConfig{
				InteractiveMode: clientcmdapi.IfAvailableExecInteractiveMode,
			}

			if !execConfig.APIVersion.IsNull() {
				exec.APIVersion = execConfig.APIVersion.ValueString()
			}

			if !execConfig.Command.IsNull() {
				exec.Command = execConfig.Command.ValueString()
			}

			// Handle args
			if !execConfig.Args.IsNull() && !execConfig.Args.IsUnknown() {
				var args []string
				diags := execConfig.Args.ElementsAs(ctx, &args, false)
				if !diags.HasError() {
					exec.Args = args
				}
			}

			// Handle env
			if !execConfig.Env.IsNull() && !execConfig.Env.IsUnknown() {
				var envMap map[string]string
				diags := execConfig.Env.ElementsAs(ctx, &envMap, false)
				if !diags.HasError() {
					for k, v := range envMap {
						exec.Env = append(exec.Env, clientcmdapi.ExecEnvVar{Name: k, Value: v})
					}
				}
			}

			overrides.AuthInfo.Exec = exec
		}
	} else if !config.LoadConfigFile.IsNull() && config.LoadConfigFile.ValueBool() && len(configPaths) > 0 {
		// Load kubeconfig files
		expandedPaths := []string{}
		for _, p := range configPaths {
			path, err := homedir.Expand(p)
			if err != nil {
				return nil, err
			}
			expandedPaths = append(expandedPaths, path)
		}

		if len(expandedPaths) == 1 {
			loader.ExplicitPath = expandedPaths[0]
		} else {
			loader.Precedence = expandedPaths
		}

		// Handle context overrides
		if !config.ConfigContext.IsNull() && !config.ConfigContext.IsUnknown() {
			overrides.CurrentContext = config.ConfigContext.ValueString()
		}

		if !config.ConfigContextAuthInfo.IsNull() && !config.ConfigContextAuthInfo.IsUnknown() {
			overrides.Context = clientcmdapi.Context{}
			overrides.Context.AuthInfo = config.ConfigContextAuthInfo.ValueString()
		}

		if !config.ConfigContextCluster.IsNull() && !config.ConfigContextCluster.IsUnknown() {
			if overrides.Context.AuthInfo == "" {
				overrides.Context = clientcmdapi.Context{}
			}
			overrides.Context.Cluster = config.ConfigContextCluster.ValueString()
		}
	}

	// Apply static configuration overrides
	if !config.Insecure.IsNull() {
		overrides.ClusterInfo.InsecureSkipTLSVerify = config.Insecure.ValueBool()
	}

	if !config.ClusterCACertificate.IsNull() {
		overrides.ClusterInfo.CertificateAuthorityData = bytes.NewBufferString(config.ClusterCACertificate.ValueString()).
			Bytes()
	}

	if !config.ClientCertificate.IsNull() {
		overrides.AuthInfo.ClientCertificateData = bytes.NewBufferString(config.ClientCertificate.ValueString()).
			Bytes()
	}

	if !config.Host.IsNull() {
		hasCA := len(overrides.ClusterInfo.CertificateAuthorityData) != 0
		hasCert := len(overrides.AuthInfo.ClientCertificateData) != 0
		defaultTLS := hasCA || hasCert || overrides.ClusterInfo.InsecureSkipTLSVerify
		host, _, err := restclient.DefaultServerURL(
			config.Host.ValueString(),
			"",
			apimachineryschema.GroupVersion{},
			defaultTLS,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to parse host: %w", err)
		}

		overrides.ClusterInfo.Server = host.String()
	}

	if !config.Username.IsNull() {
		overrides.AuthInfo.Username = config.Username.ValueString()
	}

	if !config.Password.IsNull() {
		overrides.AuthInfo.Password = config.Password.ValueString()
	}

	if !config.ClientKey.IsNull() {
		overrides.AuthInfo.ClientKeyData = bytes.NewBufferString(config.ClientKey.ValueString()).
			Bytes()
	}

	if !config.Token.IsNull() {
		overrides.AuthInfo.Token = config.Token.ValueString()
	}

	if !config.ProxyURL.IsNull() {
		overrides.ClusterDefaults.ProxyURL = config.ProxyURL.ValueString()
	}

	if !config.TLSServerName.IsNull() {
		overrides.ClusterInfo.TLSServerName = config.TLSServerName.ValueString()
	}

	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, overrides)
	cfg, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubernetes config: %w", err)
	}

	return cfg, nil
}

// ComputeDiscoverCacheDir takes the parentDir and the host and comes up with a "usually non-colliding" name.
var overlyCautiousIllegalFileCharacters = regexp.MustCompile(`[^(\w/\.)]`)

func ComputeDiscoverCacheDir(parentDir, host string) string {
	schemelessHost := strings.Replace(strings.Replace(host, "https://", "", 1), "http://", "", 1)
	safeHost := overlyCautiousIllegalFileCharacters.ReplaceAllString(schemelessHost, "_")
	return filepath.Join(parentDir, safeHost)
}

// GetEnvOrDefault returns the environment variable value or a default.
func GetEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// StringListToFramework converts a []string to a Framework types.List.
func StringListToFramework(ctx context.Context, input []string) types.List {
	if len(input) == 0 {
		return types.ListNull(types.StringType)
	}

	values := make([]attr.Value, len(input))
	for i, v := range input {
		values[i] = types.StringValue(v)
	}

	list, _ := types.ListValue(types.StringType, values)
	return list
}

// StringMapToFramework converts a map[string]string to a Framework types.Map.
func StringMapToFramework(ctx context.Context, input map[string]string) types.Map {
	if len(input) == 0 {
		return types.MapNull(types.StringType)
	}

	values := make(map[string]attr.Value)
	for k, v := range input {
		values[k] = types.StringValue(v)
	}

	mapVal, _ := types.MapValue(types.StringType, values)
	return mapVal
}
