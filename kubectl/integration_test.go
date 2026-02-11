// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build integration

package kubectl_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/alekc/terraform-provider-kubectl/kubectl"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	k3sContainer           *k3s.K3sContainer
	integrationK8sClient   dynamic.Interface
	integrationProviderCfg map[string]func() (tfprotov6.ProviderServer, error)
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	// Start K3s container
	log.Println("Starting K3s container...")
	var err error
	k3sContainer, err = k3s.Run(ctx, "rancher/k3s:v1.31.2-k3s1")
	if err != nil {
		log.Fatalf("Failed to start K3s container: %v", err)
	}

	// Get kubeconfig from container
	kubeConfigYaml, err := k3sContainer.GetKubeConfig(ctx)
	if err != nil {
		log.Fatalf("Failed to get kubeconfig from K3s container: %v", err)
	}

	// Write kubeconfig to a temp file
	kubeconfigFile, err := os.CreateTemp("", "k3s-kubeconfig-*.yaml")
	if err != nil {
		log.Fatalf("Failed to create temp kubeconfig file: %v", err)
	}
	kubeconfigPath := kubeconfigFile.Name()
	if _, err := kubeconfigFile.Write(kubeConfigYaml); err != nil {
		log.Fatalf("Failed to write kubeconfig: %v", err)
	}
	kubeconfigFile.Close()

	// Set KUBECONFIG so the provider picks it up
	os.Setenv("KUBECONFIG", kubeconfigPath)
	os.Setenv("KUBE_CONFIG_PATH", kubeconfigPath)

	// Build a dynamic client for direct K8s assertions
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeConfigYaml)
	if err != nil {
		log.Fatalf("Failed to build REST config: %v", err)
	}
	integrationK8sClient, err = dynamic.NewForConfig(restConfig)
	if err != nil {
		log.Fatalf("Failed to create dynamic K8s client: %v", err)
	}

	// Build provider factories
	integrationProviderCfg = map[string]func() (tfprotov6.ProviderServer, error){
		"kubectl": providerserver.NewProtocol6WithError(kubectl.New("test")()),
	}

	log.Println("K3s cluster is ready")

	// Wait a moment for the API server to be fully ready
	time.Sleep(2 * time.Second)

	// Run the tests
	code := m.Run()

	// Cleanup
	log.Println("Stopping K3s container...")
	if err := k3sContainer.Terminate(ctx); err != nil {
		log.Printf("Failed to terminate K3s container: %v", err)
	}
	os.Remove(kubeconfigPath)

	os.Exit(code)
}

// -- ConfigMap CRUD Tests --

func TestIntegration_ConfigMap_CreateRead(t *testing.T) {
	name := fmt.Sprintf("test-cm-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: configMapConfig(name, "default", "hello", "world"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test", "id"),
				),
			},
		},
	})
}

func TestIntegration_ConfigMap_Update(t *testing.T) {
	name := fmt.Sprintf("test-cm-upd-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: configMapConfig(name, "default", "key1", "value1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test", "id"),
				),
			},
			{
				Config: configMapConfig(name, "default", "key1", "value2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test", "id"),
				),
			},
		},
	})
}

func TestIntegration_ConfigMap_Import(t *testing.T) {
	name := fmt.Sprintf("test-cm-imp-%d", time.Now().UnixNano()%100000)
	importID := fmt.Sprintf("v1//ConfigMap//%s//default", name)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: configMapConfig(name, "default", "imported", "true"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test", "id"),
				),
			},
			{
				ResourceName:            "kubectl_manifest.test",
				ImportState:             true,
				ImportStateId:           importID,
				ImportStateVerify:       false, // Schema differs between import and config
				ImportStateVerifyIgnore: []string{"computed_fields"},
			},
		},
	})
}

// -- Namespace (cluster-scoped) Tests --

func TestIntegration_Namespace_CreateDelete(t *testing.T) {
	name := fmt.Sprintf("test-ns-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: namespaceConfig(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test_ns", "id"),
				),
			},
		},
	})
}

func TestIntegration_Namespace_UpdateLabels(t *testing.T) {
	name := fmt.Sprintf("test-ns-lbl-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: namespaceConfig(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test_ns", "id"),
				),
			},
			{
				Config: namespaceConfigWithLabels(name, map[string]string{"env": "test"}),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test_ns", "id"),
				),
			},
		},
	})
}

func TestIntegration_Namespace_Import(t *testing.T) {
	name := fmt.Sprintf("test-ns-imp-%d", time.Now().UnixNano()%100000)
	importID := fmt.Sprintf("v1//Namespace//%s", name)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: namespaceConfig(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test_ns", "id"),
				),
			},
			{
				ResourceName:            "kubectl_manifest.test_ns",
				ImportState:             true,
				ImportStateId:           importID,
				ImportStateVerify:       false,
				ImportStateVerifyIgnore: []string{"computed_fields"},
			},
		},
	})
}

// -- Secret Tests --

func TestIntegration_Secret_CreateRead(t *testing.T) {
	name := fmt.Sprintf("test-secret-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: secretConfig(name, "default", "myuser", "mypass"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test_secret", "id"),
				),
			},
		},
	})
}

// -- Deployment (with spec) Tests --

func TestIntegration_Deployment_CreateRead(t *testing.T) {
	name := fmt.Sprintf("test-deploy-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: deploymentConfig(name, "default", "nginx:alpine"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test_deploy", "id"),
				),
			},
		},
	})
}

func TestIntegration_Deployment_Update(t *testing.T) {
	name := fmt.Sprintf("test-deploy-upd-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: deploymentConfig(name, "default", "nginx:alpine"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test_deploy", "id"),
				),
			},
			{
				Config: deploymentConfig(name, "default", "nginx:latest"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test_deploy", "id"),
				),
			},
		},
	})
}

// -- ApplyOnly Tests --

func TestIntegration_ConfigMap_ApplyOnly(t *testing.T) {
	name := fmt.Sprintf("test-cm-ao-%d", time.Now().UnixNano()%100000)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: integrationProviderCfg,
		Steps: []resource.TestStep{
			{
				Config: configMapApplyOnlyConfig(name, "default"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_manifest.test", "id"),
				),
			},
		},
	})
}

// -- Config Helpers --

func configMapConfig(name, namespace, key, value string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = %q
      namespace = %q
    }
    data = {
      %s = %q
    }
  }
}
`, name, namespace, key, value)
}

func configMapApplyOnlyConfig(name, namespace string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  apply_only = true

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
`, name, namespace)
}

func namespaceConfig(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test_ns" {
  manifest = {
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = %q
    }
  }
}
`, name)
}

func namespaceConfigWithLabels(name string, labels map[string]string) string {
	labelsHCL := ""
	for k, v := range labels {
		labelsHCL += fmt.Sprintf("        %s = %q\n", k, v)
	}
	return fmt.Sprintf(`
resource "kubectl_manifest" "test_ns" {
  manifest = {
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = %q
      labels = {
%s      }
    }
  }
}
`, name, labelsHCL)
}

func secretConfig(name, namespace, user, password string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test_secret" {
  manifest = {
    apiVersion = "v1"
    kind       = "Secret"
    metadata = {
      name      = %q
      namespace = %q
    }
    stringData = {
      username = %q
      password = %q
    }
  }
}
`, name, namespace, user, password)
}

func deploymentConfig(name, namespace, image string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test_deploy" {
  manifest = {
    apiVersion = "apps/v1"
    kind       = "Deployment"
    metadata = {
      name      = %q
      namespace = %q
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
              name  = "main"
              image = %q
            }
          ]
        }
      }
    }
  }
}
`, name, namespace, name, name, image)
}
