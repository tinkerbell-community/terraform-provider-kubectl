# Advanced kubectl_manifest examples demonstrating various features

# Example 1: Wait for conditions with custom polling
resource "kubectl_manifest" "statefulset_with_wait" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: StatefulSet
    metadata:
      name: web
      namespace: default
    spec:
      serviceName: "nginx"
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
              name: web
  YAML

  # Wait for the StatefulSet to be ready
  wait_for_rollout = true

  # Also wait for specific conditions
  wait_for {
    field = "status.readyReplicas"
    value = "3"
  }
}

# Example 2: Server-side apply with field manager
resource "kubectl_manifest" "with_field_manager" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: shared-config
      namespace: default
    data:
      managed_by_terraform: "true"
      key: value
  YAML

  server_side_apply = true
  force_conflicts   = true
}

# Example 3: Sensitive fields obfuscation
resource "kubectl_manifest" "with_secrets" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Secret
    metadata:
      name: app-secrets
      namespace: default
    type: Opaque
    stringData:
      api_key: "super-secret-key"
      db_password: "another-secret"
  YAML

  sensitive_fields = [
    "data.api_key",
    "data.db_password",
  ]
}

# Example 4: Force recreation on changes
resource "kubectl_manifest" "force_new_example" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
      name: data-pvc
      namespace: default
    spec:
      accessModes:
        - ReadWriteOnce
      resources:
        requests:
          storage: 1Gi
  YAML

  # Force recreation if the resource needs to change
  force_new = true
}

# Example 5: Ignoring specific fields for drift detection
resource "kubectl_manifest" "ignore_managed_fields" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: loadbalancer-service
      namespace: default
      annotations:
        terraform-managed: "true"
    spec:
      type: LoadBalancer
      selector:
        app: myapp
      ports:
      - protocol: TCP
        port: 80
        targetPort: 8080
  YAML

  # Ignore fields that are set by controllers
  ignore_fields = [
    "metadata.annotations.kubectl.kubernetes.io/last-applied-configuration",
    "status",
    "spec.clusterIP",
    "spec.clusterIPs",
  ]
}

# Example 6: Wait for custom conditions
resource "kubectl_manifest" "job_with_completion_wait" {
  yaml_body = <<-YAML
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: batch-job
      namespace: default
    spec:
      template:
        spec:
          containers:
          - name: job
            image: busybox
            command: ["sh", "-c", "echo Processing && sleep 30 && echo Done"]
          restartPolicy: Never
      backoffLimit: 4
  YAML

  # Wait for job completion
  wait_for {
    field = "status.conditions[?(@.type==\"Complete\")].status"
    value = "True"
  }
}

# Example 7: DaemonSet with rollout wait
resource "kubectl_manifest" "daemonset" {
  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: DaemonSet
    metadata:
      name: monitoring-agent
      namespace: default
    spec:
      selector:
        matchLabels:
          app: monitoring
      template:
        metadata:
          labels:
            app: monitoring
        spec:
          containers:
          - name: agent
            image: monitoring-agent:latest
  YAML

  wait_for_rollout = true
}

# Example 8: Namespace override for multi-environment
resource "kubectl_manifest" "multi_env_config" {
  for_each = toset(["dev", "staging", "prod"])

  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: app-config
      namespace: default
    data:
      environment: ${each.key}
  YAML

  override_namespace = each.key
}

# Example 9: Validate schema before apply
resource "kubectl_manifest" "with_validation" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: validated-service
      namespace: default
    spec:
      selector:
        app: myapp
      ports:
      - protocol: TCP
        port: 80
        targetPort: 8080
  YAML

  validate_schema = true
}

# Example 10: Complex application stack
resource "kubectl_manifest" "app_namespace" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: Namespace
    metadata:
      name: myapp
      labels:
        name: myapp
        managed-by: terraform
  YAML
}

resource "kubectl_manifest" "app_config" {
  depends_on = [kubectl_manifest.app_namespace]

  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: app-config
      namespace: myapp
    data:
      database_url: "postgres://db:5432/myapp"
      redis_url: "redis://redis:6379"
  YAML
}

resource "kubectl_manifest" "app_secret" {
  depends_on = [kubectl_manifest.app_namespace]

  yaml_body = <<-YAML
    apiVersion: v1
    kind: Secret
    metadata:
      name: app-secrets
      namespace: myapp
    type: Opaque
    stringData:
      db_password: "${var.db_password}"
      api_key: "${var.api_key}"
  YAML

  sensitive_fields = [
    "data.db_password",
    "data.api_key",
  ]
}

resource "kubectl_manifest" "app_deployment" {
  depends_on = [
    kubectl_manifest.app_config,
    kubectl_manifest.app_secret,
  ]

  yaml_body = <<-YAML
    apiVersion: apps/v1
    kind: Deployment
    metadata:
      name: myapp
      namespace: myapp
    spec:
      replicas: 3
      selector:
        matchLabels:
          app: myapp
      template:
        metadata:
          labels:
            app: myapp
        spec:
          containers:
          - name: app
            image: myapp:latest
            envFrom:
            - configMapRef:
                name: app-config
            - secretRef:
                name: app-secrets
            ports:
            - containerPort: 8080
  YAML

  server_side_apply = true
  wait_for_rollout  = true
}

resource "kubectl_manifest" "app_service" {
  depends_on = [kubectl_manifest.app_deployment]

  yaml_body = <<-YAML
    apiVersion: v1
    kind: Service
    metadata:
      name: myapp
      namespace: myapp
    spec:
      type: LoadBalancer
      selector:
        app: myapp
      ports:
      - protocol: TCP
        port: 80
        targetPort: 8080
  YAML
}

variable "db_password" {
  description = "Database password"
  type        = string
  sensitive   = true
}

variable "api_key" {
  description = "API key for external service"
  type        = string
  sensitive   = true
}
