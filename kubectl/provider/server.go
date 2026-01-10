// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/alekc/terraform-provider-kubectl/kubectl/openapi"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

func init() {
	install.Install(scheme.Scheme)
}

var (
	_ tfprotov6.ProviderServer   = &RawProviderServer{}
	_ tfprotov6.ResourceServer   = &RawProviderServer{}
	_ tfprotov6.DataSourceServer = &RawProviderServer{}
)

// RawProviderServer implements the ProviderServer interface as exported from ProtoBuf.
type RawProviderServer struct {
	// Since the provider is essentially a gRPC server, the execution flow is dictated by the order of the client (Terraform) request calls.
	// Thus it needs a way to persist state between the gRPC calls. These attributes store values that need to be persisted between gRPC calls,
	// such as instances of the Kubernetes clients, configuration options needed at runtime.
	logger                      hclog.Logger
	clientConfig                *rest.Config
	clientConfigUnknown         bool
	dynamicClient               cache[dynamic.Interface]
	discoveryClient             cache[discovery.DiscoveryInterface]
	restMapper                  cache[meta.RESTMapper]
	restClient                  cache[rest.Interface]
	OAPIFoundry                 cache[openapi.Foundry]
	crds                        cache[[]unstructured.Unstructured]
	checkValidCredentialsResult cache[[]*tfprotov6.Diagnostic]

	hostTFVersion string
}

func dump(v any) hclog.Format {
	return hclog.Fmt("%v", v)
}

// ValidateProviderConfig function
func (s *RawProviderServer) ValidateProviderConfig(
	ctx context.Context,
	req *tfprotov6.ValidateProviderConfigRequest,
) (*tfprotov6.ValidateProviderConfigResponse, error) {
	s.logger.Trace("[ValidateProviderConfig][Request]\n%s\n", dump(*req))
	resp := &tfprotov6.ValidateProviderConfigResponse{}
	return resp, nil
}

// GetMetadata function
func (s *RawProviderServer) GetMetadata(
	ctx context.Context,
	req *tfprotov6.GetMetadataRequest,
) (*tfprotov6.GetMetadataResponse, error) {
	s.logger.Trace("[GetMetadata][Request]\n%s\n", dump(*req))

	sch := GetProviderResourceSchema()
	rs := make([]tfprotov6.ResourceMetadata, 0, len(sch))
	for k := range sch {
		rs = append(rs, tfprotov6.ResourceMetadata{TypeName: k})
	}

	sch = GetProviderDataSourceSchema()
	ds := make([]tfprotov6.DataSourceMetadata, 0, len(sch))
	for k := range sch {
		ds = append(ds, tfprotov6.DataSourceMetadata{TypeName: k})
	}

	resp := &tfprotov6.GetMetadataResponse{
		Resources:   rs,
		DataSources: ds,
	}
	return resp, nil
}

// ValidateDataResourceConfig function
func (s *RawProviderServer) ValidateDataResourceConfig(
	ctx context.Context,
	req *tfprotov6.ValidateDataResourceConfigRequest,
) (*tfprotov6.ValidateDataResourceConfigResponse, error) {
	s.logger.Trace("[ValidateDataResourceConfig][Request]\n%s\n", dump(*req))
	resp := &tfprotov6.ValidateDataResourceConfigResponse{}
	return resp, nil
}

// StopProvider function
func (s *RawProviderServer) StopProvider(
	ctx context.Context,
	req *tfprotov6.StopProviderRequest,
) (*tfprotov6.StopProviderResponse, error) {
	s.logger.Trace("[StopProvider][Request]\n%s\n", dump(*req))

	return nil, status.Errorf(codes.Unimplemented, "method Stop not implemented")
}

// CallFunction function
func (s *RawProviderServer) CallFunction(
	ctx context.Context,
	req *tfprotov6.CallFunctionRequest,
) (*tfprotov6.CallFunctionResponse, error) {
	s.logger.Trace("[CallFunction][Request]\n%s\n", dump(*req))
	resp := &tfprotov6.CallFunctionResponse{}
	return resp, nil
}

// GetFunctions function
func (s *RawProviderServer) GetFunctions(
	ctx context.Context,
	req *tfprotov6.GetFunctionsRequest,
) (*tfprotov6.GetFunctionsResponse, error) {
	s.logger.Trace("[GetFunctions][Request]\n%s\n", dump(*req))
	resp := &tfprotov6.GetFunctionsResponse{}
	return resp, nil
}

// MoveResourceState function
func (s *RawProviderServer) MoveResourceState(
	ctx context.Context,
	req *tfprotov6.MoveResourceStateRequest,
) (*tfprotov6.MoveResourceStateResponse, error) {
	s.logger.Trace("[MoveResourceState][Request]\n%s\n", dump(*req))
	resp := &tfprotov6.MoveResourceStateResponse{}
	return resp, nil
}

func (s *RawProviderServer) OpenEphemeralResource(
	ctx context.Context,
	req *tfprotov6.OpenEphemeralResourceRequest,
) (*tfprotov6.OpenEphemeralResourceResponse, error) {
	s.logger.Trace("[OpenEphemeralResource][Request]\n%s\n", dump(*req))
	resp := &tfprotov6.OpenEphemeralResourceResponse{}
	return resp, nil
}

func (s *RawProviderServer) CloseEphemeralResource(
	ctx context.Context,
	req *tfprotov6.CloseEphemeralResourceRequest,
) (*tfprotov6.CloseEphemeralResourceResponse, error) {
	s.logger.Trace("[CloseEphemeralResource][Request]\n%s\n", dump(*req))
	resp := &tfprotov6.CloseEphemeralResourceResponse{}
	return resp, nil
}

func (s *RawProviderServer) RenewEphemeralResource(
	ctx context.Context,
	req *tfprotov6.RenewEphemeralResourceRequest,
) (*tfprotov6.RenewEphemeralResourceResponse, error) {
	s.logger.Trace("[RenewEphemeralResource][Request]\n%s\n", dump(*req))
	resp := &tfprotov6.RenewEphemeralResourceResponse{}
	return resp, nil
}

func (s *RawProviderServer) ValidateEphemeralResourceConfig(
	ctx context.Context,
	req *tfprotov6.ValidateEphemeralResourceConfigRequest,
) (*tfprotov6.ValidateEphemeralResourceConfigResponse, error) {
	s.logger.Trace("[ValidateEphemeralResourceConfig][Request]\n%s\n", dump(*req))
	resp := &tfprotov6.ValidateEphemeralResourceConfigResponse{}
	return resp, nil
}
