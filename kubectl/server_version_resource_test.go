package kubectl_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccResourceKubectlServerVersion_basic(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlServerVersion_basic(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_server_version.test", "id"),
					resource.TestCheckResourceAttrSet("kubectl_server_version.test", "version"),
					resource.TestCheckResourceAttrSet("kubectl_server_version.test", "major"),
					resource.TestCheckResourceAttrSet("kubectl_server_version.test", "minor"),
					resource.TestCheckResourceAttrSet("kubectl_server_version.test", "patch"),
					resource.TestCheckResourceAttrSet("kubectl_server_version.test", "git_version"),
					resource.TestMatchResourceAttr(
						"kubectl_server_version.test",
						"version",
						regexp.MustCompile(`^\d+\.\d+\.\d+$`),
					),
				),
			},
		},
	})
}

func TestAccResourceKubectlServerVersion_triggers(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccResourceKubectlServerVersion_triggers("trigger1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_server_version.test", "id"),
					resource.TestCheckResourceAttr(
						"kubectl_server_version.test",
						"triggers.%",
						"1",
					),
					resource.TestCheckResourceAttr(
						"kubectl_server_version.test",
						"triggers.key1",
						"trigger1",
					),
				),
			},
			{
				Config: testAccResourceKubectlServerVersion_triggers("trigger2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("kubectl_server_version.test", "id"),
					resource.TestCheckResourceAttr(
						"kubectl_server_version.test",
						"triggers.%",
						"1",
					),
					resource.TestCheckResourceAttr(
						"kubectl_server_version.test",
						"triggers.key1",
						"trigger2",
					),
				),
			},
		},
	})
}

func testAccResourceKubectlServerVersion_basic() string {
	return `
resource "kubectl_server_version" "test" {}
`
}

func testAccResourceKubectlServerVersion_triggers(triggerValue string) string {
	return `
resource "kubectl_server_version" "test" {
  triggers = {
    key1 = "` + triggerValue + `"
  }
}
`
}
