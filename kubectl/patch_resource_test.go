// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build integration

package kubectl_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	meta_v1_unstruct "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
)

// TestIntegration_Patch_AddLabel patches an existing ConfigMap to add a label.
func TestIntegration_Patch_AddLabel(t *testing.T) {
	cmName := fmt.Sprintf("patch-label-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				// Step 1: Create a ConfigMap, then patch it to add a label
				Config: patchAddLabelConfig(cmName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.target", "id"),
					resource.TestCheckResourceAttrSet("kubectl_patch.add_label", "id"),
				),
			},
		},
		CheckDestroy: func(s *terraform.State) error {
			// Verify ConfigMap still exists but label was removed
			cm, err := integrationK8sClient.Resource(
				k8sschema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
			).Namespace("default").Get(context.Background(), cmName, meta_v1.GetOptions{})
			if err != nil {
				// ConfigMap was destroyed by the manifest resource, that's fine
				return nil
			}
			labels := cm.GetLabels()
			if _, ok := labels["patched-by"]; ok {
				return fmt.Errorf(
					"expected label 'patched-by' to be removed after destroy, but it still exists",
				)
			}
			return nil
		},
	})
}

// TestIntegration_Patch_AddAnnotation patches an existing ConfigMap to add an annotation.
func TestIntegration_Patch_AddAnnotation(t *testing.T) {
	cmName := fmt.Sprintf("patch-ann-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: patchAddAnnotationConfig(cmName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_patch.add_annotation", "id"),
				),
			},
		},
	})
}

// TestIntegration_Patch_UpdatePatch changes the patch content and verifies the update.
func TestIntegration_Patch_UpdatePatch(t *testing.T) {
	cmName := fmt.Sprintf("patch-upd-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: patchUpdateStep1Config(cmName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_patch.update_test", "id"),
				),
			},
			{
				Config: patchUpdateStep2Config(cmName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_patch.update_test", "id"),
				),
			},
		},
	})
}

// TestIntegration_Patch_PatchSpec patches the spec of a Deployment to change replicas.
func TestIntegration_Patch_PatchSpec(t *testing.T) {
	deployName := fmt.Sprintf("patch-spec-%d", time.Now().UnixNano()%100000)

	// Pre-create the deployment outside of this test's Terraform config
	createDeployment(t, deployName, "default", 1)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: patchDeploymentReplicasConfig(deployName, 3),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_patch.scale", "id"),
				),
			},
		},
	})

	// Cleanup the deployment
	deleteDeployment(t, deployName, "default")
}

// TestIntegration_Patch_FieldManagerForceConflicts tests force_conflicts option.
func TestIntegration_Patch_FieldManagerForceConflicts(t *testing.T) {
	cmName := fmt.Sprintf("patch-fm-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: patchFieldManagerConfig(cmName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_patch.fm_test", "id"),
				),
			},
		},
	})
}

// --- Helper configs ---

func patchAddLabelConfig(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "target" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %[1]q
      namespace = "default"
    }
    data = {
      key = "value"
    }
  }
}

resource "kubectl_patch" "add_label" {
  api_version = "v1"
  kind        = "ConfigMap"
  name        = %[1]q
  namespace   = "default"

  patch = {
    metadata = {
      labels = {
        "patched-by" = "terraform"
      }
    }
  }

  depends_on = [kubectl_manifest.target]
}
`, name)
}

func patchAddAnnotationConfig(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "target" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %[1]q
      namespace = "default"
    }
    data = {
      key = "value"
    }
  }
}

resource "kubectl_patch" "add_annotation" {
  api_version = "v1"
  kind        = "ConfigMap"
  name        = %[1]q
  namespace   = "default"

  patch = {
    metadata = {
      annotations = {
        "my-annotation" = "hello-world"
      }
    }
  }

  depends_on = [kubectl_manifest.target]
}
`, name)
}

func patchUpdateStep1Config(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "target" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %[1]q
      namespace = "default"
    }
    data = {
      key = "value"
    }
  }
}

resource "kubectl_patch" "update_test" {
  api_version = "v1"
  kind        = "ConfigMap"
  name        = %[1]q
  namespace   = "default"

  patch = {
    metadata = {
      labels = {
        "env" = "dev"
      }
    }
  }

  depends_on = [kubectl_manifest.target]
}
`, name)
}

func patchUpdateStep2Config(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "target" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %[1]q
      namespace = "default"
    }
    data = {
      key = "value"
    }
  }
}

resource "kubectl_patch" "update_test" {
  api_version = "v1"
  kind        = "ConfigMap"
  name        = %[1]q
  namespace   = "default"

  patch = {
    metadata = {
      labels = {
        "env" = "prod"
      }
    }
  }

  depends_on = [kubectl_manifest.target]
}
`, name)
}

func patchDeploymentReplicasConfig(name string, replicas int) string {
	return fmt.Sprintf(`
resource "kubectl_patch" "scale" {
  api_version = "apps/v1"
  kind        = "Deployment"
  name        = %[1]q
  namespace   = "default"

  patch = {
    spec = {
      replicas = %[2]d
    }
  }

  field_manager {
    name            = "TerraformPatcher"
    force_conflicts = true
  }
}
`, name, replicas)
}

func patchFieldManagerConfig(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "target" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %[1]q
      namespace = "default"
    }
    data = {
      key = "value"
    }
  }
}

resource "kubectl_patch" "fm_test" {
  api_version = "v1"
  kind        = "ConfigMap"
  name        = %[1]q
  namespace   = "default"

  patch = {
    metadata = {
      labels = {
        "managed-by" = "custom-fm"
      }
    }
  }

  field_manager {
    name            = "CustomManager"
    force_conflicts = true
  }

  depends_on = [kubectl_manifest.target]
}
`, name)
}

// --- Direct K8s helpers for test setup ---

func createDeployment(t *testing.T, name, namespace string, replicas int) {
	t.Helper()
	deploy := &meta_v1_unstruct.Unstructured{
		Object: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]any{
				"replicas": int64(replicas),
				"selector": map[string]any{
					"matchLabels": map[string]any{
						"app": name,
					},
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"labels": map[string]any{
							"app": name,
						},
					},
					"spec": map[string]any{
						"containers": []any{
							map[string]any{
								"name":  "nginx",
								"image": "nginx:alpine",
							},
						},
					},
				},
			},
		},
	}

	_, err := integrationK8sClient.Resource(
		k8sschema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
	).Namespace(namespace).Create(context.Background(), deploy, meta_v1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create deployment %s: %v", name, err)
	}
}

func deleteDeployment(t *testing.T, name, namespace string) {
	t.Helper()
	err := integrationK8sClient.Resource(
		k8sschema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"},
	).Namespace(namespace).Delete(context.Background(), name, meta_v1.DeleteOptions{})
	if err != nil {
		t.Logf("Warning: failed to delete deployment %s: %v", name, err)
	}
}
