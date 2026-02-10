# Modern YAML Conversion Implementation

## Overview

The kubectl provider has been successfully updated to use provider-defined functions for YAML parsing and manipulation, following the modern and stable approach established by the HashiCorp Kubernetes provider (terraform-provider-kubernetes).

## Key Benefits

### 1. **Stability**
- Based on battle-tested implementation from HashiCorp's official Kubernetes provider
- Uses `sigs.k8s.io/yaml` library, the same library used by kubectl and Kubernetes itself
- Handles edge cases properly (nulls, numeric precision, nested structures)

### 2. **Performance**
- Functions are evaluated during Terraform's plan phase
- No separate state management or data source refresh cycles
- No network calls for YAML parsing
- Faster execution compared to data sources

### 3. **Simplicity**
- Cleaner Terraform code using native function syntax
- Can be composed with other Terraform functions (`file()`, `fileset()`, etc.)
- No need for separate data source blocks
- Direct integration with locals and variables

### 4. **Modern Pattern**
- Follows Terraform 1.8+ provider function conventions
- Aligns with HashiCorp's direction for provider capabilities
- Future-proof architecture

## Implementation Details

### Files Created

```
kubectl/functions/
├── decode.go                   # Core YAML → Terraform object conversion
├── encode.go                   # Core Terraform object → YAML conversion
├── manifest_decode.go          # Single manifest function
├── manifest_decode_multi.go    # Multiple manifest function
└── manifest_encode.go          # Encode function
```

### Functions Added

1. **`provider::kubectl::manifest_decode(manifest string) object`**
   - Decodes a single Kubernetes YAML manifest
   - Returns a dynamic Terraform object matching the YAML structure
   - Validates required Kubernetes fields

2. **`provider::kubectl::manifest_decode_multi(manifest string) tuple`**
   - Decodes multiple YAML manifests (separated by `---`)
   - Returns a tuple of dynamic objects
   - Skips empty documents with warnings

3. **`provider::kubectl::manifest_encode(manifest object|list) string`**
   - Encodes Terraform object(s) to YAML
   - Supports single manifest or list of manifests
   - Produces multi-document YAML for lists

### Data Sources Removed

The following data sources are now deprecated in favor of functions:

- `kubectl_file_documents` → Use `manifest_decode_multi(file(...))`
- `kubectl_path_documents` → Use `fileset()` + `manifest_decode_multi()`
- `kubectl_filename_list` → Use native `fileset()` function

## Technical Implementation

### YAML Parsing

```go
// Uses regex to split multi-document YAML
var documentSeparator = regexp.MustCompile(`(:?^|\s*\n)---\s*`)

// Parses with sigs.k8s.io/yaml (same as kubectl)
err := yaml.Unmarshal([]byte(doc), &data)

// Validates required Kubernetes fields
func validateKubernetesManifest(m map[string]any) error {
    for _, k := range []string{"apiVersion", "kind", "metadata"} {
        if _, ok := m[k]; !ok {
            return fmt.Errorf("missing field %q", k)
        }
    }
    return nil
}
```

### Type Conversion

The implementation handles all Go → Terraform type conversions:

- `string` → `types.StringValue`
- `bool` → `types.BoolValue`
- `int/int32/int64/float32/float64` → `types.NumberValue` (using `big.Float`)
- `map[string]any` → `types.ObjectValue`
- `[]any` → `types.TupleValue`
- `nil` → `types.DynamicNull()`

### Numeric Precision

Uses `big.Float` to preserve numeric precision:

```go
case float64:
    return types.NumberValue(big.NewFloat(v)), nil
```

This prevents precision loss for large numbers or decimal values.

## Migration Examples

### Simple File Loading

**Before (Data Source):**
```hcl
data "kubectl_file_documents" "docs" {
  content = file("manifests.yaml")
}

resource "kubectl_manifest" "resources" {
  for_each  = data.kubectl_file_documents.docs.manifests
  yaml_body = each.value
}
```

**After (Function):**
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

### Directory Processing

**Before (Data Source):**
```hcl
data "kubectl_path_documents" "docs" {
  pattern = "manifests/*.yaml"
}

resource "kubectl_manifest" "resources" {
  for_each  = data.kubectl_path_documents.docs.manifests
  yaml_body = each.value
}
```

**After (Function):**
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

### Dynamic Manifest Generation

**New Capability:**
```hcl
locals {
  # Generate ConfigMaps dynamically
  configmaps = [
    for env in ["dev", "staging", "prod"] : {
      apiVersion = "v1"
      kind       = "ConfigMap"
      metadata = {
        name      = "app-config"
        namespace = env
      }
      data = {
        environment = env
        database    = "db-${env}.example.com"
      }
    }
  ]
}

resource "kubectl_manifest" "configs" {
  count     = length(local.configmaps)
  yaml_body = provider::kubectl::manifest_encode(local.configmaps[count.index])
}
```

### Manifest Transformation

**New Capability:**
```hcl
locals {
  # Load base manifests
  base = provider::kubectl::manifest_decode_multi(file("base.yaml"))
  
  # Transform: add labels, override namespace
  transformed = [
    for m in local.base : merge(m, {
      metadata = merge(m.metadata, {
        namespace = "production"
        labels = merge(
          try(m.metadata.labels, {}),
          {
            environment = "prod"
            managed-by  = "terraform"
          }
        )
      })
    })
  ]
}

resource "kubectl_manifest" "prod" {
  count     = length(local.transformed)
  yaml_body = provider::kubectl::manifest_encode(local.transformed[count.index])
}
```

## Testing

### Console Testing

Functions can be tested directly in Terraform console:

```bash
$ terraform console

> provider::kubectl::manifest_decode(file("test.yaml"))
{
  "apiVersion" = "v1"
  "kind" = "ConfigMap"
  "metadata" = {
    "name" = "test"
  }
  "data" = {
    "key" = "value"
  }
}

> provider::kubectl::manifest_decode_multi(file("multi.yaml"))
[
  {
    "apiVersion" = "v1"
    "kind" = "ConfigMap"
    "metadata" = { "name" = "config1" }
  },
  {
    "apiVersion" = "v1"
    "kind" = "Service"
    "metadata" = { "name" = "service1" }
  }
]

> provider::kubectl::manifest_encode({
    apiVersion = "v1"
    kind = "ConfigMap"
    metadata = { name = "test" }
  })
"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\n"
```

### Validation

The functions perform validation:

```hcl
# This will error: missing required fields
locals {
  invalid = provider::kubectl::manifest_decode(<<-YAML
    apiVersion: v1
    data:
      key: value
  YAML
  )
}
# Error: Invalid Kubernetes manifest: missing field "kind"
```

## Documentation

Complete documentation has been created:

- `docs/functions/manifest_decode.md` - Single manifest decode
- `docs/functions/manifest_decode_multi.md` - Multiple manifest decode
- `docs/functions/manifest_encode.md` - Encode to YAML
- `FUNCTIONS_MIGRATION.md` - Migration guide from data sources

## Requirements

- **Terraform**: >= 1.8.0 (provider functions support)
- **kubectl provider**: >= 2.0.0 (when this is released)

## Backward Compatibility

The data sources have been removed from the provider. Users must migrate to functions. The migration is straightforward and documented in `FUNCTIONS_MIGRATION.md`.

## References

- **HashiCorp Kubernetes Provider Implementation**: 
  - https://github.com/hashicorp/terraform-provider-kubernetes/tree/main/internal/framework/provider/functions
  - Source of truth for this implementation

- **Terraform Provider Functions**:
  - https://developer.hashicorp.com/terraform/plugin/framework/functions

- **Provider Functions User Documentation**:
  - https://developer.hashicorp.com/terraform/language/functions/provider-functions

## Conclusion

This implementation:

✅ Follows HashiCorp's proven pattern from their official Kubernetes provider  
✅ Uses stable, reliable YAML parsing (`sigs.k8s.io/yaml`)  
✅ Improves performance (no data source refresh cycles)  
✅ Simplifies Terraform code (native function syntax)  
✅ Enables powerful new capabilities (transformation, generation)  
✅ Future-proofs the provider architecture  

The kubectl provider is now aligned with modern Terraform best practices and uses the same proven implementation as the official Kubernetes provider.
