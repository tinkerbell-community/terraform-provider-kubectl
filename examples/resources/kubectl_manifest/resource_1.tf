resource "kubectl_manifest" "test" {
    yaml_body = <<YAML
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: test-ingress
  annotations:
    nginx.ingress.kubernetes.io/rewrite-target: /
    azure/frontdoor: enabled
spec:
  rules:
  - http:
      paths:
      - path: /testpath
        pathType: "Prefix"
        backend:
          serviceName: test
          servicePort: 80
YAML
}
