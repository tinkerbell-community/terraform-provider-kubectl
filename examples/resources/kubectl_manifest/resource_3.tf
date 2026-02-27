resource "kubectl_manifest" "test" {
    sensitive_fields = [
        "metadata.annotations.my-secret-annotation"
    ]

    yaml_body = <<YAML
apiVersion: admissionregistration.k8s.io/v1beta1
kind: MutatingWebhookConfiguration
metadata:
  name: istio-sidecar-injector
  annotations:
    my-secret-annotation: "this is very secret"
webhooks:
  - clientConfig:
      caBundle: ""
YAML
}
