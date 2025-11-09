resource "kubectl_manifest" "test" {
  wait_for {
    field {
      key = "status.containerStatuses.[0].ready"
      value = "true"
    }
    field {
      key = "status.phase"
      value = "Running"
    }
    field {
      key = "status.podIP"
      value = "^(\\d+(\\.|$)){4}"
      value_type = "regex"
    }
    condition {
      type = "ContainersReady"
      status = "True"
    }
    condition {
      type = "Ready"
      status = "True"
    }
  }
  yaml_body = <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: nginx
spec:
  containers:
  - name: nginx
    image: nginx:1.14.2
    readinessProbe:
      httpGet:
        path: "/"
        port: 80
      initialDelaySeconds: 10
YAML
}
