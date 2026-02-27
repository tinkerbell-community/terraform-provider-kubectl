// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccDataSourceKubectlManifest_configMap(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-ds-cm")
	resourceName := "data.kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				// Create a ConfigMap, then read it via data source
				Config: testAccDataSourceConfigMap(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "object"),
				),
			},
		},
	})
}

func TestAccDataSourceKubectlManifest_namespace(t *testing.T) {
	t.Parallel()

	// Read the existing "default" namespace — no need to create it
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceNamespace("default"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName_dsNs, "object"),
				),
			},
		},
	})
}

var resourceName_dsNs = "data.kubectl_manifest.ns" //nolint:revive

func TestAccDataSourceKubectlManifest_createdAndRead(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-ds-cr")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				// Create namespace with resource, then read it with data source
				Config: testAccDataSourceCreatedAndRead(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test_ns", "id"),
					resource.TestCheckResourceAttrSet("data.kubectl_manifest.read_ns", "object"),
				),
			},
		},
	})
}

// --- Config helpers ---

func testAccDataSourceConfigMap(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "setup" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %q
      namespace = "default"
    }
    data = {
      key1 = "value1"
    }
  }
}

data "kubectl_manifest" "test" {
  api_version = "v1"
  kind        = "ConfigMap"
  name        = %q
  namespace   = "default"

  depends_on = [kubectl_manifest.setup]
}
`, name, name)
}

func testAccDataSourceNamespace(name string) string {
	return fmt.Sprintf(`
data "kubectl_manifest" "ns" {
  api_version = "v1"
  kind        = "Namespace"
  name        = %q
}
`, name)
}

func testAccDataSourceCreatedAndRead(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test_ns" {
  manifest = {
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = %q
      labels = {
        "test-label" = "ds-test"
      }
    }
  }
}

data "kubectl_manifest" "read_ns" {
  api_version = "v1"
  kind        = "Namespace"
  name        = %q

  depends_on = [kubectl_manifest.test_ns]
}
`, name, name)
}
