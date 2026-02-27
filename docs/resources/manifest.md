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
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: example-config
      namespace: default
    data:
      key1: value1
      key2: value2
  YAML
}

# Service example
resource "kubectl_manifest" "service" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: example-service
      namespace: default
    spec:
      ports:
        - name: https
          port: 443
          targetPort: 8443
        - name: http
          port: 80
          targetPort: 9090
      selector:
        app: example
  YAML
}

# Deployment with server-side apply and rollout wait
resource "kubectl_manifest" "deployment" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: nginx-deployment
      namespace: default
    spec:
      replicas: 3
      selector:
        matchLabels:
          app: nginx
      template:
        metadata:
          labels:
            app: nginx
        spec:
          containers:
          - name: nginx
            image: nginx:1.21
            ports:
            - containerPort: 80
  YAML

  # Use server-side apply for better conflict resolution
  server_side_apply = true
  force_conflicts   = true

  # Wait for the deployment to be ready
  wait_for_rollout = true
}

# Basic Ingress example
resource "kubectl_manifest" "ingress_basic" {
  yaml_body = <<-YAML
    apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      name: basic-ingress
      namespace: default
      annotations:
        nginx.ingress.kubernetes.io/rewrite-target: "/"
    spec:
      ingressClassName: "nginx"
      rules:
      - host: "*.example.com"
        http:
          paths:
          - path: "/testpath"
            pathType: "Prefix"
            backend:
              service:
                name: test
                port:
                  number: 80
  YAML

  # Mark sensitive annotations
  sensitive_fields = [
    "metadata.annotations.nginx.ingress.kubernetes.io/auth-secret",
  ]
}

# Complex Ingress with multiple annotations
resource "kubectl_manifest" "ingress_complex" {
  yaml_body = <<-YAML
    apiVersion: networking.k8s.io/v1
    kind: Ingress
    metadata:
      annotations:
        nginx.ingress.kubernetes.io/affinity: cookie
        nginx.ingress.kubernetes.io/proxy-body-size: 0m
        nginx.ingress.kubernetes.io/rewrite-target: "/"
        nginx.ingress.kubernetes.io/ssl-redirect: "true"
      name: complex-ingress
      namespace: default
    spec:
      ingressClassName: "nginx"
      rules:
        - host: "app.example.com"
          http:
            paths:
              - path: "/"
                pathType: "Prefix"
                backend:
                  service:
                    name: backend-service
                    port:
                      number: 80
      tls:
        - secretName: tls-secret
          hosts:
          - app.example.com
  YAML
}

# RBAC: ServiceAccount and ClusterRoleBinding
resource "kubectl_manifest" "service_account" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ServiceAccount
    metadata:
      name: example-sa
      namespace: default
  YAML
}

resource "kubectl_manifest" "cluster_role_binding" {
  depends_on = [kubectl_manifest.service_account]

  yaml_body = <<-YAML
    apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    metadata:
      name: example-crb
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: cluster-admin
    subjects:
      - kind: ServiceAccount
        name: example-sa
        namespace: default
  YAML
}

# Custom Resource Definition (CRD)
resource "kubectl_manifest" "crd" {
  yaml_body = <<-YAML
    apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    metadata:
      name: crontabs.stable.example.com
    spec:
      group: stable.example.com
      conversion:
        strategy: None
      scope: Namespaced
      names:
        plural: crontabs
        singular: crontab
        kind: CronTab
        shortNames:
          - ct
      versions:
        - name: v1
          served: true
          storage: true
          schema:
            openAPIV3Schema:
              type: object
              properties:
                spec:
                  type: object
                  properties:
                    cronSpec:
                      type: string
                    image:
                      type: string
  YAML
}

# Custom Resource using the CRD
resource "kubectl_manifest" "custom_resource" {
  depends_on = [kubectl_manifest.crd]

  yaml_body = <<-YAML
    apiVersion: stable.example.com/v1
    kind: CronTab
    metadata:
      name: my-crontab
      namespace: default
    spec:
      cronSpec: "* * * * /5"
      image: my-awesome-cron-image
  YAML
}

# Override namespace example
resource "kubectl_manifest" "with_override" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: override-example
      namespace: original-namespace
    data:
      key: value
  YAML

  # Override the namespace from YAML
  override_namespace = "production"
}

# Ignore specific fields for drift detection
resource "kubectl_manifest" "ignore_fields_example" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: ignore-example
      namespace: default
    data:
      managed: "by-terraform"
      unmanaged: "can-change"
  YAML

  # Ignore changes to specific fields
  ignore_fields = [
    "data.unmanaged",
    "metadata.annotations",
  ]
}
```

```terraform
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
```

```terraform
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
```

```terraform
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
```

```terraform
resource "kubectl_manifest" "test" {
    yaml_body = <<YAML
apiVersion: v1
kind: ServiceAccount
metadata:
  name: name-here
  namespace: default
  annotations:
    this.should.be.ignored: "true"
YAML

    ignore_fields = ["metadata.annotations"]
}
```

```terraform
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
```

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `manifest` (Dynamic) An object representation of the Kubernetes resource manifest. Must contain `apiVersion`, `kind`, and `metadata` (with at least `name`). Additional fields like `spec`, `data`, `stringData`, etc. depend on the resource kind.

### Optional

- `apply_only` (Boolean) Apply only (never delete the resource). Default: false
- `computed_fields` (List of String) List of manifest fields whose values may be altered by the API server during apply. Defaults to: `["metadata.annotations", "metadata.labels"]`
- `delete_cascade` (String) Cascade mode for deletion: Background or Foreground. Default: Background
- `field_manager` (Block List) Configure field manager options for server-side apply. (see [below for nested schema](#nestedblock--field_manager))
- `timeouts` (Attributes) (see [below for nested schema](#nestedatt--timeouts))
- `wait` (Block List) Configure waiter options. (see [below for nested schema](#nestedblock--wait))

### Read-Only

- `id` (String) Kubernetes resource unique identifier (UID) assigned by the API server. This is a read-only value and has no impact on the plan.
- `object` (Dynamic) The full resource object as returned by the API server.
- `status` (Dynamic) Resource status as reported by the Kubernetes API server.

<a id="nestedblock--field_manager"></a>
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


<a id="nestedblock--wait"></a>
### Nested Schema for `wait`

Optional:

- `condition` (Block List) Wait for status conditions to match. (see [below for nested schema](#nestedblock--wait--condition))
- `error_on` (Block List) Fail the apply immediately if any of these conditions are detected. Use this to detect error states during waiting, such as CrashLoopBackOff or Failed status. The `key` is a JSON path (e.g., `status.containerStatuses.0.state.waiting.reason`) and `value` is a regex pattern matched against the field value. (see [below for nested schema](#nestedblock--wait--error_on))
- `field` (Block List) Wait for a resource field to reach an expected value. Multiple `field` blocks can be specified; all must match. (see [below for nested schema](#nestedblock--wait--field))
- `rollout` (Boolean) Wait for rollout to complete on resources that support `kubectl rollout status`.

<a id="nestedblock--wait--condition"></a>
### Nested Schema for `wait.condition`

Optional:

- `status` (String) The condition status.
- `type` (String) The type of condition.


<a id="nestedblock--wait--error_on"></a>
### Nested Schema for `wait.error_on`

Required:

- `key` (String) JSON path to the field to check (e.g., `status.phase`, `status.conditions.0.reason`).
- `value` (String) Regex pattern to match against the field value. If matched, the apply fails immediately.

Optional:

- `message` (String) Custom error message to display when this error condition is matched.


<a id="nestedblock--wait--field"></a>
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
