// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/alekc/terraform-provider-kubectl/kubectl/api"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"k8s.io/client-go/dynamic"
)

func (s *RawProviderServer) waitForCompletion(
	ctx context.Context,
	waitForBlock tftypes.Value,
	rs dynamic.ResourceInterface,
	rname string,
	rtype tftypes.Type,
	th map[string]string,
) error {
	if waitForBlock.IsNull() || !waitForBlock.IsKnown() {
		return nil
	}

	waiter, err := api.NewResourceWaiter(rs, rname, rtype, th, waitForBlock, s.logger)
	if err != nil {
		return err
	}
	return waiter.Wait(ctx)
}
