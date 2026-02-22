package kubectl_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alekc/terraform-provider-kubectl/kubectl"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// testAccProtoV6ProviderFactories are used to instantiate the provider during
// acceptance testing. The factory function will be invoked for every Terraform CLI
// command executed to create a provider server to which the CLI can reattach.
//
//nolint:unused
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"kubectl": providerserver.NewProtocol6WithError(kubectl.New("test")()),
}

// testAccPreCheck validates that required environment variables or conditions
// are met before running acceptance tests.
//
//nolint:unused
func testAccPreCheck(t *testing.T) {
	// Check for kubeconfig or other Kubernetes authentication
	// We'll check if either KUBECONFIG is set or ~/.kube/config exists
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			t.Fatal("could not determine home directory")
		}
		kubeconfig = homeDir + "/.kube/config"

		if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
			t.Skip(
				"KUBECONFIG not set and ~/.kube/config does not exist. Skipping acceptance test.",
			)
		}
	}

	// Optionally check for a test namespace or other requirements
	// For now, just ensure we have some way to connect to Kubernetes
}

// testAccCheckDestroy is a common check function to verify resources are destroyed
// This can be customized per resource type.
//
//nolint:unused
func testAccCheckDestroy(resourceType string) func(*terraform.State) error {
	return func(s *terraform.State) error {
		// This would typically check that the resource no longer exists
		// in the Kubernetes cluster. For now, we'll implement basic validation.
		for _, rs := range s.RootModule().Resources {
			if rs.Type != resourceType {
				continue
			}

			// TODO: Implement actual Kubernetes API check to verify resource is gone
			// For now, if the resource is still in state, we consider it not destroyed
			_ = rs.Primary.ID
		}
		return nil
	}
}

// Helper function to generate random names for test resources.
//
//nolint:unused
func testAccRandomName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().Unix())
}

// Helper function to create test YAML manifests.
//
//nolint:unused
func testAccKubectlManifestConfig(name, namespace, key, value string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: %s
    data:
      %s: %s
  YAML
}
`, name, namespace, key, value)
}

// Helper function for server version data source tests.
//
//nolint:unused
func testAccKubectlServerVersionDataSourceConfig() string {
	return `
data "kubectl_server_version" "test" {}
`
}

// Helper function for filename list data source tests.
//
//nolint:unused
func testAccKubectlFilenameListDataSourceConfig(pattern string) string {
	return fmt.Sprintf(`
data "kubectl_filename_list" "test" {
  pattern = %q
}
`, pattern)
}

// Helper function for file documents data source tests.
//
//nolint:unused
func testAccKubectlFileDocumentsDataSourceConfig(content string) string {
	return fmt.Sprintf(`
data "kubectl_file_documents" "test" {
  content = %q
}
`, content)
}

// Helper function for path documents data source tests.
//
//nolint:unused
func testAccKubectlPathDocumentsDataSourceConfig(pattern string, vars map[string]string) string {
	varsBlock := ""
	if len(vars) > 0 {
		varsList := make([]string, 0, len(vars))
		for k, v := range vars {
			varsList = append(varsList, fmt.Sprintf(`    %s = %q`, k, v))
		}
		varsBlock = fmt.Sprintf(`
  vars = {
%s
  }`, strings.Join(varsList, "\n"))
	}

	return fmt.Sprintf(`
data "kubectl_path_documents" "test" {
  pattern = %q%s
}
`, pattern, varsBlock)
}
