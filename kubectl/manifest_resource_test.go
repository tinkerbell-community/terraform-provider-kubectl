package kubectl_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccResourceKubectlManifest_basic(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	configMapName := "test-config-basic"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_basic(configMapName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(resourceName, "id"),
					resource.TestCheckResourceAttr(resourceName, "api_version", "v1"),
					resource.TestCheckResourceAttr(resourceName, "kind", "ConfigMap"),
					resource.TestCheckResourceAttr(resourceName, "name", configMapName),
					resource.TestCheckResourceAttr(resourceName, "namespace", "default"),
					resource.TestCheckResourceAttrSet(resourceName, "uid"),
					resource.TestCheckResourceAttrSet(resourceName, "resource_version"),
					resource.TestMatchResourceAttr(
						resourceName,
						"yaml_body",
						regexp.MustCompile(`key1:\s*value1`),
					),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     fmt.Sprintf("v1//ConfigMap//%s//default", configMapName),
			},
		},
	})
}

func TestAccResourceKubectlManifest_update(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	configMapName := "test-config-update"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_configMap(configMapName, "value1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", configMapName),
					resource.TestMatchResourceAttr(
						resourceName,
						"yaml_body",
						regexp.MustCompile(`key1:\s*value1`),
					),
				),
			},
			{
				Config: testAccResourceKubectlManifest_configMap(configMapName, "value2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", configMapName),
					resource.TestMatchResourceAttr(
						resourceName,
						"yaml_body",
						regexp.MustCompile(`key1:\s*value2`),
					),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_serverSideApply(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	configMapName := "test-config-ssa"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_serverSideApply(configMapName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "server_side_apply", "true"),
					resource.TestCheckResourceAttr(resourceName, "name", configMapName),
					resource.TestCheckResourceAttrSet(resourceName, "uid"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_overrideNamespace(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	configMapName := "test-config-namespace"
	overrideNamespace := "kube-system"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_overrideNamespace(configMapName, overrideNamespace),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", configMapName),
					resource.TestCheckResourceAttr(resourceName, "namespace", overrideNamespace),
					resource.TestCheckResourceAttr(resourceName, "override_namespace", overrideNamespace),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_waitForRollout(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	deploymentName := "test-deployment-rollout"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_waitForRollout(deploymentName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "kind", "Deployment"),
					resource.TestCheckResourceAttr(resourceName, "name", deploymentName),
					resource.TestCheckResourceAttr(resourceName, "wait_for_rollout", "true"),
					resource.TestCheckResourceAttrSet(resourceName, "uid"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_ignoreFields(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	configMapName := "test-config-ignore"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_ignoreFields(configMapName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "name", configMapName),
					resource.TestCheckResourceAttr(resourceName, "ignore_fields.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "ignore_fields.0", "metadata.annotations"),
				),
			},
		},
	})
}

func TestAccResourceKubectlManifest_clusterScoped(t *testing.T) {
	t.Parallel()

	resourceName := "kubectl_manifest.test"
	namespaceName := "test-namespace"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlManifest_clusterScoped(namespaceName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "kind", "Namespace"),
					resource.TestCheckResourceAttr(resourceName, "name", namespaceName),
					resource.TestCheckResourceAttr(resourceName, "namespace", ""),
					resource.TestCheckResourceAttrSet(resourceName, "uid"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     fmt.Sprintf("v1//Namespace//%s", namespaceName),
			},
		},
	})
}

func testAccResourceKubectlManifest_basic(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: default
    data:
      key1: value1
  YAML
}
`, name)
}

func testAccResourceKubectlManifest_configMap(name, value string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: default
    data:
      key1: %s
  YAML
}
`, name, value)
}

func testAccResourceKubectlManifest_serverSideApply(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  server_side_apply = true
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: default
    data:
      key1: value1
  YAML
}
`, name)
}

func testAccResourceKubectlManifest_overrideNamespace(name, namespace string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  override_namespace = "%s"
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: default
    data:
      key1: value1
  YAML
}
`, namespace, name)
}

func testAccResourceKubectlManifest_waitForRollout(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  wait_for_rollout = true
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: %s
      namespace: default
    spec:
      replicas: 1
      selector:
        matchLabels:
          app: test
      template:
        metadata:
          labels:
            app: test
        spec:
          containers:
          - name: nginx
            image: nginx:latest
  YAML
}
`, name)
}

func testAccResourceKubectlManifest_ignoreFields(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  ignore_fields = ["metadata.annotations"]
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: %s
      namespace: default
    data:
      key1: value1
  YAML
}
`, name)
}

func testAccResourceKubectlManifest_clusterScoped(name string) string {
	return fmt.Sprintf(`
resource "kubectl_manifest" "test" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: %s
  YAML
}
`, name)
}
