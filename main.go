package main

import (
	"context"
	"flag"
	"log"

	"github.com/alekc/terraform-provider-kubectl/kubectl"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// version is set via -ldflags during build
var version string = "dev"

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

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/alekc/kubectl",
		Debug:   debug,
	}

	err := providerserver.Serve(context.Background(), kubectl.New(version), opts)
	if err != nil {
		log.Fatal(err.Error())
	}
}
