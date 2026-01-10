---
page_title: "manifest_decode_multi Function - kubectl"
subcategory: "Functions"
description: |-
  Decode multiple Kubernetes YAML manifests into a tuple of Terraform objects
---

# Function: manifest_decode_multi

Given a YAML text containing multiple Kubernetes manifests (separated by `---`), will decode and return a tuple of object representations for each resource.

-> **Note** Provider-defined functions require Terraform 1.8 and later.

## Example Usage

```terraform
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

# Decode multiple manifests from a file
locals {
  manifests = provider::kubectl::manifest_decode_multi(file("all-resources.yaml"))
}

# Apply all manifests using count
resource "kubectl_manifest" "resources" {
  count     = length(local.manifests)
  yaml_body = provider::kubectl::manifest_encode(local.manifests[count.index])
}

# Create a map for for_each usage
locals {
  manifest_map = {
    for idx, manifest in local.manifests :
    "${manifest.kind}/${manifest.metadata.name}" => manifest
  }
}

resource "kubectl_manifest" "keyed_resources" {
  for_each  = local.manifest_map
  yaml_body = provider::kubectl::manifest_encode(each.value)
}

# Process multiple files from a directory
locals {
  yaml_files = fileset(path.module, "manifests/*.yaml")
  
  all_manifests = flatten([
    for file in local.yaml_files :
    provider::kubectl::manifest_decode_multi(file("${path.module}/${file}"))
  ])
}
```

## Signature

```text
manifest_decode_multi(manifest string) tuple
```

## Arguments

1. `manifest` (String) The YAML text containing one or more Kubernetes manifests, separated by `---`

## Return Type

The `tuple` returned from `manifest_decode_multi` will contain dynamic objects that mirror the structure of the resources in the YAML manifest supplied.

Each element in the tuple represents a single Kubernetes resource.

## Error Handling

This function will return an error if:
- The YAML is invalid
- Any manifest is missing required Kubernetes fields (`apiVersion`, `kind`, `metadata`)

Empty YAML documents (between `---` separators) are skipped with a warning.

## Migration from Data Sources

This function replaces the following data sources:

### From `kubectl_file_documents`

**Before:**
```hcl
data "kubectl_file_documents" "docs" {
  content = file("manifests.yaml")
}

resource "kubectl_manifest" "resources" {
  for_each  = data.kubectl_file_documents.docs.manifests
  yaml_body = each.value
}
```

**After:**
```hcl
locals {
  manifests = provider::kubectl::manifest_decode_multi(file("manifests.yaml"))
  manifest_map = {
    for idx, m in local.manifests :
    "${m.kind}/${m.metadata.name}" => m
  }
}

resource "kubectl_manifest" "resources" {
  for_each  = local.manifest_map
  yaml_body = provider::kubectl::manifest_encode(each.value)
}
```

### From `kubectl_path_documents`

**Before:**
```hcl
data "kubectl_path_documents" "docs" {
  pattern = "manifests/*.yaml"
}

resource "kubectl_manifest" "resources" {
  for_each  = data.kubectl_path_documents.docs.manifests
  yaml_body = each.value
}
```

**After:**
```hcl
locals {
  yaml_files = fileset(path.module, "manifests/*.yaml")
  manifests = flatten([
    for file in local.yaml_files :
    provider::kubectl::manifest_decode_multi(file("${path.module}/${file}"))
  ])
  manifest_map = {
    for idx, m in local.manifests :
    "${m.kind}/${m.metadata.name}" => m
  }
}

resource "kubectl_manifest" "resources" {
  for_each  = local.manifest_map
  yaml_body = provider::kubectl::manifest_encode(each.value)
}
```

## See Also

- [`manifest_decode`](manifest_decode.md) - Decode a single manifest
- [`manifest_encode`](manifest_encode.md) - Encode Terraform objects back to YAML
