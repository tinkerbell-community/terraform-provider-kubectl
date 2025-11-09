data "kubectl_path_documents" "docs" {
    pattern = "./manifests/*.yaml"
}

resource "kubectl_manifest" "test" {
    count     = length(data.kubectl_path_documents.docs.documents)
    yaml_body = element(data.kubectl_path_documents.docs.documents, count.index)
}
