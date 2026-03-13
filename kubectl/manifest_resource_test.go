// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/hashicorp-oss/terraform-provider-kubectl/kubectl"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
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

func TestAccResourceKubectlManifest_CRDPreserveUnknownFields(t *testing.T) {
	t.Parallel()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%100000)
	crdGroup := fmt.Sprintf("testpuf%s.example.com", suffix)
	crName := fmt.Sprintf("test-puf-%s", suffix)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				// A CRD with x-kubernetes-preserve-unknown-fields in its schema
				// previously caused a panic because the OpenAPI extension value
				// arrived as a native Go bool instead of json.RawMessage.
				Config: testAccResourceKubectlManifest_crdPreserveUnknownFields(crdGroup, crName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.crd", "id"),
					resource.TestCheckResourceAttrSet("kubectl_manifest.cr", "id"),
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

func testAccResourceKubectlManifest_crdPreserveUnknownFields(crdGroup, crName string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "crd" {
  manifest = {
    apiVersion = "apiextensions.k8s.io/v1"
    kind       = "CustomResourceDefinition"
    metadata = {
      name = "flexobjects.%[1]s"
    }
    spec = {
      group = "%[1]s"
      names = {
        kind     = "FlexObject"
        listKind = "FlexObjectList"
        plural   = "flexobjects"
        singular = "flexobject"
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
                  type                                   = "object"
                  "x-kubernetes-preserve-unknown-fields" = true
                }
                status = {
                  type                                   = "object"
                  "x-kubernetes-preserve-unknown-fields" = true
                }
              }
            }
          }
        }
      ]
    }
  }
}

resource "kubectl_manifest" "cr" {
  depends_on = [kubectl_manifest.crd]

  manifest = {
    apiVersion = "%[1]s/v1"
    kind       = "FlexObject"
    metadata = {
      name      = "%[2]s"
      namespace = "default"
    }
    spec = {
      arbitraryField = "hello"
      nested = {
        deep = true
        count = 42
      }
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

  wait = {
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

// --- immutable_fields tests ---

func TestAccResourceKubectlManifest_immutableFieldsNoChange(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	configMapName := fmt.Sprintf("test-acc-immut-nochg-%d", time.Now().UnixNano()%100000)

	// When immutable_fields is set but the field doesn't change, update should succeed in place.
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_immutableField(
					configMapName,
					"value1",
					"immutable-val",
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			{
				// Change data.key1 but NOT data.frozen — should update in place
				Config: testAccResourceKubectlManifest_immutableField(
					configMapName,
					"value2",
					"immutable-val",
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_immutableFieldsTriggersReplace(t *testing.T) {
	t.Parallel()

	configMapName := fmt.Sprintf("test-acc-immut-repl-%d", time.Now().UnixNano()%100000)

	// When an immutable field changes, the resource should be replaced (destroy+create).
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_immutableField(
					configMapName,
					"value1",
					"original",
				),
			},
			{
				// Change data.frozen — immutable_fields should trigger replacement
				Config: testAccResourceKubectlManifest_immutableField(
					configMapName,
					"value1",
					"changed",
				),
				// The plan output will show "must be replaced" for the manifest attribute.
				// The test framework handles this automatically (destroy old + create new).
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test", "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_immutableFieldsNestedPath(t *testing.T) {
	t.Parallel()

	deploymentName := fmt.Sprintf("test-acc-immut-nested-%d", time.Now().UnixNano()%100000)

	// immutable_fields with a nested path like spec.selector.matchLabels should
	// trigger replacement when the selector changes.
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_immutableDeployment(
					deploymentName,
					"app-v1",
				),
			},
			{
				// Change spec.selector.matchLabels — should trigger replacement
				Config: testAccResourceKubectlManifest_immutableDeployment(
					deploymentName,
					"app-v2",
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test", "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_immutableFieldsMultiple(t *testing.T) {
	t.Parallel()

	configMapName := fmt.Sprintf("test-acc-immut-multi-%d", time.Now().UnixNano()%100000)

	// Multiple immutable_fields — changing any one should trigger replacement.
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_multipleImmutableFields(
					configMapName,
					"v1",
					"v2",
				),
			},
			{
				// Change only the second immutable field
				Config: testAccResourceKubectlManifest_multipleImmutableFields(
					configMapName,
					"v1",
					"v2-changed",
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test", "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_immutableFieldsMissing(t *testing.T) {
	t.Parallel()

	configMapName := fmt.Sprintf("test-acc-immut-miss-%d", time.Now().UnixNano()%100000)

	// If the immutable field path doesn't exist in the manifest, it should
	// not trigger replacement (both sides are missing → no change).
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_immutableFieldMissing(
					configMapName,
					"value1",
				),
			},
			{
				Config: testAccResourceKubectlManifest_immutableFieldMissing(
					configMapName,
					"value2",
				),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test", "id"),
				),
			},
		},
	})
}

// --- immutable_fields config helpers ---

func testAccResourceKubectlManifest_immutableField(name, value, frozen string) string {
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
      key1   = "%s"
      frozen = "%s"
    }
  }

  fields = {
    immutable = ["data.frozen"]
  }
}
`, name, value, frozen)
}

func testAccResourceKubectlManifest_immutableDeployment(name, selectorLabel string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "apps/v1"
    kind       = "Deployment"
    metadata = {
      name      = "%[1]s"
      namespace = "default"
    }
    spec = {
      replicas = 1
      selector = {
        matchLabels = {
          app = "%[2]s"
        }
      }
      template = {
        metadata = {
          labels = {
            app = "%[2]s"
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

  fields = {
    immutable = ["spec.selector.matchLabels"]
  }

  wait = {
    rollout = true
  }
}
`, name, selectorLabel)
}

func testAccResourceKubectlManifest_multipleImmutableFields(name, field1, field2 string) string {
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
      immut1  = "%s"
      immut2  = "%s"
      mutable = "can-change"
    }
  }

  fields = {
    immutable = ["data.immut1", "data.immut2"]
  }
}
`, name, field1, field2)
}

func testAccResourceKubectlManifest_immutableFieldMissing(name, value string) string {
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

  fields = {
    immutable = ["data.nonexistent_field"]
  }
}
`, name, value)
}

// --- CRUD Tests ---

func TestAccResourceKubectlManifest_ConfigMap_CRUD(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-cm-crud")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		CheckDestroy: testAccCheckManifestDestroy(
			k8sschema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			"default", name,
		),
		Steps: []resource.TestStep{
			// Create
			{
				Config: configMapConfig(name, "default", "key1", "value1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttrSet(resourceName, "object.kind"),
				),
			},
			// Update
			{
				Config: configMapConfig(name, "default", "key1", "value2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			// Import
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateId:           fmt.Sprintf("v1//ConfigMap//%s//default", name),
				ImportStateVerify:       false,
				ImportStateVerifyIgnore: []string{"fields"},
			},
		},
	})
}

func TestAccResourceKubectlManifest_Namespace_CRUD(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-ns-crud")
	resourceName := "kubectl_manifest.test_ns"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		CheckDestroy: testAccCheckManifestDestroy(
			k8sschema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
			"", name,
		),
		Steps: []resource.TestStep{
			{
				Config: namespaceConfig(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			{
				Config: namespaceConfigWithLabels(name, map[string]string{"env": "test"}),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateId:           fmt.Sprintf("v1//Namespace//%s", name),
				ImportStateVerify:       false,
				ImportStateVerifyIgnore: []string{"fields"},
			},
		},
	})
}

func TestAccResourceKubectlManifest_Secret_CRUD(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-secret-crud")
	resourceName := "kubectl_manifest.test_secret"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: secretConfig(name, "default", "admin", "secret123"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttrSet(resourceName, "object.kind"),
				),
			},
			{
				Config: secretConfig(name, "default", "admin", "newsecret456"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_Deployment_CRUD(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-deploy-crud")
	resourceName := "kubectl_manifest.test_deploy"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: deploymentConfig(name, "default", "nginx:alpine"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttrSet(resourceName, "object.kind"),
				),
			},
			{
				Config: deploymentConfig(name, "default", "nginx:latest"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

// --- object computed metadata Tests ---

func TestAccResourceKubectlManifest_objectMetadataComputed(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-obj-meta")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		CheckDestroy: testAccCheckManifestDestroy(
			k8sschema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			"default", name,
		),
		Steps: []resource.TestStep{
			{
				Config: configMapConfig(name, "default", "key1", "value1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttrSet(resourceName, "object.metadata.uid"),
					resource.TestCheckResourceAttrSet(
						resourceName,
						"object.metadata.creationTimestamp",
					),
					resource.TestCheckResourceAttrSet(
						resourceName,
						"object.metadata.resourceVersion",
					),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_objectMetadataComputedAfterUpdate(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-obj-meta-upd")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		CheckDestroy: testAccCheckManifestDestroy(
			k8sschema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			"default", name,
		),
		Steps: []resource.TestStep{
			{
				Config: configMapConfig(name, "default", "key1", "value1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "object.metadata.uid"),
					resource.TestCheckResourceAttrSet(
						resourceName,
						"object.metadata.creationTimestamp",
					),
					resource.TestCheckResourceAttrSet(
						resourceName,
						"object.metadata.resourceVersion",
					),
				),
			},
			{
				Config: configMapConfig(name, "default", "key1", "value2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "object.metadata.uid"),
					resource.TestCheckResourceAttrSet(
						resourceName,
						"object.metadata.creationTimestamp",
					),
					resource.TestCheckResourceAttrSet(
						resourceName,
						"object.metadata.resourceVersion",
					),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_objectMetadataComputedClusterScoped(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-obj-meta-ns")
	resourceName := "kubectl_manifest.test_ns"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		CheckDestroy: testAccCheckManifestDestroy(
			k8sschema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
			"", name,
		),
		Steps: []resource.TestStep{
			{
				Config: namespaceConfig(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "object.metadata.uid"),
					resource.TestCheckResourceAttrSet(
						resourceName,
						"object.metadata.creationTimestamp",
					),
					resource.TestCheckResourceAttrSet(
						resourceName,
						"object.metadata.resourceVersion",
					),
				),
			},
		},
	})
}

// --- computed_fields Tests ---

func TestAccResourceKubectlManifest_computedFieldsDefault(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-computed-default")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestComputedFieldsDefault(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			// Update should not produce drift from server-added annotations/labels
			{
				Config: testAccManifestComputedFieldsDefault(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_computedFieldsCustom(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-computed-custom")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestComputedFieldsCustom(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

// --- field_manager Tests ---

func TestAccResourceKubectlManifest_fieldManager(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-fm")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestFieldManager(name, "CustomManager"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_fieldManagerForceConflicts(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-fm-force")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestFieldManagerForce(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

// --- wait Tests ---

func TestAccResourceKubectlManifest_waitFieldNamespacePhase(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-wait-field")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestWaitFieldNamespace(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_waitFieldRegex(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-wait-regex")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestWaitFieldRegex(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_waitCondition(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-wait-cond")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestWaitCondition(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_waitRollout(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-wait-rollout")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_waitForRollout(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			// Update the image and wait for new rollout
			{
				Config: testAccManifestWaitRolloutUpdate(name, "nginx:alpine"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

// --- apply_only Tests ---

func TestAccResourceKubectlManifest_applyOnly(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-apply-only")
	resourceName := "kubectl_manifest.test"
	gvr := k8sschema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		// With apply_only the resource should NOT be deleted on destroy
		CheckDestroy: func(_ *terraform.State) error {
			ctx := context.Background()
			_, err := integrationK8sClient.Resource(gvr).Namespace("default").Get(
				ctx, name, metav1.GetOptions{},
			)
			if err != nil {
				return fmt.Errorf(
					"apply_only resource should still exist after destroy, but got: %w",
					err,
				)
			}
			// Clean up manually
			_ = integrationK8sClient.Resource(gvr).Namespace("default").Delete(
				ctx, name, metav1.DeleteOptions{},
			)
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: configMapApplyOnlyConfig(name, "default"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttr(resourceName, "delete.skip", "true"),
				),
			},
		},
	})
}

// --- delete_cascade Tests ---

func TestAccResourceKubectlManifest_deleteCascadeForeground(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-cascade-fg")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestDeleteCascade(name, "Foreground"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_deleteCascadeBackground(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-cascade-bg")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestDeleteCascade(name, "Background"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

// --- Service resource test ---

func TestAccResourceKubectlManifest_service(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-svc")
	resourceName := "kubectl_manifest.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestService(name, 80),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
			{
				Config: testAccManifestService(name, 8080),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
				),
			},
		},
	})
}

// --- Multiple resources test ---

func TestAccResourceKubectlManifest_multipleResources(t *testing.T) {
	t.Parallel()

	nsName := testAccRandomName("test-multi-ns")
	cmName := testAccRandomName("test-multi-cm")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: testAccManifestMultipleResources(nsName, cmName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.ns", "id"),
					resource.TestCheckResourceAttrSet("kubectl_manifest.cm", "id"),
				),
			},
		},
	})
}

// --- error_on Tests ---

func TestAccResourceKubectlManifest_errorOnFieldInvalid(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-error-on")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				// Deploy a pod with a non-existent image to trigger error_on
				Config: testAccManifestErrorOnField(name),
				ExpectError: regexp.MustCompile(
					`(ErrImagePull|ImagePullBackOff|error_on|timed out|timeout)`,
				),
			},
		},
	})
}

// --- Config helpers for new tests ---

func testAccManifestComputedFieldsDefault(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
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
`, name)
}

func testAccManifestComputedFieldsCustom(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
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

  fields = {
    computed = [
      "metadata.annotations",
      "metadata.labels",
      "metadata.finalizers",
    ]
  }
}
`, name)
}

func testAccManifestFieldManager(name, manager string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
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

  field_manager = {
    name = %q
  }
}
`, name, manager)
}

func testAccManifestFieldManagerForce(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
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

  field_manager = {
    name            = "TerraformForce"
    force_conflicts = true
  }
}
`, name)
}

func testAccManifestWaitFieldNamespace(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = %q
    }
  }

  wait = {
    fields = [
      {
        key   = "status.phase"
        value = "Active"
      }
    ]
  }
}
`, name)
}

func testAccManifestWaitFieldRegex(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = %q
    }
  }

  wait = {
    fields = [
      {
        key        = "status.phase"
        value      = "Activ.*"
        value_type = "regex"
      }
    ]
  }
}
`, name)
}

func testAccManifestWaitCondition(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "apps/v1"
    kind       = "Deployment"
    metadata = {
      name      = %q
      namespace = "default"
    }
    spec = {
      replicas = 1
      selector = {
        matchLabels = {
          app = %q
        }
      }
      template = {
        metadata = {
          labels = {
            app = %q
          }
        }
        spec = {
          containers = [
            {
              name  = "nginx"
              image = "nginx:alpine"
            }
          ]
        }
      }
    }
  }

  wait = {
    conditions = [
      {
        type   = "Available"
        status = "True"
      }
    ]
  }
}
`, name, name, name)
}

func testAccManifestWaitRolloutUpdate(name, image string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "apps/v1"
    kind       = "Deployment"
    metadata = {
      name      = %q
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
              image = %q
            }
          ]
        }
      }
    }
  }

  wait = {
    rollout = true
  }
}
`, name, image)
}

func testAccManifestDeleteCascade(name, cascade string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
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

  delete = {
    cascade = %q
  }
}
`, name, cascade)
}

func testAccManifestService(name string, port int) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "Service"
    metadata = {
      name      = %q
      namespace = "default"
    }
    spec = {
      selector = {
        app = "test"
      }
      ports = [
        {
          port       = %d
          targetPort = "http"
          protocol   = "TCP"
        }
      ]
    }
  }
}
`, name, port)
}

func testAccManifestMultipleResources(nsName, cmName string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "ns" {
  manifest = {
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = %q
    }
  }
}

resource "kubectl_manifest" "cm" {
  depends_on = [kubectl_manifest.ns]

  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %q
      namespace = %q
    }
    data = {
      key = "value"
    }
  }
}
`, nsName, cmName, nsName)
}

func testAccManifestErrorOnField(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "Pod"
    metadata = {
      name      = %q
      namespace = "default"
    }
    spec = {
      containers = [
        {
          name  = "fail"
          image = "this-image-does-not-exist:never"
        }
      ]
    }
  }

  wait = {
    fields = [
      {
        key   = "status.phase"
        value = "Running"
      }
    ]
  }

  error = {
    fields = [
      {
        key   = "status.containerStatuses.0.state.waiting.reason"
        value = "ErrImagePull|ImagePullBackOff"
      }
    ]
  }

  timeouts = {
    create = "30s"
  }
}
`, name)
}

func TestErrorConditionError_implements_error(t *testing.T) {
	var err error = &kubectl.MatchingConditionError{Msg: "test error"}
	if err.Error() != "test error" {
		t.Fatalf("expected 'test error', got %q", err.Error())
	}
}

func TestErrorConditionError_detectable_via_errorsAs(t *testing.T) {
	original := &kubectl.MatchingConditionError{Msg: "field matched"}
	wrapped := fmt.Errorf("failed to wait: %w", original)

	var ece *kubectl.MatchingConditionError
	if !errors.As(wrapped, &ece) {
		t.Fatal("errors.As should detect errorConditionError through wrapping")
	}
	if ece.Msg != "field matched" {
		t.Fatalf("unexpected message: %q", ece.Msg)
	}
}

func TestErrorConditionError_not_detected_for_other_errors(t *testing.T) {
	other := fmt.Errorf("some other error")
	var ece *kubectl.MatchingConditionError
	if errors.As(other, &ece) {
		t.Fatal("errors.As should NOT detect errorConditionError for unrelated errors")
	}
}

func TestErrorConditionError_survives_backoff_permanent(t *testing.T) {
	original := &kubectl.MatchingConditionError{Msg: "condition met"}
	permanent := backoff.Permanent(original)

	// After backoff.Retry returns a Permanent error, the inner error is unwrapped.
	// Verify we can still detect the errorConditionError.
	var ece *kubectl.MatchingConditionError
	if !errors.As(permanent, &ece) {
		t.Fatal("errors.As should detect errorConditionError through backoff.Permanent wrapping")
	}
}

func TestErrorConditionError_double_wrapped(t *testing.T) {
	original := &kubectl.MatchingConditionError{Msg: "crash loop"}
	wrapped := fmt.Errorf("failed to wait for conditions: %w",
		fmt.Errorf("wait error: %w", original))

	var ece *kubectl.MatchingConditionError
	if !errors.As(wrapped, &ece) {
		t.Fatal("errors.As should detect errorConditionError through multiple layers of wrapping")
	}
	if ece.Msg != "crash loop" {
		t.Fatalf("unexpected message: %q", ece.Msg)
	}
}

// --- manifest_wo Tests ---

func TestAccResourceKubectlManifest_manifestWo_basic(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-mwo-basic")
	resourceName := "kubectl_manifest.test"
	gvr := k8sschema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		CheckDestroy:             testAccCheckManifestDestroy(gvr, "default", name),
		Steps: []resource.TestStep{
			{
				// manifest_wo should deep-merge "password" into the ConfigMap's data.
				Config: testAccManifestWoBasic(name, "secret-value"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					// Verify the resource was created on the server with the merged data.
					testAccCheckK8sField(gvr, "default", name, "data.public", "visible"),
					testAccCheckK8sField(gvr, "default", name, "data.password", "secret-value"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_manifestWo_update(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-mwo-update")
	resourceName := "kubectl_manifest.test"
	gvr := k8sschema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		CheckDestroy:             testAccCheckManifestDestroy(gvr, "default", name),
		Steps: []resource.TestStep{
			{
				Config: testAccManifestWoBasic(name, "secret-v1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					testAccCheckK8sField(gvr, "default", name, "data.password", "secret-v1"),
				),
			},
			{
				// Changing manifest_wo should trigger an update.
				Config: testAccManifestWoBasic(name, "secret-v2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					testAccCheckK8sField(gvr, "default", name, "data.password", "secret-v2"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_manifestWo_nestedMerge(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-mwo-nested")
	resourceName := "kubectl_manifest.test"
	gvr := k8sschema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		CheckDestroy:             testAccCheckManifestDestroy(gvr, "default", name),
		Steps: []resource.TestStep{
			{
				// manifest provides data.public, manifest_wo provides data.secret.
				// They should be deep-merged under data.
				Config: testAccManifestWoNested(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					testAccCheckK8sField(gvr, "default", name, "data.public", "visible"),
					testAccCheckK8sField(gvr, "default", name, "data.secret", "hidden"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_manifestWo_deepNestedObject(t *testing.T) {
	t.Parallel()

	name := testAccRandomName("test-mwo-deep")
	resourceName := "kubectl_manifest.test"
	gvr := k8sschema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheck(t) },
		ProtoV6ProviderFactories: integrationProviderCfg,
		CheckDestroy:             testAccCheckManifestDestroy(gvr, "default", name),
		Steps: []resource.TestStep{
			{
				// manifest defines metadata.labels.app and data.public.
				// manifest_wo adds metadata.labels.secret-label and data.token.
				// Deep merge should preserve both manifest and manifest_wo values
				// at every nesting level.
				Config: testAccManifestWoDeepNested(name, "my-token-value"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					testAccCheckK8sField(gvr, "default", name, "metadata.labels.app", "myapp"),
					testAccCheckK8sField(
						gvr,
						"default",
						name,
						"metadata.labels.secret-label",
						"wo-injected",
					),
					testAccCheckK8sField(gvr, "default", name, "data.public", "visible"),
					testAccCheckK8sField(gvr, "default", name, "data.token", "my-token-value"),
				),
			},
			{
				// Update the write-only token — should trigger an update.
				Config: testAccManifestWoDeepNested(name, "updated-token"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					testAccCheckK8sField(gvr, "default", name, "data.token", "updated-token"),
					// Existing fields should be preserved.
					testAccCheckK8sField(gvr, "default", name, "data.public", "visible"),
					testAccCheckK8sField(gvr, "default", name, "metadata.labels.app", "myapp"),
				),
			},
		},
	})
}

// --- manifest_wo config helpers ---

func testAccManifestWoDeepNested(name, token string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %q
      namespace = "default"
      labels = {
        app = "myapp"
      }
    }
    data = {
      public = "visible"
    }
  }

  manifest_wo = {
    metadata = {
      labels = {
        "secret-label" = "wo-injected"
      }
    }
    data = {
      token = %q
    }
  }
}
`, name, token)
}

func testAccManifestWoBasic(name, password string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %q
      namespace = "default"
    }
    data = {
      public = "visible"
    }
  }

  manifest_wo = {
    data = {
      password = %q
    }
  }
}
`, name, password)
}

func testAccManifestWoNested(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %q
      namespace = "default"
    }
    data = {
      public = "visible"
    }
  }

  manifest_wo = {
    data = {
      secret = "hidden"
    }
  }
}
`, name)
}

// testAccCheckK8sField verifies a specific field value on a Kubernetes resource.
func testAccCheckK8sField(
	gvr k8sschema.GroupVersionResource,
	namespace, name, fieldPath, expected string, //nolint:unparam
) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		ctx := context.Background()
		obj, err := integrationK8sClient.Resource(gvr).
			Namespace(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get resource %s/%s: %w", namespace, name, err)
		}

		// Navigate the field path (supports dot-separated paths like "data.password").
		parts := splitFieldPath(fieldPath)
		current := obj.Object
		for i, part := range parts {
			if i == len(parts)-1 {
				// Leaf — check the value.
				val, ok := current[part]
				if !ok {
					return fmt.Errorf(
						"field %q not found in resource %s/%s (available keys: %v)",
						fieldPath, namespace, name, mapKeys(current),
					)
				}
				strVal := fmt.Sprintf("%v", val)
				if strVal != expected {
					return fmt.Errorf(
						"field %q = %q, want %q",
						fieldPath, strVal, expected,
					)
				}
				return nil
			}
			// Non-leaf — descend into nested map.
			sub, ok := current[part].(map[string]any)
			if !ok {
				return fmt.Errorf(
					"field %q: segment %q is not a map (type %T)",
					fieldPath, part, current[part],
				)
			}
			current = sub
		}
		return fmt.Errorf("field path %q is empty", fieldPath)
	}
}

func splitFieldPath(path string) []string {
	var parts []string
	for _, p := range regexp.MustCompile(`\.`).Split(path, -1) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
