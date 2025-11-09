package kubectl_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccDataSourceKubectlFileDocuments_singleDocument(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlFileDocuments_singleDocument(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_file_documents.test", "id"),
					resource.TestCheckResourceAttr("data.kubectl_file_documents.test", "documents.#", "1"),
					resource.TestMatchResourceAttr(
						"data.kubectl_file_documents.test",
						"documents.0",
						regexp.MustCompile(`kind:\s*ConfigMap`),
					),
					resource.TestCheckResourceAttr("data.kubectl_file_documents.test", "manifests.%", "1"),
				),
			},
		},
	})
}

func TestAccDataSourceKubectlFileDocuments_multiDocument(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlFileDocuments_multiDocument(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_file_documents.test", "id"),
					resource.TestCheckResourceAttr("data.kubectl_file_documents.test", "documents.#", "3"),
					resource.TestMatchResourceAttr(
						"data.kubectl_file_documents.test",
						"documents.0",
						regexp.MustCompile(`kind:\s*(ConfigMap|Deployment|Service)`),
					),
					resource.TestCheckResourceAttr("data.kubectl_file_documents.test", "manifests.%", "3"),
				),
			},
		},
	})
}

func TestAccDataSourceKubectlFileDocuments_emptyDocuments(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlFileDocuments_emptyDocuments(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_file_documents.test", "id"),
					resource.TestCheckResourceAttr("data.kubectl_file_documents.test", "documents.#", "0"),
					resource.TestCheckResourceAttr("data.kubectl_file_documents.test", "manifests.%", "0"),
				),
			},
		},
	})
}

func testAccDataSourceKubectlFileDocuments_singleDocument() string {
	return `
data "kubectl_file_documents" "test" {
  content = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: test-config
      namespace: default
    data:
      key1: value1
  YAML
}
`
}

func testAccDataSourceKubectlFileDocuments_multiDocument() string {
	return `
data "kubectl_file_documents" "test" {
  content = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: test-config
      namespace: default
    data:
      key1: value1
    ---
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: test-deployment
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
    ---
    apiVersion: v1
    kind: Service
    metadata:
      name: test-service
      namespace: default
    spec:
      selector:
        app: test
      ports:
      - port: 80
        targetPort: 80
  YAML
}
`
}

func testAccDataSourceKubectlFileDocuments_emptyDocuments() string {
	return `
data "kubectl_file_documents" "test" {
  content = "---\n---\n"
}
`
}
