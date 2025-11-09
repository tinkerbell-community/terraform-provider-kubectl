package kubectl_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccDataSourceKubectlServerVersion_basic(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlServerVersion_basic(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_server_version.test", "id"),
					resource.TestCheckResourceAttrSet(
						"data.kubectl_server_version.test",
						"version",
					),
					resource.TestCheckResourceAttrSet("data.kubectl_server_version.test", "major"),
					resource.TestCheckResourceAttrSet("data.kubectl_server_version.test", "minor"),
					resource.TestCheckResourceAttrSet("data.kubectl_server_version.test", "patch"),
					resource.TestCheckResourceAttrSet(
						"data.kubectl_server_version.test",
						"git_version",
					),
					resource.TestCheckResourceAttrSet(
						"data.kubectl_server_version.test",
						"platform",
					),
					resource.TestMatchResourceAttr(
						"data.kubectl_server_version.test",
						"version",
						regexp.MustCompile(`^\d+\.\d+\.\d+$`),
					),
				),
			},
		},
	})
}

func testAccDataSourceKubectlServerVersion_basic() string {
	return `
data "kubectl_server_version" "test" {}
`
}
