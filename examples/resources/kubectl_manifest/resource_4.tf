resource "kubectl_manifest" "test" {
    yaml_body = <<YAML
apiVersion: v1
kind: ServiceAccount
metadata:
  name: name-here
  namespace: default
  annotations:
    this.should.be.ignored: "true"
YAML

    ignore_fields = ["metadata.annotations"]
}
