---
page_title: "manifest_decode Function - kubectl"
subcategory: "Functions"
description: |-
  Decode a Kubernetes YAML manifest into a Terraform object
---

# Function: manifest_decode

Given a YAML text containing a single Kubernetes manifest, will decode and return an object representation of that resource.

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

# Decode a single manifest from a file
locals {
  manifest = provider::kubectl::manifest_decode(file("configmap.yaml"))
}

output "configmap_name" {
  value = local.manifest.metadata.name
}

# Decode an inline manifest
locals {
  inline_manifest = provider::kubectl::manifest_decode(<<-YAML
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: example
      namespace: default
    data:
      key: value
  YAML
  )
}
```

## Signature

```text
manifest_decode(manifest string) object
```

## Arguments

1. `manifest` (String) The YAML text for a single Kubernetes manifest

## Return Type

The `object` returned from `manifest_decode` is dynamic and will mirror the structure of the YAML manifest supplied.

## Error Handling

This function will return an error if:
- The YAML is invalid
- The manifest is missing required Kubernetes fields (`apiVersion`, `kind`, `metadata`)
- Multiple resources are present in the YAML (use `manifest_decode_multi` instead)

## See Also

- [`manifest_decode_multi`](manifest_decode_multi.md) - Decode multiple manifests from a single YAML file
- [`manifest_encode`](manifest_encode.md) - Encode a Terraform object back to YAML
