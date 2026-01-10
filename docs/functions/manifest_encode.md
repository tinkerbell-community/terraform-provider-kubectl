---
page_title: "manifest_encode Function - kubectl"
subcategory: "Functions"
description: |-
  Encode a Terraform object to Kubernetes YAML
---

# Function: manifest_encode

Given an object representation of a Kubernetes manifest (or a list of manifests), will encode and return a YAML string for that resource.

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

# Encode a Terraform object to YAML
locals {
  manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "example"
      namespace = "default"
    }
    data = {
      key = "value"
    }
  }
}

resource "kubectl_manifest" "configmap" {
  yaml_body = provider::kubectl::manifest_encode(local.manifest)
}

# Encode multiple manifests
locals {
  manifests = [
    {
      apiVersion = "v1"
      kind       = "ConfigMap"
      metadata   = { name = "config1" }
      data       = { key = "value1" }
    },
    {
      apiVersion = "v1"
      kind       = "ConfigMap"
      metadata   = { name = "config2" }
      data       = { key = "value2" }
    }
  ]
}

# This will produce a multi-document YAML with --- separators
output "multi_yaml" {
  value = provider::kubectl::manifest_encode(local.manifests)
}

# Round-trip example: decode, modify, encode
locals {
  original  = provider::kubectl::manifest_decode(file("base.yaml"))
  
  modified = merge(local.original, {
    metadata = merge(local.original.metadata, {
      labels = {
        environment = "production"
        managed-by  = "terraform"
      }
    })
  })
}

resource "kubectl_manifest" "modified" {
  yaml_body = provider::kubectl::manifest_encode(local.modified)
}
```

## Signature

```text
manifest_encode(manifest object|list) string
```

## Arguments

1. `manifest` (Object or List) The object or list of objects representing Kubernetes manifest(s)

## Return Type

The `string` returned from `manifest_encode` contains the YAML encoded Kubernetes manifest.

- For a single object, returns a single YAML document
- For a list of objects, returns multi-document YAML with `---` separators

## Error Handling

This function will return an error if:
- The object is missing required Kubernetes fields (`apiVersion`, `kind`, `metadata`)
- The object structure cannot be marshalled to YAML

## Null Handling

- Null values in the object are preserved as `null` in the YAML output
- This matches Kubernetes API server behavior

## Use Cases

### 1. Dynamic Manifest Generation

```hcl
locals {
  namespaces = ["dev", "staging", "prod"]
  
  configmaps = [
    for ns in local.namespaces : {
      apiVersion = "v1"
      kind       = "ConfigMap"
      metadata = {
        name      = "app-config"
        namespace = ns
      }
      data = {
        environment = ns
      }
    }
  ]
}

resource "kubectl_manifest" "configs" {
  count     = length(local.configmaps)
  yaml_body = provider::kubectl::manifest_encode(local.configmaps[count.index])
}
```

### 2. Manifest Transformation

```hcl
locals {
  base_manifests = provider::kubectl::manifest_decode_multi(file("base.yaml"))
  
  # Add common labels to all resources
  labeled_manifests = [
    for m in local.base_manifests : merge(m, {
      metadata = merge(m.metadata, {
        labels = merge(
          try(m.metadata.labels, {}),
          { "managed-by" = "terraform" }
        )
      })
    })
  ]
}

resource "kubectl_manifest" "transformed" {
  count     = length(local.labeled_manifests)
  yaml_body = provider::kubectl::manifest_encode(local.labeled_manifests[count.index])
}
```

### 3. Template Interpolation

```hcl
variable "app_version" {
  type = string
}

locals {
  deployment = {
    apiVersion = "apps/v1"
    kind       = "Deployment"
    metadata = {
      name = "myapp"
    }
    spec = {
      replicas = 3
      selector = {
        matchLabels = { app = "myapp" }
      }
      template = {
        metadata = {
          labels = { 
            app     = "myapp"
            version = var.app_version
          }
        }
        spec = {
          containers = [{
            name  = "myapp"
            image = "myapp:${var.app_version}"
            ports = [{ containerPort = 8080 }]
          }]
        }
      }
    }
  }
}

resource "kubectl_manifest" "app" {
  yaml_body = provider::kubectl::manifest_encode(local.deployment)
}
```

## See Also

- [`manifest_decode`](manifest_decode.md) - Decode a single manifest to an object
- [`manifest_decode_multi`](manifest_decode_multi.md) - Decode multiple manifests
