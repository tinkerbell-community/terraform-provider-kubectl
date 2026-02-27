# Advanced kubectl_manifest examples demonstrating various features

# Example 1: Wait for rollout and specific field values
resource "kubectl_manifest" "statefulset_with_wait" {
  manifest = {
    apiVersion = "apps/v1"
    kind       = "StatefulSet"
    metadata = {
      name      = "web"
      namespace = "default"
    }
    spec = {
      serviceName = "nginx"
      replicas    = 3
      selector = {
        matchLabels = {
          app = "nginx"
        }
      }
      template = {
        metadata = {
          labels = {
            app = "nginx"
          }
        }
        spec = {
          containers = [{
            name  = "nginx"
            image = "nginx:1.21"
            ports = [{
              containerPort = 80
              name          = "web"
            }]
          }]
        }
      }
    }
  }

  wait {
    rollout = true

    field {
      key   = "status.readyReplicas"
      value = "3"
    }
  }
}

# Example 2: Server-side apply with field manager
resource "kubectl_manifest" "with_field_manager" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "shared-config"
      namespace = "default"
    }
    data = {
      managed_by_terraform = "true"
      key                  = "value"
    }
  }

  field_manager {
    name            = "Terraform"
    force_conflicts = true
  }
}

# Example 3: Wait for Job completion with error detection
resource "kubectl_manifest" "job_with_error_detection" {
  manifest = {
    apiVersion = "batch/v1"
    kind       = "Job"
    metadata = {
      name      = "batch-job"
      namespace = "default"
    }
    spec = {
      template = {
        spec = {
          containers = [{
            name    = "job"
            image   = "busybox"
            command = ["sh", "-c", "echo Processing && sleep 30 && echo Done"]
          }]
          restartPolicy = "Never"
        }
      }
      backoffLimit = 4
    }
  }

  # Wait for job completion
  wait {
    condition {
      type   = "Complete"
      status = "True"
    }
  }

  # Fail immediately if the job reports a failure condition
  error_on {
    condition {
      type   = "Failed"
      status = "True"
    }
  }
}

# Example 4: DaemonSet with rollout wait
resource "kubectl_manifest" "daemonset" {
  manifest = {
    apiVersion = "apps/v1"
    kind       = "DaemonSet"
    metadata = {
      name      = "monitoring-agent"
      namespace = "default"
    }
    spec = {
      selector = {
        matchLabels = {
          app = "monitoring"
        }
      }
      template = {
        metadata = {
          labels = {
            app = "monitoring"
          }
        }
        spec = {
          containers = [{
            name  = "agent"
            image = "monitoring-agent:latest"
          }]
        }
      }
    }
  }

  wait {
    rollout = true
  }
}

# Example 5: Computed fields and apply-only
resource "kubectl_manifest" "apply_only_config" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "ephemeral-config"
      namespace = "default"
    }
    data = {
      key = "value"
    }
  }

  # Resource is applied but never deleted by Terraform
  apply_only = true

  # Fields that may be modified by controllers
  computed_fields = [
    "metadata.annotations",
    "metadata.labels",
  ]
}

# Example 6: PVC with foreground cascade deletion
resource "kubectl_manifest" "pvc" {
  manifest = {
    apiVersion = "v1"
    kind       = "PersistentVolumeClaim"
    metadata = {
      name      = "data-pvc"
      namespace = "default"
    }
    spec = {
      accessModes = ["ReadWriteOnce"]
      resources = {
        requests = {
          storage = "1Gi"
        }
      }
    }
  }

  delete_cascade = "Foreground"
}

# Example 7: Complex application stack
resource "kubectl_manifest" "app_namespace" {
  manifest = {
    apiVersion = "v1"
    kind       = "Namespace"
    metadata = {
      name = "myapp"
      labels = {
        name       = "myapp"
        managed-by = "terraform"
      }
    }
  }
}

resource "kubectl_manifest" "app_config" {
  depends_on = [kubectl_manifest.app_namespace]

  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "app-config"
      namespace = "myapp"
    }
    data = {
      database_url = "postgres://db:5432/myapp"
      redis_url    = "redis://redis:6379"
    }
  }
}

resource "kubectl_manifest" "app_deployment" {
  depends_on = [kubectl_manifest.app_config]

  manifest = {
    apiVersion = "apps/v1"
    kind       = "Deployment"
    metadata = {
      name      = "myapp"
      namespace = "myapp"
    }
    spec = {
      replicas = 3
      selector = {
        matchLabels = {
          app = "myapp"
        }
      }
      template = {
        metadata = {
          labels = {
            app = "myapp"
          }
        }
        spec = {
          containers = [{
            name  = "app"
            image = "myapp:latest"
            envFrom = [
              { configMapRef = { name = "app-config" } },
            ]
            ports = [{
              containerPort = 8080
            }]
          }]
        }
      }
    }
  }

  field_manager {
    name            = "Terraform"
    force_conflicts = true
  }

  wait {
    rollout = true
  }
}

resource "kubectl_manifest" "app_service" {
  depends_on = [kubectl_manifest.app_deployment]

  manifest = {
    apiVersion = "v1"
    kind       = "Service"
    metadata = {
      name      = "myapp"
      namespace = "myapp"
    }
    spec = {
      type = "LoadBalancer"
      selector = {
        app = "myapp"
      }
      ports = [{
        protocol   = "TCP"
        port       = 80
        targetPort = 8080
      }]
    }
  }
}
