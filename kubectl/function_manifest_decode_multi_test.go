// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestAccFunction_ManifestDecodeMulti_basic(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
output "test" {
  value = provider::kubectl::manifest_decode_multi(<<-EOT
apiVersion: v1
kind: ConfigMap
metadata:
  name: first
  namespace: default
data:
  a: "1"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: second
  namespace: default
data:
  b: "2"
EOT
  )
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

func TestAccFunction_ManifestDecodeMulti_singleDocument(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
					value = provider::kubectl::manifest_decode_multi(yamlencode({
						apiVersion: "v1",
						kind: "ConfigMap",
						metadata: {
							name: "only-one",
							namespace: "default"
						},
						data: {
							hello: "world"
						}
					}))
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

func TestAccFunction_ManifestDecodeMulti_emptyDocuments(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
output "test" {
  value = provider::kubectl::manifest_decode_multi(<<-EOT
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: only-one
  namespace: default
---
EOT
  )
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

func TestAccFunction_DecodeMulti_WithResource(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-fn-res")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccFunctionDecodeMultiWithResource(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.from_func", "id"),
				),
			},
		},
	})
}

func testAccFunctionDecodeMultiWithResource(name string) string {
	return fmt.Sprintf(`
locals {
  manifests = provider::kubectl::manifest_decode_multi(<<-EOT
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: default
data:
  from: function
EOT
  )
}

resource "kubectl_manifest" "from_func" {
  manifest = local.manifests[0]
}
`, name)
}
