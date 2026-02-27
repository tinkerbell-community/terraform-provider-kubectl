# kubectl_manifest Migration Completion Guide

## Current Status

### ✅ Completed (Foundation)

1. **Conversion Utilities** (`kubectl/conversion.go`)
   - `dynamicToMap()` - Convert Dynamic → map[string]any
   - `mapToDynamic()` - Convert map[string]any → Dynamic
   - Full test coverage with 100% pass rate

2. **Helper Functions** (`kubectl/manifest_v2_helpers.go`)
   - `buildUnstructured()` - Build Kubernetes objects from Dynamic attributes
   - `setStateFromUnstructured()` - Populate state from API responses
   - `extractMetadataField()` - Extract fields from metadata
   - Full test coverage

3. **New CRUD Implementation** (`kubectl/manifest_crud.go`)
   - `applyManifestV2()` - Server-side apply with field manager
   - `readManifestV2()` - Read resources using Dynamic attributes
   - `deleteManifestV2()` - Delete resources
   - Helper functions for null removal

4. **Schema Update** (`kubectl/manifest_resource.go`)
   - New model with Dynamic attributes
   - Updated Schema() method
   - New wait and field_manager blocks

### 🚧 Remaining Work

## Step 1: Update Create() Method

**Location:** `kubectl/manifest_resource.go` ~line 270

**Current (lines 270-320):**

```go
func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
    var plan manifestResourceModel
    // ... setup code ...
    
    err := backoff.Retry(func() error {
        return r.applyManifest(updateCtx, &plan)  // OLD METHOD
    }, backoffStrategy)
    
    if err := r.readManifest(createCtx, &plan); err != nil {  // OLD METHOD
        // ...
    }
}
```

**Replace with:**

```go
func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
    var plan manifestResourceModel
    diags := req.Plan.Get(ctx, &plan)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    // Get timeout
    createTimeout, diags := plan.Timeouts.Create(ctx, 10*time.Minute)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    createCtx, cancel := context.WithTimeout(ctx, createTimeout)
    defer cancel()

    // Apply with retry
    retryConfig := backoff.NewExponentialBackOff()
    retryConfig.InitialInterval = 3 * time.Second
    retryConfig.MaxInterval = 30 * time.Second
    retryConfig.MaxElapsedTime = createTimeout

    retryCount := r.providerData.ApplyRetryCount
    var backoffStrategy backoff.BackOff = retryConfig
    if retryCount > 0 {
        backoffStrategy = backoff.WithMaxRetries(retryConfig, uint64(retryCount))
    }

    // Use NEW V2 method
    err := backoff.Retry(func() error {
        return r.applyManifestV2(createCtx, &plan)
    }, backoffStrategy)
    
    if err != nil {
        resp.Diagnostics.AddError(
            "Failed to Create Resource",
            fmt.Sprintf("Could not apply manifest: %s", err),
        )
        return
    }

    // Read back using NEW V2 method
    if err := r.readManifestV2(createCtx, &plan); err != nil {
        resp.Diagnostics.AddError(
            "Failed to Read Resource",
            fmt.Sprintf("Could not read manifest after creation: %s", err),
        )
        return
    }

    // Handle wait for rollout if configured
    if err := r.handleWait(createCtx, &plan); err != nil {
        resp.Diagnostics.AddWarning(
            "Wait Condition Not Met",
            fmt.Sprintf("Resource created but wait condition failed: %s", err),
        )
    }

    // Set state
    diags = resp.State.Set(ctx, plan)
    resp.Diagnostics.Append(diags...)
}
```

## Step 2: Update Read() Method

**Location:** `kubectl/manifest_resource.go` ~line 330

**Replace:**

```go
func (r *manifestResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
    var state manifestResourceModel
    diags := req.State.Get(ctx, &state)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    // Use NEW V2 method
    if err := r.readManifestV2(ctx, &state); err != nil {
        if isNotFoundError(err) {
            resp.State.RemoveResource(ctx)
            return
        }
        
        resp.Diagnostics.AddError(
            "Failed to Read Resource",
            fmt.Sprintf("Could not read manifest: %s", err),
        )
        return
    }

    // Set state
    diags = resp.State.Set(ctx, state)
    resp.Diagnostics.Append(diags...)
}
```

## Step 3: Update Update() Method

**Location:** `kubectl/manifest_resource.go` ~line 360

**Similar to Create()**, replace `applyManifest()` with `applyManifestV2()` and `readManifest()` with `readManifestV2()`.

## Step 4: Update Delete() Method

**Location:** `kubectl/manifest_resource.go` ~line 420

**Replace:**

```go
func (r *manifestResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
    var state manifestResourceModel
    diags := req.State.Get(ctx, &state)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    // Check apply_only flag
    if !state.ApplyOnly.IsNull() && state.ApplyOnly.ValueBool() {
        log.Printf("[INFO] Resource has apply_only=true, skipping deletion")
        return
    }

    // Get timeout
    deleteTimeout, diags := state.Timeouts.Delete(ctx, 10*time.Minute)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    deleteCtx, cancel := context.WithTimeout(ctx, deleteTimeout)
    defer cancel()

    // Use NEW V2 method
    if err := r.deleteManifestV2(deleteCtx, &state); err != nil {
        resp.Diagnostics.AddError(
            "Failed to Delete Resource",
            fmt.Sprintf("Could not delete manifest: %s", err),
        )
        return
    }
}
```

## Step 5: Update ModifyPlan() Method

**Location:** `kubectl/manifest_resource.go` ~line 544

**Current logic:**

- Checks yaml_body changes
- Detects UID drift
- Generates fingerprints

**New logic:**

- Check metadata.name/namespace changes → RequiresReplace
- Set status/object as Unknown if metadata/spec changed
- Remove yaml fingerprint logic

**Replace with:**

```go
func (r *manifestResource) ModifyPlan(
    ctx context.Context,
    req resource.ModifyPlanRequest,
    resp *resource.ModifyPlanResponse,
) {
    // Skip if creating or deleting
    if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
        return
    }

    var plan, state manifestResourceModel
    
    diagsPlan := req.Plan.Get(ctx, &plan)
    resp.Diagnostics.Append(diagsPlan...)
    
    diagsState := req.State.Get(ctx, &state)
    resp.Diagnostics.Append(diagsState...)
    
    if resp.Diagnostics.HasError() {
        return
    }

    // Check if metadata.name changed → RequiresReplace
    planName, _ := extractMetadataField(ctx, plan.Metadata, "name")
    stateName, _ := extractMetadataField(ctx, state.Metadata, "name")
    if planName != stateName {
        resp.RequiresReplace = append(resp.RequiresReplace, path.Root("metadata"))
    }

    // Check if metadata.namespace changed → RequiresReplace
    planNs, _ := extractMetadataField(ctx, plan.Metadata, "namespace")
    stateNs, _ := extractMetadataField(ctx, state.Metadata, "namespace")
    if planNs != stateNs {
        resp.RequiresReplace = append(resp.RequiresReplace, path.Root("metadata"))
    }

    // If metadata or spec changed, mark status/object as unknown
    if !plan.Metadata.Equal(state.Metadata) || !plan.Spec.Equal(state.Spec) {
        plan.Status = types.DynamicUnknown()
        plan.Object = types.DynamicUnknown()
    }

    // Update plan
    diags := resp.Plan.Set(ctx, plan)
    resp.Diagnostics.Append(diags...)
}
```

## Step 6: Update ImportState() Method

**Location:** `kubectl/manifest_resource.go` ~line 450

**Current format:** `apiVersion//kind//name//namespace`

**Keep this format, but populate new schema:**

```go
func (r *manifestResource) ImportState(
    ctx context.Context,
    req resource.ImportStateRequest,
    resp *resource.ImportStateResponse,
) {
    // Parse import ID: apiVersion//kind//name[//namespace]
    idParts := strings.Split(req.ID, "//")
    if len(idParts) != 3 && len(idParts) != 4 {
        resp.Diagnostics.AddError(
            "Invalid Import ID",
            fmt.Sprintf(
                "Expected format 'apiVersion//kind//name//namespace' or 'apiVersion//kind//name', got: %s",
                req.ID,
            ),
        )
        return
    }

    apiVersion := idParts[0]
    kind := idParts[1]
    name := idParts[2]
    namespace := ""
    if len(idParts) == 4 {
        namespace = idParts[3]
    }

    // Build metadata
    metadataMap := map[string]any{
        "name": name,
    }
    if namespace != "" {
        metadataMap["namespace"] = namespace
    }
    
    metadataDynamic, diags := mapToDynamic(ctx, metadataMap)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    // Create initial state
    model := manifestResourceModel{
        APIVersion: types.StringValue(apiVersion),
        Kind:       types.StringValue(kind),
        Metadata:   metadataDynamic,
        Spec:       types.DynamicNull(),
    }

    // Read from cluster to get full state
    if err := r.readManifestV2(ctx, &model); err != nil {
        resp.Diagnostics.AddError(
            "Failed to Import Resource",
            fmt.Sprintf("Could not read resource from cluster: %s", err),
        )
        return
    }

    // Set state
    diags = resp.State.Set(ctx, model)
    resp.Diagnostics.Append(diags...)
}
```

## Step 7: Add ValidateConfig() Method

**Add after ImportState():**

```go
func (r *manifestResource) ValidateConfig(
    ctx context.Context,
    req resource.ValidateConfigRequest,
    resp *resource.ValidateConfigResponse,
) {
    var config manifestResourceModel
    diags := req.Config.Get(ctx, &config)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    // Validate metadata has "name" field
    if !config.Metadata.IsNull() && !config.Metadata.IsUnknown() {
        metadataMap, d := dynamicToMap(ctx, config.Metadata)
        resp.Diagnostics.Append(d...)
        if resp.Diagnostics.HasError() {
            return
        }
        
        if _, ok := metadataMap["name"]; !ok {
            resp.Diagnostics.AddAttributeError(
                path.Root("metadata"),
                "Missing Required Field",
                "metadata must contain a 'name' field",
            )
        }
    }
}
```

## Step 8: Implement handleWait() Helper

**Add new method:**

```go
func (r *manifestResource) handleWait(ctx context.Context, model *manifestResourceModel) error {
    // Parse wait configuration
    if model.Wait.IsNull() || len(model.Wait.Elements()) == 0 {
        return nil  // No wait configured
    }

    var waitModels []waitModel
    diags := model.Wait.ElementsAs(ctx, &waitModels, false)
    if diags.HasError() {
        return fmt.Errorf("failed to parse wait configuration: %v", diags)
    }

    if len(waitModels) == 0 {
        return nil
    }

    waitConfig := waitModels[0]

    // Handle rollout wait
    if !waitConfig.Rollout.IsNull() && waitConfig.Rollout.ValueBool() {
        if err := r.waitForRollout(ctx, model); err != nil {
            return err
        }
    }

    // Handle field waits
    if !waitConfig.Fields.IsNull() {
        if err := r.waitForFields(ctx, model, waitConfig.Fields); err != nil {
            return err
        }
    }

    // Handle condition waits
    if !waitConfig.Conditions.IsNull() && len(waitConfig.Conditions.Elements()) > 0 {
        if err := r.waitForConditions(ctx, model, waitConfig.Conditions); err != nil {
            return err
        }
    }

    return nil
}
```

## Step 9: Remove Old Methods

**Delete these methods from manifest_resource.go:**

1. `applyManifest()` (old version ~line 681)
2. `readManifest()` (old version ~line 937)
3. `deleteManifest()` (old version ~line 1033)
4. `generateFingerprints()` (line ~1143)
5. `obfuscateYAML()` (line ~1200)

## Step 10: Add Interface Compliance

**At top of manifest_resource.go, add:**

```go
var (
    _ resource.Resource                   = &manifestResource{}
    _ resource.ResourceWithConfigure      = &manifestResource{}
    _ resource.ResourceWithImportState    = &manifestResource{}
    _ resource.ResourceWithModifyPlan     = &manifestResource{}
    _ resource.ResourceWithValidateConfig = &manifestResource{}  // NEW
)
```

## Step 11: Update Imports

**Ensure these imports are present:**

```go
import (
    "context"
    "fmt"
    "log"
    "strings"
    "time"

    "github.com/cenkalti/backoff/v4"
    "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
    "github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
    "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
    "github.com/hashicorp/terraform-plugin-framework/path"
    "github.com/hashicorp/terraform-plugin-framework/resource"
    "github.com/hashicorp/terraform-plugin-framework/resource/schema"
    "github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
    "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
    "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
    "github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
    "github.com/hashicorp/terraform-plugin-framework/schema/validator"
    "github.com/hashicorp/terraform-plugin-framework/types"
    meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/api/errors"
)
```

**Remove unused imports:**

- `github.com/tinkerbell-community/terraform-provider-kubectl/yaml` (from manifest_resource.go, keep in manifest_crud.go)
- `github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/util` (from manifest_resource.go, keep in manifest_crud.go)
- `github.com/thedevsaddam/gojsonq/v2`
- `crypto/sha256`
- `encoding/base64`
- `regexp`
- `sort`

## Step 12: Testing

### Unit Tests

```bash
go test -v ./kubectl/conversion_test.go ./kubectl/conversion.go
go test -v ./kubectl/manifest_v2_helpers_test.go ./kubectl/manifest_v2_helpers.go ./kubectl/conversion.go
```

### Build Test

```bash
go build ./kubectl/...
```

### Acceptance Tests

```bash
TF_ACC=1 go test -v ./kubectl/manifest_resource_test.go -timeout 120m
```

## Step 13: Update Tests

**Location:** `kubectl/manifest_resource_test.go`

Update test configurations from:

```hcl
resource "kubectl_manifest" "test" {
  yaml_body = <<-YAML
    apiVersion: v1
    kind: ConfigMap
    ...
  YAML
}
```

To:

```hcl
resource "kubectl_manifest" "test" {
  api_version = "v1"
  kind        = "ConfigMap"
  
  metadata = {
    name      = "test-config"
    namespace = "default"
  }
  
  # For ConfigMap, no spec needed
  # For Deployment, add: spec = { replicas = 3, ... }
}
```

## Compilation Checklist

- [ ] `go build ./kubectl/...` succeeds
- [ ] No references to `yaml_body`, `yaml_body_parsed`, `yaml_incluster`
- [ ] No references to `uid`, `live_uid`, `name`, `namespace` (as separate attributes)
- [ ] No references to `force_new`, `server_side_apply`, `validate_schema`
- [ ] No references to `wait_for_rollout` (bool)
- [ ] No references to `waitFieldModel` struct

## Final Validation

```bash
# 1. Build
go build ./kubectl/...

# 2. Format
go fmt ./kubectl/...

# 3. Vet
go vet ./kubectl/...

# 4. Test
go test ./kubectl/...

# 5. Acceptance (requires K8s cluster)
TF_ACC=1 go test -v ./kubectl/... -timeout 120m
```

## Breaking Changes Communication

Users MUST update their configurations:

### Migration Example

**Before (v0):**

```hcl
resource "kubectl_manifest" "example" {
  yaml_body = file("deployment.yaml")
  
  wait_for_rollout = true
  field_manager    = "terraform"
  force_conflicts  = false
}
```

**After (v1):**

```hcl
resource "kubectl_manifest" "example" {
  api_version = "apps/v1"
  kind        = "Deployment"
  
  metadata = {
    name      = "nginx"
    namespace = "default"
    labels = {
      app = "nginx"
    }
  }
  
  spec = {
    replicas = 3
    selector = {
      matchLabels = {
        app = "nginx"
      }
    }
    template = {
      metadata = {
        labels = {
          app = "nginx"
        }
      }
      spec = {
        containers = [{
          name  = "nginx"
          image = "nginx:1.21"
          ports = [{
            containerPort = 80
          }]
        }]
      }
    }
  }
  
  wait {
    rollout = true
  }
  
  field_manager {
    name            = "terraform"
    force_conflicts = false
  }
}
```

### Using provider::kubectl::manifest_decode Function

Users can migrate gradually using:

```hcl
locals {
  manifests = provider::kubectl::manifest_decode(file("deployment.yaml"))
}

resource "kubectl_manifest" "example" {
  api_version = local.manifests.apiVersion
  kind        = local.manifests.kind
  metadata    = local.manifests.metadata
  spec        = try(local.manifests.spec, null)
}
```

## Estimated Completion Time

- Steps 1-6: 4-6 hours (CRUD and ModifyPlan updates)
- Steps 7-10: 2-3 hours (Validation, cleanup)
- Steps 11-13: 2-3 hours (Testing updates)
- **Total: 8-12 hours** focused development

## Success Criteria

✅ All unit tests pass
✅ `go build ./kubectl/...` succeeds
✅ Acceptance tests pass (requires K8s cluster)
✅ Import works correctly
✅ No breaking changes in provider block
✅ Documentation updated
