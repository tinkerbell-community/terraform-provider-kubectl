// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/yaml"
)

// decodeManifestYAML delegates to yaml.DecodeManifestYAML.
func decodeManifestYAML(
	ctx context.Context,
	manifest string,
	validate bool,
) (types.Tuple, diag.Diagnostics) {
	return yaml.DecodeManifestYAML(ctx, manifest, validate)
}

// encodeManifestYAML delegates to yaml.EncodeManifestYAML.
func encodeManifestYAML(v attr.Value, validate bool) (string, diag.Diagnostics) {
	return yaml.EncodeManifestYAML(v, validate)
}
