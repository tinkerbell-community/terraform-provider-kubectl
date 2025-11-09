package kubectl_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccDataSourceKubectlPathDocuments_basic(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlPathDocuments_basic(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_path_documents.test", "id"),
					resource.TestCheckResourceAttr(
						"data.kubectl_path_documents.test",
						"pattern",
						"../_examples/manifests/*.yaml",
					),
					resource.TestCheckResourceAttrSet(
						"data.kubectl_path_documents.test",
						"documents.#",
					),
					resource.TestCheckResourceAttrSet(
						"data.kubectl_path_documents.test",
						"manifests.%",
					),
				),
			},
		},
	})
}

func TestAccDataSourceKubectlPathDocuments_templateVars(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlPathDocuments_templateVars(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_path_documents.test", "id"),
					resource.TestCheckResourceAttr(
						"data.kubectl_path_documents.test",
						"vars.%",
						"2",
					),
					resource.TestCheckResourceAttr(
						"data.kubectl_path_documents.test",
						"vars.namespace",
						"test-namespace",
					),
					resource.TestCheckResourceAttr(
						"data.kubectl_path_documents.test",
						"vars.replicas",
						"3",
					),
					resource.TestMatchResourceAttr(
						"data.kubectl_path_documents.test",
						"documents.0",
						regexp.MustCompile(`test-namespace`),
					),
				),
			},
		},
	})
}

func TestAccDataSourceKubectlPathDocuments_sensitiveVars(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlPathDocuments_sensitiveVars(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_path_documents.test", "id"),
					resource.TestCheckResourceAttr(
						"data.kubectl_path_documents.test",
						"sensitive_vars.%",
						"1",
					),
					resource.TestMatchResourceAttr(
						"data.kubectl_path_documents.test",
						"documents.0",
						regexp.MustCompile(`my-secret-password`),
					),
				),
			},
		},
	})
}

func TestAccDataSourceKubectlPathDocuments_disableTemplate(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlPathDocuments_disableTemplate(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_path_documents.test", "id"),
					resource.TestCheckResourceAttr(
						"data.kubectl_path_documents.test",
						"disable_template",
						"true",
					),
					resource.TestMatchResourceAttr(
						"data.kubectl_path_documents.test",
						"documents.0",
						regexp.MustCompile(`\$\{var\.namespace\}`),
					),
				),
			},
		},
	})
}

func testAccDataSourceKubectlPathDocuments_basic() string {
	return `
data "kubectl_path_documents" "test" {
  pattern = "../_examples/manifests/*.yaml"
}
`
}

func testAccDataSourceKubectlPathDocuments_templateVars() string {
	return `
resource "local_file" "test_manifest" {
  filename = "${path.module}/test-manifest.yaml"
  content  = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: ${var.namespace}
    ---
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: test-deployment
      namespace: ${var.namespace}
    spec:
      replicas: ${var.replicas}
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

data "kubectl_path_documents" "test" {
  pattern = "${path.module}/test-manifest.yaml"
  vars = {
    namespace = "test-namespace"
    replicas  = "3"
  }
  depends_on = [local_file.test_manifest]
}
`
}

func testAccDataSourceKubectlPathDocuments_sensitiveVars() string {
	return `
resource "local_file" "test_manifest" {
  filename = "${path.module}/test-secret.yaml"
  content  = <<-YAML
    apiVersion: v1
    kind: Secret
    metadata:
      name: test-secret
      namespace: default
    type: Opaque
    stringData:
      password: ${sensitive_password}
  YAML
}

data "kubectl_path_documents" "test" {
  pattern = "${path.module}/test-secret.yaml"
  sensitive_vars = {
    sensitive_password = "my-secret-password"
  }
  depends_on = [local_file.test_manifest]
}
`
}

func testAccDataSourceKubectlPathDocuments_disableTemplate() string {
	return `
resource "local_file" "test_manifest" {
  filename = "${path.module}/test-no-template.yaml"
  content  = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: test-config
      namespace: default
    data:
      template: "\${var.namespace}"
  YAML
}

data "kubectl_path_documents" "test" {
  pattern          = "${path.module}/test-no-template.yaml"
  disable_template = true
  depends_on       = [local_file.test_manifest]
}
`
}
