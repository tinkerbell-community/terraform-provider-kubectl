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
