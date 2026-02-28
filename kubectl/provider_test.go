package kubectl_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	k3sContainer           *k3s.K3sContainer
	integrationK8sClient   dynamic.Interface
	integrationProviderCfg map[string]func() (tfprotov6.ProviderServer, error)
)

func TestMain(m *testing.M) {
	if os.Getenv("TF_ACC") == "" {
		// short circuit non acceptance test runs
		os.Exit(m.Run())
	}

	os.Exit(runAcceptanceTests(m))
}

func runAcceptanceTests(m *testing.M) int {
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
	_ = kubeconfigFile.Close()

	// Set KUBECONFIG so the provider picks it up
	_ = os.Setenv("KUBECONFIG", kubeconfigPath)
	_ = os.Setenv("KUBE_CONFIG_PATH", kubeconfigPath)

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

	// Wait for K3s API server to be fully operational
	if err := waitForK3sReady(ctx); err != nil {
		log.Fatalf("K3s cluster did not become ready: %v", err)
	}

	log.Println("K3s cluster is ready")

	// Run the tests
	code := m.Run()

	// Cleanup
	log.Println("Stopping K3s container...")
	if err := k3sContainer.Terminate(ctx); err != nil {
		log.Printf("Failed to terminate K3s container: %v", err)
	}
	_ = os.Remove(kubeconfigPath)

	return code
}

// waitForK3sReady polls the Kubernetes API until it is fully operational.
// It verifies that namespaces and pods can be listed, similar to the unifi
// provider's waitForUniFiAPI pattern.
func waitForK3sReady(ctx context.Context) error {
	maxRetries := 30
	retryDelay := 2 * time.Second

	log.Printf(
		"Waiting for K3s API to be ready (max %d attempts, %v between attempts)...",
		maxRetries, retryDelay,
	)

	for i := range maxRetries {
		// Verify we can list namespaces (API server operational)
		nsList, err := integrationK8sClient.Resource(
			k8sschema.GroupVersionResource{Version: "v1", Resource: "namespaces"},
		).List(ctx, metav1.ListOptions{})
		if err != nil {
			if i < maxRetries-1 {
				if (i+1)%10 == 0 {
					log.Printf(
						"Still waiting for API server... (attempt %d/%d): %v",
						i+1,
						maxRetries,
						err,
					)
				}
				time.Sleep(retryDelay)
				continue
			}
			return fmt.Errorf("K3s API not ready after %d attempts: %w", maxRetries, err)
		}

		if len(nsList.Items) == 0 {
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return fmt.Errorf("no namespaces found after %d attempts", maxRetries)
		}

		// Verify we can list pods in kube-system (cluster bootstrapped)
		podList, err := integrationK8sClient.Resource(
			k8sschema.GroupVersionResource{Version: "v1", Resource: "pods"},
		).Namespace("kube-system").List(ctx, metav1.ListOptions{})
		if err != nil {
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return fmt.Errorf("cannot list kube-system pods after %d attempts: %w", maxRetries, err)
		}

		if len(podList.Items) == 0 {
			if i < maxRetries-1 {
				time.Sleep(retryDelay)
				continue
			}
			return fmt.Errorf("kube-system has no pods after %d attempts", maxRetries)
		}

		log.Printf("K3s API ready (%d namespaces, %d kube-system pods) after %d attempts",
			len(nsList.Items), len(podList.Items), i+1)
		return nil
	}

	return fmt.Errorf("K3s API did not become ready after %d attempts", maxRetries)
}

// preCheck validates the K3s environment is set up before running acceptance tests.
func preCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC must be set for acceptance tests")
	}
	if os.Getenv("KUBECONFIG") == "" && os.Getenv("KUBE_CONFIG_PATH") == "" {
		t.Fatal("KUBECONFIG or KUBE_CONFIG_PATH must be set for acceptance tests")
	}
}

// testAccCheckManifestDestroy verifies a Kubernetes resource has been deleted.
func testAccCheckManifestDestroy(
	gvr k8sschema.GroupVersionResource,
	namespace, name string,
) func(*terraform.State) error {
	return func(_ *terraform.State) error {
		ctx := context.Background()
		var err error
		if namespace != "" {
			_, err = integrationK8sClient.Resource(gvr).
				Namespace(namespace).
				Get(ctx, name, metav1.GetOptions{})
		} else {
			_, err = integrationK8sClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
		}
		if err == nil {
			return fmt.Errorf("resource %s/%s still exists", namespace, name)
		}
		return nil
	}
}

// testAccRandomName generates a unique name for test resources.
func testAccRandomName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%100000)
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
  delete = {
    skip = true
  }

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
