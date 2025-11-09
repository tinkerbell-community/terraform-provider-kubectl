data "kubectl_filename_list" "yaml_files" {
  pattern = "./manifests/*.yaml"
}

output "yaml_files" {
  value = {
    basenames = data.kubectl_filename_list.yaml_files.basenames
    matches   = data.kubectl_filename_list.yaml_files.matches
  }
}
