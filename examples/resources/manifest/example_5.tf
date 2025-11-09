resource "kubectl_manifest" "test" {
    yaml_body = <<YAML
apiVersion: admissionregistration.k8s.io/v1beta1
kind: MutatingWebhookConfiguration
metadata:
  name: istio-sidecar-injector
webhooks:
  - clientConfig:
      caBundle: ""
YAML

    ignore_fields = ["webhooks.0.clientConfig.caBundle"]
}
