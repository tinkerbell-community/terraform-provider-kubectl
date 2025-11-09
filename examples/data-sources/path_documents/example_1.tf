data "kubectl_path_documents" "manifests-directory-yaml" {
  pattern = "./manifests/*.yaml"
}
resource "kubectl_manifest" "directory-yaml" {
  for_each  = data.kubectl_path_documents.manifests-directory-yaml.manifests
  yaml_body = each.value
}
