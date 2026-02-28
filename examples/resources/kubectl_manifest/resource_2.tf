resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "Pod"
    metadata = {
      name = "nginx"
    }
    spec = {
      containers = [{
        name  = "nginx"
        image = "nginx:1.14.2"
        readinessProbe = {
          httpGet = {
            path = "/"
            port = 80
          }
          initialDelaySeconds = 10
        }
      }]
    }
  }

  wait = {
    field = [
      {
        key   = "status.containerStatuses.[0].ready"
        value = "true"
      },
      {
        key   = "status.phase"
        value = "Running"
      },
      {
        key        = "status.podIP"
        value      = "^(\\d+(\\.|$)){4}"
        value_type = "regex"
      },
    ]
    condition = [
      {
        type   = "ContainersReady"
        status = "True"
      },
      {
        type   = "Ready"
        status = "True"
      },
    ]
  }

  # Fail immediately if the pod enters a crash loop
  error_on = {
    field = [
      {
        key   = "status.containerStatuses.[0].state.waiting.reason"
        value = "CrashLoopBackOff|ErrImagePull|ImagePullBackOff"
      },
    ]
  }
}
