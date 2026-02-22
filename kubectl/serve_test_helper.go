// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6/tf6server"
)

const providerName = "registry.terraform.io/alekc/kubectl"

// ServeTest starts an in-process framework provider server for acceptance testing.
// It returns ReattachInfo that can be passed to terraform-exec's SetReattachInfo.
func ServeTest(
	ctx context.Context,
	logger hclog.Logger,
	t *testing.T,
) (tfexec.ReattachInfo, error) {
	reattachConfigCh := make(chan *plugin.ReattachConfig)

	go func() {
		_ = tf6server.Serve(
			providerName,
			providerserver.NewProtocol6(New("test")()),
			tf6server.WithDebug(ctx, reattachConfigCh, nil),
			tf6server.WithLoggingSink(t),
			tf6server.WithGoPluginLogger(logger),
		)
	}()

	reattachConfig, err := waitForReattachConfig(reattachConfigCh)
	if err != nil {
		return nil, fmt.Errorf("error getting reattach config: %s", err)
	}

	return tfexec.ReattachInfo{
		providerName: convertReattachConfig(reattachConfig),
	}, nil
}

func convertReattachConfig(rc *plugin.ReattachConfig) tfexec.ReattachConfig {
	return tfexec.ReattachConfig{
		Protocol:        string(rc.Protocol),
		ProtocolVersion: rc.ProtocolVersion,
		Pid:             rc.Pid,
		Test:            true,
		Addr: tfexec.ReattachConfigAddr{
			Network: rc.Addr.Network(),
			String:  rc.Addr.String(),
		},
	}
}

func waitForReattachConfig(ch chan *plugin.ReattachConfig) (*plugin.ReattachConfig, error) {
	select {
	case config := <-ch:
		return config, nil
	case <-time.After(2 * time.Second):
		return nil, fmt.Errorf("timeout while waiting for reattach configuration")
	}
}
