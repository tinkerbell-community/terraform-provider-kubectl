# ServiceAccount with computed fields for controller-managed annotations
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "ServiceAccount"
    metadata = {
      name      = "name-here"
      namespace = "default"
      annotations = {
        "this.should.be.computed" = "true"
      }
    }
  }

  computed_fields = ["metadata.annotations"]
}
