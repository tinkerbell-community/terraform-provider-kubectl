package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl"
)

// version is set via -ldflags during build.
var version string = "dev"

//go:generate go tool tfplugindocs generate -provider-name kubectl

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
		Address: "registry.terraform.io/tinkerbell-community/kubectl",
		Debug:   debug,
	}

	err := providerserver.Serve(context.Background(), kubectl.New(version), opts)
	if err != nil {
		log.Fatal(err.Error())
	}
}
