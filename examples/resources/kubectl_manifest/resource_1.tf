resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "networking.k8s.io/v1"
    kind       = "Ingress"
    metadata = {
      name = "test-ingress"
      annotations = {
        "nginx.ingress.kubernetes.io/rewrite-target" = "/"
        "azure/frontdoor"                            = "enabled"
      }
    }
    spec = {
      rules = [{
        http = {
          paths = [{
            path     = "/testpath"
            pathType = "Prefix"
            backend = {
              service = {
                name = "test"
                port = {
                  number = 80
                }
              }
            }
          }]
        }
      }]
    }
  }
}
