---
page_title: Manifest (Resource)
subcategory: ""
description: |-
  Deploy and manage any Kubernetes resource using YAML manifests. Handles the full lifecycle including creation, updates with drift detection, and deletion.
---

# Manifest (Resource)

Deploy and manage any Kubernetes resource using YAML manifests. Handles the full lifecycle including creation, updates with drift detection, and deletion.

## Example Usage

```terraform
# Basic ConfigMap example
resource "kubectl_manifest" "config" {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "example-config"
      namespace = "default"
    }
    data = {
      key1 = "value1"
      key2 = "value2"
    }
  }
}

# Service example
resource "kubectl_manifest" "service" {
  manifest = {
    apiVersion = "v1"
    kind       = "Service"
    metadata = {
      name      = "example-service"
      namespace = "default"
    }
    spec = {
      ports = [
        {
          name       = "https"
          port       = 443
          targetPort = 8443
        },
        {
          name       = "http"
          port       = 80
          targetPort = 9090
        },
      ]
      selector = {
        app = "example"
      }
    }
  }
}

# Deployment with field manager and rollout wait
resource "kubectl_manifest" "deployment" {
  manifest = {
    apiVersion = "apps/v1"
    kind       = "Deployment"
    metadata = {
      name      = "nginx-deployment"
      namespace = "default"
    }
    spec = {
      replicas = 3
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
            }]
          }]
        }
      }
    }
  }

  # Use server-side apply with field manager for better conflict resolution
  field_manager = {
    name            = "Terraform"
    force_conflicts = true
  }

  # Wait for the deployment to be ready
  wait = {
    rollout = true
  }
}

# Ingress example
resource "kubectl_manifest" "ingress_basic" {
  manifest = {
    apiVersion = "networking.k8s.io/v1"
    kind       = "Ingress"
    metadata = {
      name      = "basic-ingress"
      namespace = "default"
      annotations = {
        "nginx.ingress.kubernetes.io/rewrite-target" = "/"
      }
    }
    spec = {
      ingressClassName = "nginx"
      rules = [{
        host = "*.example.com"
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

# Ingress with TLS
resource "kubectl_manifest" "ingress_complex" {
  manifest = {
    apiVersion = "networking.k8s.io/v1"
    kind       = "Ingress"
    metadata = {
      name      = "complex-ingress"
      namespace = "default"
      annotations = {
        "nginx.ingress.kubernetes.io/affinity"        = "cookie"
        "nginx.ingress.kubernetes.io/proxy-body-size" = "0m"
        "nginx.ingress.kubernetes.io/rewrite-target"  = "/"
        "nginx.ingress.kubernetes.io/ssl-redirect"    = "true"
      }
    }
    spec = {
      ingressClassName = "nginx"
      rules = [{
        host = "app.example.com"
        http = {
          paths = [{
            path     = "/"
            pathType = "Prefix"
            backend = {
              service = {
                name = "backend-service"
                port = {
                  number = 80
                }
              }
            }
          }]
        }
      }]
      tls = [{
        secretName = "tls-secret"
        hosts      = ["app.example.com"]
      }]
    }
  }
}

# RBAC: ServiceAccount and ClusterRoleBinding
resource "kubectl_manifest" "service_account" {
  manifest = {
    apiVersion = "v1"
    kind       = "ServiceAccount"
    metadata = {
      name      = "example-sa"
      namespace = "default"
    }
  }
}

resource "kubectl_manifest" "cluster_role_binding" {
  depends_on = [kubectl_manifest.service_account]

  manifest = {
    apiVersion = "rbac.authorization.k8s.io/v1"
    kind       = "ClusterRoleBinding"
    metadata = {
      name = "example-crb"
    }
    roleRef = {
      apiGroup = "rbac.authorization.k8s.io"
      kind     = "ClusterRole"
      name     = "cluster-admin"
    }
    subjects = [{
      kind      = "ServiceAccount"
      name      = "example-sa"
      namespace = "default"
    }]
  }
}

# Custom Resource Definition (CRD)
resource "kubectl_manifest" "crd" {
  manifest = {
    apiVersion = "apiextensions.k8s.io/v1"
    kind       = "CustomResourceDefinition"
    metadata = {
      name = "crontabs.stable.example.com"
    }
    spec = {
      group = "stable.example.com"
      conversion = {
        strategy = "None"
      }
      scope = "Namespaced"
      names = {
        plural     = "crontabs"
        singular   = "crontab"
        kind       = "CronTab"
        shortNames = ["ct"]
      }
      versions = [{
        name    = "v1"
        served  = true
        storage = true
        schema = {
          openAPIV3Schema = {
            type = "object"
            properties = {
              spec = {
                type = "object"
                properties = {
                  cronSpec = { type = "string" }
                  image    = { type = "string" }
                }
              }
            }
          }
        }
      }]
    }
  }
}

# Custom Resource using the CRD
resource "kubectl_manifest" "custom_resource" {
  depends_on = [kubectl_manifest.crd]

  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "CronTab"
    metadata = {
      name      = "my-crontab"
      namespace = "default"
    }
    spec = {
      cronSpec = "* * * * /5"
      image    = "my-awesome-cron-image"
    }
  }
}
```

```terraform
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
```

```terraform
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
```

```terraform
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
```

```terraform
# ServiceAccount with computed fields for controller-managed annotations
resource "kubectl_manifest" "test" {
  manifest = {
    apiVersion = "v1"
    kind       = "ServiceAccount"
    metadata = {
      name      = "name-here"
      namespace = "default"
      annotations = {
        "this.should.be.computed" = "true"
      }
    }
  }

  computed_fields = ["metadata.annotations"]
}
```

```terraform
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

  computed_fields = ["webhooks.0.clientConfig.caBundle"]
}
```

```terraform
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

  wait = {
    rollout = true

    field = [
      {
        key   = "status.readyReplicas"
        value = "3"
      },
    ]
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

  field_manager = {
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
  wait = {
    condition = [
      {
        type   = "Complete"
        status = "True"
      },
    ]
  }

  # Fail immediately if the job reports a failure condition
  error_on = {
    condition = [
      {
        type   = "Failed"
        status = "True"
      },
    ]
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

  wait = {
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

  field_manager = {
    name            = "Terraform"
    force_conflicts = true
  }

  wait = {
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
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `manifest` (Dynamic) An object representation of the Kubernetes resource manifest. Must contain `apiVersion`, `kind`, and `metadata` (with at least `name`). Additional fields like `spec`, `data`, `stringData`, etc. depend on the resource kind.

### Optional

- `apply_only` (Boolean) Apply only (never delete the resource). Default: false
- `computed_fields` (List of String) List of manifest fields whose values may be altered by the API server during apply. Defaults to: `["metadata.annotations", "metadata.labels"]`
- `delete_cascade` (String) Cascade mode for deletion: Background or Foreground. Default: Background
- `error_on` (Attributes) Define error conditions that are checked continuously while waiting for success conditions. If any error condition matches, the apply fails immediately. Use this to detect error states such as CrashLoopBackOff or Failed status. (see [below for nested schema](#nestedatt--error_on))
- `field_manager` (Attributes) Configure field manager options for server-side apply. (see [below for nested schema](#nestedatt--field_manager))
- `immutable_fields` (List of String) List of manifest field paths that are immutable after creation. If any of these fields change, the resource will be replaced (destroyed and re-created). Uses dot-separated paths (e.g., `spec.selector`).
- `timeouts` (Attributes) (see [below for nested schema](#nestedatt--timeouts))
- `wait` (Attributes) Configure waiter options. The apply will block until success conditions are met or the timeout is reached. (see [below for nested schema](#nestedatt--wait))

### Read-Only

- `id` (String) Kubernetes resource unique identifier (UID) assigned by the API server. This is a read-only value and has no impact on the plan.
- `object` (Dynamic) The full resource object as returned by the API server.
- `status` (Dynamic) Resource status as reported by the Kubernetes API server.

<a id="nestedatt--error_on"></a>
### Nested Schema for `error_on`

Optional:

- `condition` (Attributes List) Fail if a status condition matches. Any match triggers failure. (see [below for nested schema](#nestedatt--error_on--condition))
- `field` (Attributes List) Fail if a resource field matches an error pattern. Multiple entries can be specified; any match triggers failure. (see [below for nested schema](#nestedatt--error_on--field))

<a id="nestedatt--error_on--condition"></a>
### Nested Schema for `error_on.condition`

Optional:

- `status` (String) The condition status to match.
- `type` (String) The type of condition to check.


<a id="nestedatt--error_on--field"></a>
### Nested Schema for `error_on.field`

Required:

- `key` (String) JSON path to the field to check (e.g., `status.containerStatuses.0.state.waiting.reason`).
- `value` (String) Regex pattern to match against the field value. If matched, the apply fails immediately.

Optional:

- `value_type` (String) Comparison type: `eq` for exact match or `regex` for regular expression matching (default).



<a id="nestedatt--field_manager"></a>
### Nested Schema for `field_manager`

Optional:

- `force_conflicts` (Boolean) Force changes against conflicts. Default: false
- `name` (String) The name to use for the field manager when applying server-side. Default: Terraform


<a id="nestedatt--timeouts"></a>
### Nested Schema for `timeouts`

Optional:

- `create` (String) A string that can be [parsed as a duration](https://pkg.go.dev/time#ParseDuration) consisting of numbers and unit suffixes, such as "30s" or "2h45m". Valid time units are "s" (seconds), "m" (minutes), "h" (hours).
- `delete` (String) A string that can be [parsed as a duration](https://pkg.go.dev/time#ParseDuration) consisting of numbers and unit suffixes, such as "30s" or "2h45m". Valid time units are "s" (seconds), "m" (minutes), "h" (hours). Setting a timeout for a Delete operation is only applicable if changes are saved into state before the destroy operation occurs.
- `update` (String) A string that can be [parsed as a duration](https://pkg.go.dev/time#ParseDuration) consisting of numbers and unit suffixes, such as "30s" or "2h45m". Valid time units are "s" (seconds), "m" (minutes), "h" (hours).


<a id="nestedatt--wait"></a>
### Nested Schema for `wait`

Optional:

- `condition` (Attributes List) Wait for status conditions to match. (see [below for nested schema](#nestedatt--wait--condition))
- `field` (Attributes List) Wait for a resource field to reach an expected value. Multiple entries can be specified; all must match. (see [below for nested schema](#nestedatt--wait--field))
- `rollout` (Boolean) Wait for rollout to complete on resources that support `kubectl rollout status`.

<a id="nestedatt--wait--condition"></a>
### Nested Schema for `wait.condition`

Optional:

- `status` (String) The condition status.
- `type` (String) The type of condition.


<a id="nestedatt--wait--field"></a>
### Nested Schema for `wait.field`

Required:

- `key` (String) JSON path to the field to check (e.g., `status.phase`, `status.podIP`).
- `value` (String) The expected value or regex pattern to match.

Optional:

- `value_type` (String) Comparison type: `eq` for exact match (default) or `regex` for regular expression matching.

## Import

Import is supported using the following syntax:

```shell
# kubectl_manifest can be imported using the format: apiVersion//kind//name//namespace
# For cluster-scoped resources, omit the namespace

# Import a namespaced ConfigMap
terraform import kubectl_manifest.example "v1//ConfigMap//example-config//default"

# Import a cluster-scoped resource (ClusterRole)
terraform import kubectl_manifest.cluster_role "rbac.authorization.k8s.io/v1//ClusterRole//cluster-admin"

# Import a Deployment
terraform import kubectl_manifest.deployment "apps/v1//Deployment//nginx-deployment//default"
```
