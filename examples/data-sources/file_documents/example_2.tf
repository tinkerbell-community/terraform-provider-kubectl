data "kubectl_file_documents" "docs" {
    content = file("multi-doc-manifest.yaml")
}

resource "kubectl_manifest" "test" {
    count     = length(data.kubectl_file_documents.docs.documents)
    yaml_body = element(data.kubectl_file_documents.docs.documents, count.index)
}
