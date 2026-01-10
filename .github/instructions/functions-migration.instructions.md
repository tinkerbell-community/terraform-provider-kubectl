---
applyTo: '**'
---
# Provider-Defined Functions Migration

## Summary

The kubectl provider has been updated to use provider-defined functions instead of data sources for YAML parsing and manipulation, following the modern pattern established by the HashiCorp Kubernetes provider.

## Changes Made

### Removed Data Sources

The following data sources have been removed and replaced with functions:

- `kubectl_file_documents` → `provider::kubectl::manifest_decode_multi()`
- `kubectl_path_documents` → `provider::kubectl::manifest_decode_multi()` with `file()` or `fileset()`
- `kubectl_filename_list` → Use `fileset()` directly

### New Provider Functions

Three new provider-defined functions have been added:

1. **`manifest_decode`** - Decode a single Kubernetes YAML manifest
2. **`manifest_decode_multi`** - Decode multiple Kubernetes YAML manifests
3. **`manifest_encode`** - Encode an object to Kubernetes YAML

## Migration Guide

### From `kubectl_file_documents` to `manifest_decode_multi`

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
}

resource "kubectl_manifest" "resources" {
  count     = length(local.manifests)
  yaml_body = provider::kubectl::manifest_encode(local.manifests[count.index])
}
```

### From `kubectl_path_documents` to `manifest_decode_multi`

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
  manifest_files = fileset(path.module, "manifests/*.yaml")
  manifests = flatten([
    for file in local.manifest_files :
    provider::kubectl::manifest_decode_multi(file("${path.module}/${file}"))
  ])
}

resource "kubectl_manifest" "resources" {
  count     = length(local.manifests)
  yaml_body = provider::kubectl::manifest_encode(local.manifests[count.index])
}
```

### From `kubectl_filename_list` to `fileset`

**Before:**
```hcl
data "kubectl_filename_list" "manifests" {
  pattern = "manifests/*.yaml"
}

locals {
  manifest_files = data.kubectl_filename_list.manifests.matches
}
```

**After:**
```hcl
locals {
  manifest_files = fileset(path.module, "manifests/*.yaml")
}
```

## Benefits

1. **Simpler** - Functions are evaluated during plan phase, no separate data source refresh
2. **More Stable** - Based on battle-tested HashiCorp Kubernetes provider implementation
3. **Better Performance** - No network calls or state management for parsing
4. **More Flexible** - Can be composed with other Terraform functions
5. **Modern Pattern** - Follows Terraform 1.8+ provider function conventions

## Requirements

- Terraform >= 1.8.0 (for provider-defined functions)
- kubectl provider >= 2.0.0

## Examples

### Simple Single Manifest Decode

```hcl
locals {
  configmap = provider::kubectl::manifest_decode(file("configmap.yaml"))
}

output "configmap_name" {
  value = local.configmap.metadata.name
}
```

### Multiple Manifests with For-Each

```hcl
locals {
  manifests = provider::kubectl::manifest_decode_multi(file("all-resources.yaml"))
  # Create a map keyed by kind/name for for_each
  manifest_map = {
    for idx, manifest in local.manifests :
    "${manifest.kind}/${manifest.metadata.name}" => manifest
  }
}

resource "kubectl_manifest" "resources" {
  for_each  = local.manifest_map
  yaml_body = provider::kubectl::manifest_encode(each.value)
}
```

### Encode Terraform Objects to YAML

```hcl
locals {
  configmap = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "my-config"
      namespace = "default"
    }
    data = {
      key = "value"
    }
  }
}

resource "kubectl_manifest" "configmap" {
  yaml_body = provider::kubectl::manifest_encode(local.configmap)
}
```

### Dynamic Manifest Processing

```hcl
locals {
  # Load and decode
  manifests = provider::kubectl::manifest_decode_multi(file("base.yaml"))
  
  # Transform - add label to all resources
  labeled_manifests = [
    for manifest in local.manifests : merge(manifest, {
      metadata = merge(manifest.metadata, {
        labels = merge(
          try(manifest.metadata.labels, {}),
          { "managed-by" = "terraform" }
        )
      })
    })
  ]
}

resource "kubectl_manifest" "resources" {
  count     = length(local.labeled_manifests)
  yaml_body = provider::kubectl::manifest_encode(local.labeled_manifests[count.index])
}
```

## Implementation Details

The function implementation is based directly on the HashiCorp Kubernetes provider's functions:

- Uses `sigs.k8s.io/yaml` for reliable YAML parsing
- Handles multiple document separators (`---`)
- Validates Kubernetes manifest structure (apiVersion, kind, metadata)
- Preserves numeric precision with `big.Float`
- Handles null values correctly
- Supports nested objects and arrays

## Testing

Functions can be tested using `terraform console`:

```bash
$ terraform console
> provider::kubectl::manifest_decode_multi(file("test.yaml"))
[
  {
    "apiVersion" = "v1"
    "kind" = "ConfigMap"
    "metadata" = {
      "name" = "test"
    }
  }
]
```
