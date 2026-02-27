// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
)

func TestAccFunction_ManifestDecode_basic(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
output "test" {
  value = provider::kubectl::manifest_decode(yamlencode({
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "test"
      namespace = "default"
    }
    data = {
      key = "value"
    }
  }))
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue("test", knownvalue.NotNull()),
					statecheck.ExpectKnownOutputValue(
						"test",
						knownvalue.MapPartial(map[string]knownvalue.Check{
							"apiVersion": knownvalue.StringExact("v1"),
							"kind":       knownvalue.StringExact("ConfigMap"),
							"metadata": knownvalue.MapPartial(map[string]knownvalue.Check{
								"name":      knownvalue.StringExact("test"),
								"namespace": knownvalue.StringExact("default"),
							}),
							"data": knownvalue.MapPartial(map[string]knownvalue.Check{
								"key": knownvalue.StringExact("value"),
							}),
						}),
					),
				},
				// ConfigPlanChecks: resource.ConfigPlanChecks{
				// 	PreApply: []plancheck.PlanCheck{
				// 		plancheck.ExpectKnownOutputValue("test", knownvalue.NotNull()),
				// 	},
				// },
			},
		},
	})
}

func TestAccFunction_ManifestDecode_heredoc(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
output "test" {
  value = provider::kubectl::manifest_decode(<<-EOT
apiVersion: v1
kind: ConfigMap
metadata:
  name: test-heredoc
  namespace: default
data:
  hello: world
EOT
  )
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue("test", knownvalue.NotNull()),
				},
			},
		},
	})
}

func TestAccFunction_ManifestDecode_noValidation(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
output "test" {
  value = provider::kubectl::manifest_decode(yamlencode({
    custom = "data"
    nested = {
      key = "val"
    }
  }), false)
}
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue("test", knownvalue.NotNull()),
				},
			},
		},
	})
}

func TestAccFunction_ManifestDecode_invalidYAML(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
output "test" {
  value = provider::kubectl::manifest_decode("not: [valid: yaml: {{")
}
`,
				ExpectError: regexp.MustCompile(`(error|invalid|failed|cannot|unmarshal)`),
			},
		},
	})
}

func TestAccFunction_ManifestDecode_missingKind(t *testing.T) {
	t.Parallel()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: `
output "test" {
  value = provider::kubectl::manifest_decode(yamlencode({
    apiVersion = "v1"
    metadata = {
      name = "test"
    }
  }))
}
`,
				ExpectError: regexp.MustCompile(`(kind|required|missing|invalid)`),
			},
		},
	})
}
