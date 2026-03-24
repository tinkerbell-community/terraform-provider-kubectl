package util

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// emptyResolvedConfig returns a ConfigData where every field is a concrete
// empty-string / false / null value — the state produced by the Helm-style
// resolution when all provider attributes come from unknown resource outputs.
func emptyResolvedConfig() ConfigData {
	return ConfigData{
		Host:                  types.StringValue(""),
		Username:              types.StringValue(""),
		Password:              types.StringValue(""),
		Insecure:              types.BoolValue(false),
		ClientCertificate:     types.StringValue(""),
		ClientKey:             types.StringValue(""),
		ClusterCACertificate:  types.StringValue(""),
		ConfigPath:            types.StringValue(""),
		ConfigPaths:           types.ListNull(types.StringType),
		ConfigContext:         types.StringValue(""),
		ConfigContextAuthInfo: types.StringValue(""),
		ConfigContextCluster:  types.StringValue(""),
		Token:                 types.StringValue(""),
		ProxyURL:              types.StringValue(""),
		LoadConfigFile:        types.BoolValue(false),
		TLSServerName:         types.StringValue(""),
		Exec:                  types.ListNull(types.StringType),
	}
}

func TestInitializeConfiguration_emptyConfig(t *testing.T) {
	// When provider config is resolved from unknown values (all empty strings),
	// InitializeConfiguration must not panic or return a host-parse error.
	cfg := emptyResolvedConfig()

	cc, err := InitializeConfiguration(context.Background(), cfg)
	if err != nil {
		t.Fatalf("InitializeConfiguration returned unexpected error for empty config: %v", err)
	}
	if cc == nil {
		t.Fatal("InitializeConfiguration returned nil ClientConfig")
	}
}

func TestInitializeConfiguration_validHost(t *testing.T) {
	cfg := emptyResolvedConfig()
	cfg.Host = types.StringValue("https://localhost:6443")

	cc, err := InitializeConfiguration(context.Background(), cfg)
	if err != nil {
		t.Fatalf("InitializeConfiguration returned unexpected error: %v", err)
	}

	// Verify the host was set on the resulting config
	rc, err := cc.ClientConfig()
	if err != nil {
		t.Fatalf("ClientConfig() returned error: %v", err)
	}
	if rc.Host != "https://localhost:6443" {
		t.Errorf("expected host https://localhost:6443, got %s", rc.Host)
	}
}

func TestInitializeConfiguration_nullFields(t *testing.T) {
	// When provider config fields are null (not set), they should be skipped.
	cfg := ConfigData{
		Host:                  types.StringNull(),
		Username:              types.StringNull(),
		Password:              types.StringNull(),
		Insecure:              types.BoolNull(),
		ClientCertificate:     types.StringNull(),
		ClientKey:             types.StringNull(),
		ClusterCACertificate:  types.StringNull(),
		ConfigPath:            types.StringNull(),
		ConfigPaths:           types.ListNull(types.StringType),
		ConfigContext:         types.StringNull(),
		ConfigContextAuthInfo: types.StringNull(),
		ConfigContextCluster:  types.StringNull(),
		Token:                 types.StringNull(),
		ProxyURL:              types.StringNull(),
		LoadConfigFile:        types.BoolNull(),
		TLSServerName:         types.StringNull(),
		Exec:                  types.ListNull(types.StringType),
	}

	cc, err := InitializeConfiguration(context.Background(), cfg)
	if err != nil {
		t.Fatalf("InitializeConfiguration returned unexpected error for null config: %v", err)
	}
	if cc == nil {
		t.Fatal("InitializeConfiguration returned nil ClientConfig")
	}
}

func TestInitializeConfiguration_hostWithToken(t *testing.T) {
	cfg := emptyResolvedConfig()
	cfg.Host = types.StringValue("https://k8s.example.com:6443")
	cfg.Token = types.StringValue("test-token")
	cfg.Insecure = types.BoolValue(true)

	cc, err := InitializeConfiguration(context.Background(), cfg)
	if err != nil {
		t.Fatalf("InitializeConfiguration returned unexpected error: %v", err)
	}

	rc, err := cc.ClientConfig()
	if err != nil {
		t.Fatalf("ClientConfig() returned error: %v", err)
	}
	if rc.Host != "https://k8s.example.com:6443" {
		t.Errorf("expected host https://k8s.example.com:6443, got %s", rc.Host)
	}
	if rc.BearerToken != "test-token" {
		t.Errorf("expected token test-token, got %s", rc.BearerToken)
	}
	if !rc.Insecure {
		t.Error("expected insecure=true")
	}
}

func TestInitializeConfiguration_emptyHostNotParsed(t *testing.T) {
	// Regression: empty host string must not be passed to DefaultServerURL
	// which would fail with "host must be a URL or a host:port pair".
	cfg := emptyResolvedConfig()
	cfg.Host = types.StringValue("")

	cc, err := InitializeConfiguration(context.Background(), cfg)
	if err != nil {
		t.Fatalf("empty host should not cause error, got: %v", err)
	}

	// The resulting REST config should have no host set
	rc, err := cc.ClientConfig()
	if err != nil {
		// "no configuration has been provided" is expected for a completely empty config
		return
	}
	if rc.Host != "" {
		t.Errorf("expected empty host, got %s", rc.Host)
	}
}
