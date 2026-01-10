# Configuration using provider functions must include required_providers configuration.
terraform {
  required_providers {
    kubectl = {
      source = "alekc/kubectl"
    }
  }
  # Provider functions require Terraform 1.8 and later.
  required_version = ">= 1.8.0"
}

provider "kubectl" {}

# Example 1: Decode a single manifest
locals {
  single_manifest = provider::kubectl::manifest_decode(<<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: example-config
      namespace: default
    data:
      key1: value1
      key2: value2
  YAML
  )
}

output "manifest_name" {
  value = local.single_manifest.metadata.name
}

# Example 2: Decode multiple manifests from file
locals {
  multi_manifests = provider::kubectl::manifest_decode_multi(file("${path.module}/example-manifests.yaml"))
}

output "manifest_count" {
  value = length(local.multi_manifests)
}

# Example 3: Create resources from decoded manifests
resource "kubectl_manifest" "from_functions" {
  count     = length(local.multi_manifests)
  yaml_body = provider::kubectl::manifest_encode(local.multi_manifests[count.index])
}

# Example 4: Encode Terraform object to YAML
locals {
  terraform_object = {
    apiVersion = "v1"
    kind       = "Secret"
    metadata = {
      name      = "my-secret"
      namespace = "default"
    }
    type = "Opaque"
    data = {
      username = "YWRtaW4="         # base64 encoded
      password = "MWYyZDFlMmU2N2Rm" # base64 encoded
    }
  }
}

resource "kubectl_manifest" "secret" {
  yaml_body = provider::kubectl::manifest_encode(local.terraform_object)
}

# Example 5: Process multiple files
locals {
  yaml_files = fileset(path.module, "manifests/*.yaml")

  all_manifests = flatten([
    for file in local.yaml_files :
    provider::kubectl::manifest_decode_multi(file("${path.module}/${file}"))
  ])

  # Create a map for for_each
  manifest_map = {
    for idx, manifest in local.all_manifests :
    "${manifest.kind}-${manifest.metadata.name}" => manifest
  }
}

resource "kubectl_manifest" "from_directory" {
  for_each  = local.manifest_map
  yaml_body = provider::kubectl::manifest_encode(each.value)
}
