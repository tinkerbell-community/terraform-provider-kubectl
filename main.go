package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6/tf6server"
	"github.com/hashicorp/terraform-plugin-mux/tf5to6server"
	"github.com/hashicorp/terraform-plugin-mux/tf6muxserver"

	// SDK v2 provider (existing)
	sdkProvider "github.com/alekc/terraform-provider-kubectl/kubernetes"
	// Plugin Framework provider (new)
	frameworkProvider "github.com/alekc/terraform-provider-kubectl/kubectl"
)

//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate -provider-name kubectl

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

	// Create muxed server combining SDK v2 and Framework providers
	muxServer, err := tf6muxserver.NewMuxServer(
		ctx,
		upgradedSdkProvider,
		providerserver.NewProtocol6(frameworkProvider.New("dev")()),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Prepare server options
	var serveOpts []tf6server.ServeOpt

	if debug {
		serveOpts = append(serveOpts, tf6server.WithManagedDebug())
	}

	// Serve the muxed provider
	err = tf6server.Serve(
		"registry.terraform.io/alekc/kubectl",
		muxServer.ProviderServer,
		serveOpts...,
	)

	if err != nil {
		log.Fatal(err)
	}
}
