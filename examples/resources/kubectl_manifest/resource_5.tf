# MutatingWebhookConfiguration with caBundle as a computed field
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "admissionregistration.k8s.io/v1"
    kind       = "MutatingWebhookConfiguration"
    metadata = {
      name = "istio-sidecar-injector"
    }
    webhooks = [{
      name = "sidecar-injector.istio.io"
      clientConfig = {
        caBundle = ""
      }
    }]
  }

  fields = {
    computed = ["webhooks.0.clientConfig.caBundle"]
  }
}
