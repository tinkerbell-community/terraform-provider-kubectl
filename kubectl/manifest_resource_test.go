// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build integration

package kubectl_test

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccResourceKubectlManifest_basic(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	configMapName := fmt.Sprintf("test-acc-basic-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_basic(configMapName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: false,
				ImportStateId:     fmt.Sprintf("v1//ConfigMap//%s//default", configMapName),
			},
		},
	})
}

func TestAccResourceKubectlManifest_update(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	configMapName := fmt.Sprintf("test-acc-update-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_configMap(configMapName, "value1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			{
				Config: testAccResourceKubectlManifest_configMap(configMapName, "value2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_waitForRollout(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	deploymentName := fmt.Sprintf("test-acc-rollout-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_waitForRollout(deploymentName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_clusterScoped(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	namespaceName := fmt.Sprintf("test-acc-ns-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_clusterScoped(namespaceName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: false,
				ImportStateId:     fmt.Sprintf("v1//Namespace//%s", namespaceName),
			},
		},
	})
}

func TestAccResourceKubectlManifest_CRDThenInstance(t *testing.T) {
	t.Parallel()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%100000)
	crdGroup := fmt.Sprintf("testing%s.example.com", suffix)
	crName := fmt.Sprintf("test-widget-%s", suffix)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				// Step 1: Apply both CRD and CR in a single config.
				// The CR depends_on the CRD so Terraform creates them in order.
				// During plan, the CR's custom apiVersion won't exist in OpenAPI yet —
				// the provider must gracefully skip schema validation for unknown types.
				Config: testAccResourceKubectlManifest_crdAndInstance(crdGroup, crName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.crd", "id"),
					resource.TestCheckResourceAttrSet("kubectl_manifest.widget", "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_CRDInstanceInvalidField(t *testing.T) {
	t.Parallel()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%100000)
	crdGroup := fmt.Sprintf("testing%s.example.com", suffix)
	crName := fmt.Sprintf("test-widget-bad-%s", suffix)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				// The CR uses spec.size as a string ("not-a-number") but the CRD
				// schema defines it as integer. The API server should reject this.
				Config: testAccResourceKubectlManifest_crdAndBadInstance(crdGroup, crName),
				ExpectError: regexp.MustCompile(
					`(strict decoding error|failed to apply|spec\.size|invalid|validation)`,
				),
			},
		},
	})
}

func testAccResourceKubectlManifest_crdAndInstance(crdGroup, crName string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "crd" {
  manifest = {
    apiVersion = "apiextensions.k8s.io/v1"
    kind       = "CustomResourceDefinition"
    metadata = {
      name = "widgets.%[1]s"
    }
    spec = {
      group = "%[1]s"
      names = {
        kind     = "Widget"
        listKind = "WidgetList"
        plural   = "widgets"
        singular = "widget"
      }
      scope = "Namespaced"
      versions = [
        {
          name    = "v1"
          served  = true
          storage = true
          schema = {
            openAPIV3Schema = {
              type = "object"
              properties = {
                spec = {
                  type = "object"
                  properties = {
                    color = {
                      type = "string"
                    }
                    size = {
                      type = "integer"
                    }
                  }
                }
              }
            }
          }
        }
      ]
    }
  }
}

resource "kubectl_manifest" "widget" {
  depends_on = [kubectl_manifest.crd]

  manifest = {
    apiVersion = "%[1]s/v1"
    kind       = "Widget"
    metadata = {
      name      = "%[2]s"
      namespace = "default"
    }
    spec = {
      color = "blue"
      size  = 42
    }
  }
}
`, crdGroup, crName)
}

func testAccResourceKubectlManifest_crdAndBadInstance(crdGroup, crName string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "crd" {
  manifest = {
    apiVersion = "apiextensions.k8s.io/v1"
    kind       = "CustomResourceDefinition"
    metadata = {
      name = "widgets.%[1]s"
    }
    spec = {
      group = "%[1]s"
      names = {
        kind     = "Widget"
        listKind = "WidgetList"
        plural   = "widgets"
        singular = "widget"
      }
      scope = "Namespaced"
      versions = [
        {
          name    = "v1"
          served  = true
          storage = true
          schema = {
            openAPIV3Schema = {
              type = "object"
              properties = {
                spec = {
                  type = "object"
                  properties = {
                    color = {
                      type = "string"
                    }
                    size = {
                      type = "integer"
                    }
                  }
                }
              }
            }
          }
        }
      ]
    }
  }
}

resource "kubectl_manifest" "widget" {
  depends_on = [kubectl_manifest.crd]

  manifest = {
    apiVersion = "%[1]s/v1"
    kind       = "Widget"
    metadata = {
      name      = "%[2]s"
      namespace = "default"
    }
    spec = {
      color = "blue"
      size  = "not-a-number"
    }
  }
}
`, crdGroup, crName)
}

func testAccResourceKubectlManifest_basic(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "%s"
      namespace = "default"
    }
    data = {
      key1 = "value1"
    }
  }
}
`, name)
}

func testAccResourceKubectlManifest_configMap(name, value string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "%s"
      namespace = "default"
    }
    data = {
      key1 = "%s"
    }
  }
}
`, name, value)
}

func testAccResourceKubectlManifest_waitForRollout(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "apps/v1"
    kind       = "Deployment"
    metadata = {
      name      = "%s"
      namespace = "default"
    }
    spec = {
      replicas = 1
      selector = {
        matchLabels = {
          app = "test"
        }
      }
      template = {
        metadata = {
          labels = {
            app = "test"
          }
        }
        spec = {
          containers = [
            {
              name  = "nginx"
              image = "nginx:latest"
            }
          ]
        }
      }
    }
  }

  wait {
    rollout = true
  }
}
`, name)
}

func testAccResourceKubectlManifest_clusterScoped(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = "%s"
    }
  }
}
`, name)
}
