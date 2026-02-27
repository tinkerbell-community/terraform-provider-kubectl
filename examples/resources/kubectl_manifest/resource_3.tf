# MutatingWebhookConfiguration with computed fields
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "admissionregistration.k8s.io/v1"
    kind       = "MutatingWebhookConfiguration"
    metadata = {
      name = "istio-sidecar-injector"
      annotations = {
        "my-annotation" = "some-value"
      }
    }
    webhooks = [{
      name = "sidecar-injector.istio.io"
      clientConfig = {
        caBundle = ""
      }
    }]
  }

  # Fields that may be modified by external controllers
  computed_fields = [
    "metadata.annotations",
    "webhooks.0.clientConfig.caBundle",
  ]
}
