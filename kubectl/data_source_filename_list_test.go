package kubectl_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccDataSourceKubectlFilenameList_basic(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlFilenameList_basic(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_filename_list.test", "id"),
					resource.TestCheckResourceAttr("data.kubectl_filename_list.test", "pattern", "../_examples/manifests/*.yaml"),
					resource.TestCheckResourceAttrSet("data.kubectl_filename_list.test", "matches.#"),
				),
			},
		},
	})
}

func TestAccDataSourceKubectlFilenameList_noMatches(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlFilenameList_noMatches(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_filename_list.test", "id"),
					resource.TestCheckResourceAttr("data.kubectl_filename_list.test", "pattern", "../_examples/manifests/*.nonexistent"),
					resource.TestCheckResourceAttr("data.kubectl_filename_list.test", "matches.#", "0"),
				),
			},
		},
	})
}

func TestAccDataSourceKubectlFilenameList_recursive(t *testing.T) {
	t.Parallel()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataSourceKubectlFilenameList_recursive(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.kubectl_filename_list.test", "id"),
					resource.TestCheckResourceAttr("data.kubectl_filename_list.test", "pattern", "../_examples/**/*.yaml"),
					resource.TestCheckResourceAttrSet("data.kubectl_filename_list.test", "matches.#"),
					resource.TestMatchResourceAttr(
						"data.kubectl_filename_list.test",
						"matches.0",
						regexp.MustCompile(`\.yaml$`),
					),
				),
			},
		},
	})
}

func testAccDataSourceKubectlFilenameList_basic() string {
	return `
data "kubectl_filename_list" "test" {
  pattern = "../_examples/manifests/*.yaml"
}
`
}

func testAccDataSourceKubectlFilenameList_noMatches() string {
	return `
data "kubectl_filename_list" "test" {
  pattern = "../_examples/manifests/*.nonexistent"
}
`
}

func testAccDataSourceKubectlFilenameList_recursive() string {
	return `
data "kubectl_filename_list" "test" {
  pattern = "../_examples/**/*.yaml"
}
`
}
