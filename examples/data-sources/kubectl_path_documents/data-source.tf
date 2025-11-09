# Basic usage: Load YAML files from a directory
data "kubectl_path_documents" "manifests" {
  pattern = "./manifests/*.yaml"

  vars = {
    namespace   = "production"
    replicas    = "3"
    environment = "prod"
  }

  sensitive_vars = {
    api_key = var.api_key
  }
}

# Create resources from all documents
resource "kubectl_manifest" "from_path" {
  for_each  = toset(data.kubectl_path_documents.manifests.documents)
  yaml_body = each.value
}

# Or access by manifest key
resource "kubectl_manifest" "specific" {
  yaml_body = data.kubectl_path_documents.manifests.manifests["Deployment.production.nginx"]
}

# Example with HCL templating and conditionals
# Creates namespaces with conditional labels
# YAML file content:
# %{ for namespace in split(",", namespaces) }
# ---
# apiVersion: v1
# kind: Namespace
# metadata:
#   name: ${namespace}
#   labels:
#     name: ${namespace}
# %{ if hyperscale_enabled == "true" ~}
#     hyperscale: enabled
# %{ endif ~}
# %{ endfor }

data "kubectl_path_documents" "namespaces" {
  pattern = "./manifests/namespaces.yaml"

  vars = {
    namespaces         = "dev,staging,prod"
    hyperscale_enabled = "true"
  }
}

resource "kubectl_manifest" "namespaces" {
  for_each  = toset(data.kubectl_path_documents.namespaces.documents)
  yaml_body = each.value
}

# Example with variable substitution
# YAML file content:
# apiVersion: "stable.example.com/v1"
# kind: ${the_kind}
# metadata:
#   name: my-custom-resource
# spec:
#   cronSpec: "* * * * /5"
#   image: my-awesome-cron-image

data "kubectl_path_documents" "templated" {
  pattern = "./manifests/single-templated.yaml"

  vars = {
    the_kind = "CronTab"
  }
}

resource "kubectl_manifest" "templated" {
  yaml_body = data.kubectl_path_documents.templated.documents[0]
}

# Example with HCL directives (if/else)
# YAML file content:
# kind: MyAwesomeCRD
# MyYaml: Hello, %{ if name != "" }${name}%{ else }unnamed%{ endif }!

data "kubectl_path_documents" "with_directives" {
  pattern = "./manifests/directives-templated.yaml"

  vars = {
    name = "world"
  }
}

# Disable templating to use raw YAML
data "kubectl_path_documents" "raw" {
  pattern = "./manifests/*.yaml"

  disable_template = true
}

# Example: Multi-document YAML with variable substitution
# YAML file content:
# ---
# apiVersion: "stable.example.com/v1"
# kind: CronTab
# metadata:
#   name: my-crontab
# spec:
#   cronSpec: "* * * * /5"
#   image: my-awesome-cron-image
# ---
# apiVersion: apiextensions.k8s.io/v1
# kind: CustomResourceDefinition
# metadata:
#   name: crontabs.stable.example.com
# spec:
#   group: stable.example.com
#   names:
#     kind: ${crd_kind}
#     plural: crontabs
#   versions:
#     - name: v1
#       served: true
#       storage: true

data "kubectl_path_documents" "multi_templated" {
  pattern = "./manifests/multiple-templated.yaml"

  vars = {
    crd_kind = "CronTab"
  }
}

resource "kubectl_manifest" "multi" {
  for_each  = toset(data.kubectl_path_documents.multi_templated.documents)
  yaml_body = each.value
}
