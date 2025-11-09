# Terraform Plugin Framework Migration - Getting Started

## Quick Start Instructions

**Agent Task**: Migrate the [terraform-provider-unifi](https://github.com/ubiquiti-community/terraform-provider-unifi) from SDK v2 to Plugin Framework

**Target Repository**: https://github.com/ubiquiti-community/terraform-provider-unifi  
**Reference Implementation**: This repository (terraform-provider-ironic) - complete migration example

### 1. Initial Setup
- **Clone and branch** from `main` branch of terraform-provider-unifi
- **Reference the mux branch**: https://github.com/ubiquiti-community/terraform-provider-unifi/tree/terraform-provider-mux
- Follow detailed instructions in `.github/instructions/` directory:
  - [`terraform-plugin-framework-guide.md`](terraform-plugin-framework-guide.md) - Comprehensive development patterns
  - [`terraform-provider-migration-guide.md`](terraform-provider-migration-guide.md) - Step-by-step migration process
  - [Official Migration Guide](https://developer.hashicorp.com/terraform/plugin/framework/migrating) - HashiCorp documentation

### 2. Critical Migration Steps (In Order)

#### Phase 1: Project Structure
1. **Create new directory**: `{provider_name}/` (NOT `internal/`)
2. **Update dependencies** in `go.mod`:
   ```go
   github.com/hashicorp/terraform-plugin-framework v1.4.2
   github.com/hashicorp/terraform-plugin-framework-validators v0.12.0
   ```
3. **Update `main.go`** - Choose framework-only OR muxed approach

#### Phase 2: Provider Foundation
1. **Create `{provider_name}/provider.go`** with framework interfaces
2. **Implement required methods**: Schema, Configure, Resources, DataSources, Metadata
3. **Add interface compliance checks**:
   ```go
   var (
       _ provider.Provider = &myProvider{}
   )
   ```

#### Phase 3: Resource Migration
1. **Start with simplest resources first**
2. **Create resource models** with `tfsdk` tags
3. **Implement CRUD operations** with proper error handling
4. **Add interface compliance**:
   ```go
   var (
       _ resource.Resource                = &myResource{}
       _ resource.ResourceWithImportState = &myResource{}
   )
   ```

### 3. Most Critical Success Factors

#### ⚠️ State Management (Prevents Import Issues)
```go
// ✅ CRITICAL: Handle empty strings as null
if apiResponse.Field == "" {
    model.Field = types.StringNull()
} else {
    model.Field = types.StringValue(apiResponse.Field)
}

// ✅ CRITICAL: Handle empty lists as null
if len(apiResponse.Items) > 0 {
    // populate list
} else {
    model.Items = types.ListNull(types.StringType)
}
```

#### ⚠️ Schema Definition Patterns
```go
// Required user input
"field": schema.StringAttribute{
    Required: true,
}

// API-computed values
"computed_field": schema.StringAttribute{
    Computed: true,
}

// User OR API can set
"optional_computed": schema.StringAttribute{
    Optional: true,
    Computed: true,
}
```

#### ⚠️ Import Testing
```go
// ALWAYS test imports with verification
{
    ResourceName:      "myresource.test",
    ImportState:       true,
    ImportStateVerify: true,
}
```

### 4. Testing Strategy
1. **Create acceptance tests** for each migrated resource
2. **Test import scenarios** early and often
3. **Verify no-op plans** after import
4. **Test state compatibility** with existing deployments

### 5. Common Gotchas to Avoid
- ❌ Setting empty strings instead of null values
- ❌ Using `RequiresReplace` on fields that shouldn't force recreation
- ❌ Inconsistent read/write logic between Create and Read functions
- ❌ Missing null/unknown checks before value extraction
- ❌ Using `internal/` directory instead of `{provider_name}/`

### 6. Migration Validation Checklist
- [ ] Project uses `{provider_name}/` directory structure
- [ ] All resources have framework equivalents
- [ ] Import tests pass with `ImportStateVerify: true`
- [ ] No-op plans after apply/import operations
- [ ] State compatibility maintained with existing deployments
- [ ] Error handling follows framework diagnostic patterns

# UniFi Provider Migration: SDK v2 → Plugin Framework

**Target**: [terraform-provider-unifi](https://github.com/ubiquiti-community/terraform-provider-unifi)  
**Reference**: This repo (terraform-provider-ironic) - complete migration example

## Setup
1. Branch from `main` of terraform-provider-unifi
2. Use detailed guides in `.github/instructions/`:
   - `terraform-plugin-framework-guide.md` - Development patterns
   - `terraform-provider-migration-guide.md` - Migration steps

## Critical Steps
### 1. Structure: Create `unifi/` directory (NOT `internal/`)
### 2. Dependencies: Add framework packages to `go.mod`
### 3. Provider: Implement `unifi/provider.go` with framework interfaces

## Most Important: State Management
```go
// ✅ Handle empty as null (prevents import issues)
if apiResponse.Field == "" {
    model.Field = types.StringNull()
} else {
    model.Field = types.StringValue(apiResponse.Field)
}
```

## Priority Resources to Migrate
1. `unifi_wifi_network` - Core WiFi management
2. `unifi_user` - User management  
3. `unifi_port_profile` - Port configuration

## Testing
Always test imports with `ImportStateVerify: true`

## References
- [HashiCorp Framework Overview](https://developer.hashicorp.com/terraform/plugin/framework)
- [HashiCorp Migration Guide](https://developer.hashicorp.com/terraform/plugin/framework/migrating)
- [Framework Resources](https://developer.hashicorp.com/terraform/plugin/framework/resources)
- [Framework Schema Guide](https://developer.hashicorp.com/terraform/plugin/framework/handling-data/schemas)
- [Framework Testing](https://developer.hashicorp.com/terraform/plugin/framework/testing)
- [Provider Mux Documentation](https://developer.hashicorp.com/terraform/plugin/mux)
- [UniFi Mux Branch](https://github.com/ubiquiti-community/terraform-provider-unifi/tree/terraform-provider-mux)
