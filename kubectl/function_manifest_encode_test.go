// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestAccFunction_ManifestEncode_basic(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
output "test" {
  value = provider::kubectl::manifest_encode({
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "test"
      namespace = "default"
    }
    data = {
      key = "value"
    }
  })
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownOutputValue("test", knownvalue.NotNull()),
					},
				},
			},
		},
	})
}

func TestAccFunction_ManifestEncode_noValidation(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
output "test" {
  value = provider::kubectl::manifest_encode({
    arbitrary = "data"
    nested = {
      key = "val"
    }
  }, false)
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownOutputValue("test", knownvalue.NotNull()),
					},
				},
			},
		},
	})
}

func TestAccFunction_RoundTrip_DecodeEncode(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
locals {
  original_yaml = <<-EOT
apiVersion: v1
kind: ConfigMap
metadata:
  name: roundtrip
  namespace: default
data:
  key: value
EOT
  decoded   = provider::kubectl::manifest_decode(local.original_yaml)
  reencoded = provider::kubectl::manifest_encode(local.decoded)
}

output "reencoded" {
  value = local.reencoded
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownOutputValue("reencoded", knownvalue.NotNull()),
					},
				},
			},
		},
	})
}
