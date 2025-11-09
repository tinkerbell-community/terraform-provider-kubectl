# Basic usage: Split multi-document YAML string
data "kubectl_file_documents" "manifests" {
  content = <<-EOF
    ---
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: config1
      namespace: default
    data:
      key: value1
    ---
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: config2
      namespace: default
    data:
      key: value2
  EOF
}

# Create resources from each document
resource "kubectl_manifest" "configs" {
  for_each  = toset(data.kubectl_file_documents.manifests.documents)
  yaml_body = each.value
}

# Example: Multi-document YAML with CRD and Custom Resource
data "kubectl_file_documents" "crd_and_resource" {
  content = <<-EOF
    ---
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
    ---
    apiVersion: "stable.example.com/v1"
    kind: CronTab
    metadata:
      name: my-crontab
      namespace: default
    spec:
      cronSpec: "* * * * /5"
      image: my-awesome-cron-image
  EOF
}

# Apply documents in order (CRD first, then custom resource)
resource "kubectl_manifest" "crd" {
  yaml_body = data.kubectl_file_documents.crd_and_resource.documents[0]
}

resource "kubectl_manifest" "custom_resource" {
  depends_on = [kubectl_manifest.crd]
  yaml_body  = data.kubectl_file_documents.crd_and_resource.documents[1]
}

# Example: Access documents by manifest key
# Keys are in format: Kind.Namespace.Name or Kind.Name for cluster-scoped
output "manifest_keys" {
  value = keys(data.kubectl_file_documents.manifests.manifests)
}

# Access specific manifest by key
resource "kubectl_manifest" "specific_config" {
  yaml_body = data.kubectl_file_documents.manifests.manifests["ConfigMap.default.config1"]
}

# Example: Loading from file() function
data "kubectl_file_documents" "from_file" {
  content = file("${path.module}/manifests.yaml")
}

# Example: Multiple namespaces
data "kubectl_file_documents" "namespaces" {
  content = <<-EOF
    ---
    apiVersion: v1
    kind: Namespace
    metadata:
      name: development
      labels:
        environment: dev
    ---
    apiVersion: v1
    kind: Namespace
    metadata:
      name: staging
      labels:
        environment: staging
    ---
    apiVersion: v1
    kind: Namespace
    metadata:
      name: production
      labels:
        environment: prod
  EOF
}

resource "kubectl_manifest" "namespaces" {
  for_each  = toset(data.kubectl_file_documents.namespaces.documents)
  yaml_body = each.value
}
