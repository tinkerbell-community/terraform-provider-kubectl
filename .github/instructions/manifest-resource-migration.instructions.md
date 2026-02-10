# Migration: `yaml_body` → Dynamic Type Attributes in `kubectl_manifest`

## Goal

Replace the `yaml_body` string attribute in `kubectl/manifest_resource.go` with structured, dynamic-typed top-level attributes (`apiVersion`, `kind`, `metadata`, `spec`) mirroring the raw protocol `kubernetes_manifest` resource in `kubectl/provider/`, but implemented entirely with the **terraform-plugin-framework** using `schema.DynamicAttribute` and `types.Dynamic`.

Add `status` as a computed-only dynamic attribute. Update the `wait` block to match the `kubernetes_manifest` specification.

### Target HCL

```hcl
resource "kubectl_manifest" "default" {
  api_version = "apiextensions.k8s.io/v1"
  kind        = "CustomResourceDefinition"

  metadata = {
    name = "testcrds.hashicorp.com"
  }

  spec = {
    group = "hashicorp.com"
    names = {
      kind   = "TestCrd"
      plural = "testcrds"
    }
    scope = "Namespaced"
    versions = [{
      name    = "v1"
      served  = true
      storage = true
      schema = {
        openAPIV3Schema = {
          type = "object"
          properties = {
            data = { type = "string" }
            refs = { type = "number" }
          }
        }
      }
    }]
  }

  wait {
    rollout = true
  }

  field_manager {
    name             = "Terraform"
    force_conflicts  = false
  }
}
```

---

## Phase 1: New Schema Definition

### 1.1 Replace `yaml_body` with Split Attributes

Remove `yaml_body` (and its derivatives `yaml_body_parsed`, `yaml_incluster`, `live_manifest_incluster`) and introduce:

| Attribute       | Framework Type             | Required/Computed | Plan Modifier            |
|-----------------|----------------------------|-------------------|--------------------------|
| `api_version`   | `schema.StringAttribute`   | Required          | `RequiresReplace()`      |
| `kind`          | `schema.StringAttribute`   | Required          | `RequiresReplace()`      |
| `metadata`      | `schema.DynamicAttribute`  | Required          | `RequiresReplace()` on `name`, `namespace` (custom plan modifier) |
| `spec`          | `schema.DynamicAttribute`  | Optional          | None (in-place update)   |
| `status`        | `schema.DynamicAttribute`  | Computed only      | `UseStateForUnknown()`   |
| `object`        | `schema.DynamicAttribute`  | Computed only      | None                     |

#### Schema Code

```go
func (r *manifestResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
    resp.Schema = schema.Schema{
        Attributes: map[string]schema.Attribute{
            "id": schema.StringAttribute{
                Computed: true,
                PlanModifiers: []planmodifier.String{
                    stringplanmodifier.UseStateForUnknown(),
                },
            },
            "api_version": schema.StringAttribute{
                Required:            true,
                MarkdownDescription: "Kubernetes API version (e.g., `v1`, `apps/v1`).",
                PlanModifiers: []planmodifier.String{
                    stringplanmodifier.RequiresReplace(),
                },
            },
            "kind": schema.StringAttribute{
                Required:            true,
                MarkdownDescription: "Kubernetes resource kind (e.g., `ConfigMap`, `Deployment`).",
                PlanModifiers: []planmodifier.String{
                    stringplanmodifier.RequiresReplace(),
                },
            },
            "metadata": schema.DynamicAttribute{
                Required:            true,
                MarkdownDescription: "Standard Kubernetes object metadata.",
                // Custom plan modifier handles RequiresReplace for name/namespace
            },
            "spec": schema.DynamicAttribute{
                Optional:            true,
                MarkdownDescription: "Resource specification. Structure depends on the resource kind.",
            },
            "status": schema.DynamicAttribute{
                Computed:            true,
                MarkdownDescription: "Resource status as reported by the Kubernetes API server.",
            },
            "object": schema.DynamicAttribute{
                Computed:            true,
                MarkdownDescription: "The full resource object as returned by the API server.",
            },
            "computed_fields": schema.ListAttribute{
                ElementType:         types.StringType,
                Optional:            true,
                MarkdownDescription: "List of manifest fields whose values may be altered by the API server. Defaults to: [\"metadata.annotations\", \"metadata.labels\"].",
            },
            // ... timeouts, field_manager, wait blocks below
        },
        Blocks: map[string]schema.Block{
            // See Phase 3 for wait/field_manager blocks
        },
    }
}
```

### 1.2 Update Resource Model

```go
type manifestResourceModel struct {
    ID             types.String   `tfsdk:"id"`
    APIVersion     types.String   `tfsdk:"api_version"`
    Kind           types.String   `tfsdk:"kind"`
    Metadata       types.Dynamic  `tfsdk:"metadata"`
    Spec           types.Dynamic  `tfsdk:"spec"`
    Status         types.Dynamic  `tfsdk:"status"`
    Object         types.Dynamic  `tfsdk:"object"`
    ComputedFields types.List     `tfsdk:"computed_fields"`
    Timeouts       timeouts.Value `tfsdk:"timeouts"`
    // Wait and FieldManager are nested blocks — use typed models
}
```

**Key**: `types.Dynamic` wraps an `attr.Value` with an underlying type that is determined at runtime. Access the concrete value via `model.Metadata.UnderlyingValue()`.

---

## Phase 2: Custom Type for OpenAPI-Aware Dynamic Values

### 2.1 Why a Custom Type

The raw protocol resource uses `TFTypeFromOpenAPI` (in `kubectl/provider/resource.go`) to resolve a `tftypes.Type` from the cluster's OpenAPI spec at plan time. This enables:

- **Type-aware planning**: Unknown values are backfilled with correct types for unspecified attributes
- **Computed field handling**: Fields like `metadata.annotations` can be marked as computed/unknown
- **Validation**: Config values are validated against the OpenAPI schema

The Plugin Framework's `schema.DynamicAttribute` accepts any `types.Dynamic` value, but it does not perform type resolution by itself. A **custom type** can encapsulate OpenAPI resolution logic.

### 2.2 Custom Type Design

Create a new package `kubectl/types/` with a custom `KubernetesManifestType` that implements `attr.Type`:

```go
package kubetypes

import (
    "context"

    "github.com/hashicorp/terraform-plugin-framework/attr"
    "github.com/hashicorp/terraform-plugin-framework/diag"
    "github.com/hashicorp/terraform-plugin-go/tftypes"
)

// ManifestType is a custom attr.Type that can resolve its structure
// from OpenAPI at plan time. At the schema level it behaves as DynamicPseudoType,
// but during planning the provider resolves it to a concrete tftypes.Object.
//
// NOTE: This is an advanced pattern. The simpler approach is to use
// schema.DynamicAttribute directly and handle OpenAPI resolution in
// ModifyPlan / plan modifiers. This custom type approach is only
// recommended if you need reusable type-level validation.
type ManifestType struct {
    // ResolvedType is set during planning after OpenAPI lookup.
    // Nil means unresolved (uses DynamicPseudoType).
    ResolvedType tftypes.Type
}
```

**However**, for the initial migration, the **simpler recommended approach** is to keep `schema.DynamicAttribute` directly and perform all OpenAPI resolution logic inside a **custom plan modifier** or inside `ModifyPlan`. This avoids the complexity of a full custom type implementation.

### 2.3 Recommended Approach: Plan Modifier

Create a plan modifier that:

1. Reads `api_version` and `kind` from the plan
2. Calls `TFTypeFromOpenAPI` (ported from `kubectl/provider/resource.go`) to get the resolved type
3. Uses `morph.ValueToType` to transform the user's `spec` value to match the OpenAPI type
4. Uses `morph.DeepUnknown` to backfill unspecified attributes with Unknown
5. Marks `computed_fields` paths as Unknown
6. Sets `status` as Unknown (will be filled after apply)

```go
// manifestPlanModifier implements resource.ResourceWithModifyPlan
func (r *manifestResource) ModifyPlan(
    ctx context.Context,
    req resource.ModifyPlanRequest,
    resp *resource.ModifyPlanResponse,
) {
    if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
        // Create or destroy — handle accordingly
    }

    var plan manifestResourceModel
    diags := req.Plan.Get(ctx, &plan)
    resp.Diagnostics.Append(diags...)
    if resp.Diagnostics.HasError() {
        return
    }

    // 1. Resolve GVK
    gvk := resolveGVK(plan.APIVersion.ValueString(), plan.Kind.ValueString())

    // 2. Get OpenAPI type (port TFTypeFromOpenAPI)
    objectType, hints, err := r.tfTypeFromOpenAPI(ctx, gvk, false)

    // 3. Convert plan.Spec (types.Dynamic) to tftypes.Value
    specTfValue := frameworkToTftypes(plan.Spec)

    // 4. Morph to match OpenAPI type
    morphedSpec, d := morph.ValueToType(specTfValue, specType, path)

    // 5. Backfill unknown attributes
    completeSpec, err := morph.DeepUnknown(specType, morphedSpec, path)

    // 6. Convert back to types.Dynamic and set in plan
    plan.Spec = tftypesToFramework(completeSpec)

    // 7. Set status as unknown (computed)
    plan.Status = types.DynamicUnknown()

    // 8. Handle RequiresReplace for metadata.name and metadata.namespace
    checkMetadataRequiresReplace(req, resp, plan)

    resp.Plan.Set(ctx, plan)
}
```

---

## Phase 3: Update Wait and Field Manager Blocks

### 3.1 Replace Current Wait Attributes

**Remove** the following flat attributes:
- `wait` (Bool) — currently used for delete wait
- `wait_for_rollout` (Bool) — currently standalone

**Remove** the current `wait_for` list nested block.

**Add** new blocks matching the `kubernetes_manifest` pattern:

```go
Blocks: map[string]schema.Block{
    "wait": schema.ListNestedBlock{
        MarkdownDescription: "Configure waiter options.",
        Validators: []validator.List{
            listvalidator.SizeAtMost(1),
        },
        NestedObject: schema.NestedBlockObject{
            Attributes: map[string]schema.Attribute{
                "rollout": schema.BoolAttribute{
                    Optional:            true,
                    MarkdownDescription: "Wait for rollout to complete on resources that support `kubectl rollout status`.",
                },
                "fields": schema.MapAttribute{
                    ElementType:         types.StringType,
                    Optional:            true,
                    MarkdownDescription: "A map of field paths to expected values. Wait until all match.",
                },
            },
            Blocks: map[string]schema.Block{
                "condition": schema.ListNestedBlock{
                    MarkdownDescription: "Wait for status conditions to match.",
                    NestedObject: schema.NestedBlockObject{
                        Attributes: map[string]schema.Attribute{
                            "type": schema.StringAttribute{
                                Optional:            true,
                                MarkdownDescription: "The type of condition.",
                            },
                            "status": schema.StringAttribute{
                                Optional:            true,
                                MarkdownDescription: "The condition status.",
                            },
                        },
                    },
                },
            },
        },
    },
    "field_manager": schema.ListNestedBlock{
        MarkdownDescription: "Configure field manager options.",
        Validators: []validator.List{
            listvalidator.SizeAtMost(1),
        },
        NestedObject: schema.NestedBlockObject{
            Attributes: map[string]schema.Attribute{
                "name": schema.StringAttribute{
                    Optional:            true,
                    Computed:            true,
                    Default:             stringdefault.StaticString("Terraform"),
                    MarkdownDescription: "The name to use for the field manager.",
                },
                "force_conflicts": schema.BoolAttribute{
                    Optional:            true,
                    Computed:            true,
                    Default:             booldefault.StaticBool(false),
                    MarkdownDescription: "Force changes against conflicts.",
                },
            },
        },
    },
    "timeouts": timeouts.Block(ctx, timeouts.Opts{
        Create: true,
        Update: true,
        Delete: true,
    }),
},
```

### 3.2 Updated Wait Model

```go
type waitModel struct {
    Rollout    types.Bool   `tfsdk:"rollout"`
    Fields     types.Map    `tfsdk:"fields"`
    Conditions []waitConditionModel `tfsdk:"condition"`
}

type waitConditionModel struct {
    Type   types.String `tfsdk:"type"`
    Status types.String `tfsdk:"status"`
}

type fieldManagerModel struct {
    Name           types.String `tfsdk:"name"`
    ForceConflicts types.Bool   `tfsdk:"force_conflicts"`
}
```

---

## Phase 4: CRUD Operations

### 4.1 Constructing Unstructured Objects from Split Attributes

The current implementation reads `yaml_body` and calls `yaml.ParseYAML()`. The new implementation must reconstruct the unstructured object from split attributes:

```go
func (r *manifestResource) buildUnstructured(ctx context.Context, model *manifestResourceModel) (*unstructured.Unstructured, error) {
    obj := map[string]any{
        "apiVersion": model.APIVersion.ValueString(),
        "kind":       model.Kind.ValueString(),
    }

    // Convert metadata (types.Dynamic → map[string]any)
    metadata, err := dynamicToMap(model.Metadata)
    if err != nil {
        return nil, fmt.Errorf("failed to convert metadata: %w", err)
    }
    obj["metadata"] = metadata

    // Convert spec (types.Dynamic → map[string]any) — may be nil
    if !model.Spec.IsNull() && !model.Spec.IsUnknown() {
        spec, err := dynamicToMap(model.Spec)
        if err != nil {
            return nil, fmt.Errorf("failed to convert spec: %w", err)
        }
        obj["spec"] = spec
    }

    uo := &unstructured.Unstructured{}
    uo.SetUnstructuredContent(obj)
    return uo, nil
}
```

**Reuse** the `encodeValue` function from `kubectl/functions/encode.go` for `types.Dynamic` → `map[string]any` conversion. The existing `encodeObject`, `encodeTuple`, etc. handle all framework attr types.

**Reuse** the `decode`/`decodeMapping` functions from `kubectl/functions/decode.go` for `map[string]any` → `types.Dynamic` conversion (API response → framework state).

### 4.2 Apply (Create / Update)

```go
func (r *manifestResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
    var plan manifestResourceModel
    diags := req.Plan.Get(ctx, &plan)
    // ...

    // Build unstructured from split attributes
    uo, err := r.buildUnstructured(ctx, &plan)

    // Remove nulls (like mapRemoveNulls in provider/resource.go)
    rqObj := mapRemoveNulls(uo.UnstructuredContent())
    uo.SetUnstructuredContent(rqObj)

    // Get REST client
    gvr, err := GVRFromUnstructured(uo, restMapper)
    // ...

    // Apply via Patch (server-side apply)
    jsonManifest, _ := uo.MarshalJSON()
    result, err := rs.Patch(ctx, name, types.ApplyPatchType, jsonManifest,
        metav1.PatchOptions{
            FieldManager: fieldManagerName,
            Force:        &forceConflicts,
        },
    )

    // Convert response to framework state
    r.setStateFromResponse(ctx, result, &plan)

    resp.State.Set(ctx, plan)
}
```

### 4.3 Read

```go
func (r *manifestResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
    var state manifestResourceModel
    diags := req.State.Get(ctx, &state)
    // ...

    // Get resource from API
    result, err := rs.Get(ctx, name, metav1.GetOptions{})

    // Remove server-side fields (from provider/resource.go)
    cleaned := RemoveServerSideFields(result.UnstructuredContent())

    // Split response into metadata/spec/status
    r.setStateFromResponse(ctx, result, &state)

    resp.State.Set(ctx, state)
}
```

### 4.4 `setStateFromResponse` Helper

```go
func (r *manifestResource) setStateFromResponse(
    ctx context.Context,
    result *unstructured.Unstructured,
    model *manifestResourceModel,
) error {
    content := result.UnstructuredContent()

    // Set api_version and kind
    model.APIVersion = types.StringValue(result.GetAPIVersion())
    model.Kind = types.StringValue(result.GetKind())

    // Convert metadata map → types.Dynamic using decode.go patterns
    if meta, ok := content["metadata"]; ok {
        metaDynamic, err := mapToFrameworkDynamic(meta)
        model.Metadata = metaDynamic
    }

    // Convert spec → types.Dynamic
    if spec, ok := content["spec"]; ok {
        specDynamic, err := mapToFrameworkDynamic(spec)
        model.Spec = specDynamic
    }

    // Convert status → types.Dynamic (computed)
    if status, ok := content["status"]; ok {
        statusDynamic, err := mapToFrameworkDynamic(status)
        model.Status = statusDynamic
    }

    // Convert full object → types.Dynamic
    objectDynamic, err := mapToFrameworkDynamic(content)
    model.Object = objectDynamic

    return nil
}
```

### 4.5 Conversion Functions

Port/wrap from existing code:

```go
// mapToFrameworkDynamic converts map[string]any → types.Dynamic
// Uses decodeMapping from kubectl/functions/decode.go
func mapToFrameworkDynamic(v any) (types.Dynamic, error) {
    attrVal, err := decodeAny(v) // wraps decodeMapping/decodeSequence/decodeScalar
    if err != nil {
        return types.DynamicNull(), err
    }
    return types.DynamicValue(attrVal), nil
}

// dynamicToMap converts types.Dynamic → map[string]any
// Uses encodeValue from kubectl/functions/encode.go
func dynamicToMap(d types.Dynamic) (map[string]any, error) {
    v := d.UnderlyingValue()
    result, err := encodeValue(v)
    if err != nil {
        return nil, err
    }
    m, ok := result.(map[string]any)
    if !ok {
        return nil, fmt.Errorf("expected map, got %T", result)
    }
    return m, nil
}
```

---

## Phase 5: Plan Modification — OpenAPI Type Resolution

### 5.1 Port `TFTypeFromOpenAPI`

Copy the core logic from `kubectl/provider/resource.go:TFTypeFromOpenAPI` into `manifest_resource.go` (or a shared helper package). The function:

1. Gets the OpenAPI v2 foundry from the provider
2. Checks if the GVK is a CRD (via `lookUpGVKinCRDs`)
3. Gets the `tftypes.Type` from the OpenAPI spec
4. Strips `status` from the type
5. Backfills `apiVersion`, `kind`, `metadata` for CRDs

### 5.2 Convert Between Framework `types.Dynamic` and `tftypes.Value`

The morph package operates on `tftypes.Value`. Need bridge functions:

```go
// frameworkDynamicToTftypes converts types.Dynamic → tftypes.Value
func frameworkDynamicToTftypes(d types.Dynamic) (tftypes.Value, error) {
    underlying := d.UnderlyingValue()
    // Use terraform-plugin-framework's internal conversion
    // or manually traverse the attr.Value tree
    return attrValueToTftypes(underlying)
}

// tftypesToFrameworkDynamic converts tftypes.Value → types.Dynamic
func tftypesToFrameworkDynamic(v tftypes.Value) (types.Dynamic, error) {
    // Convert tftypes.Value → map[string]any → types.Dynamic
    // Or use a direct recursive conversion
    m, err := payload.FromTFValue(v, nil, tftypes.NewAttributePath())
    if err != nil {
        return types.DynamicNull(), err
    }
    return mapToFrameworkDynamic(m)
}
```

### 5.3 ModifyPlan Flow

```
User Config                        OpenAPI Spec
    │                                    │
    ▼                                    ▼
api_version + kind ──────────→ resolveGVK() ──→ TFTypeFromOpenAPI()
                                                      │
                                                      ▼
                                              objectType (tftypes.Object)
                                                      │
    ┌─────────────────────────────────────────────────┘
    │
    ▼
plan.Spec (types.Dynamic) ──→ frameworkDynamicToTftypes() ──→ specTfValue
    │
    ▼
morph.ValueToType(specTfValue, specType) ──→ morphedSpec
    │
    ▼
morph.DeepUnknown(specType, morphedSpec) ──→ completeSpec
    │
    ▼
Mark computed_fields as Unknown
    │
    ▼
tftypesToFrameworkDynamic(completeSpec) ──→ plan.Spec
    │
    ▼
plan.Status = types.DynamicUnknown()
plan.Object = types.DynamicUnknown()
```

---

## Phase 6: RequiresReplace Logic

From the raw protocol resource (`plan.go`), these paths trigger replacement:

- `manifest.apiVersion` → `api_version` (handled by `stringplanmodifier.RequiresReplace()`)
- `manifest.kind` → `kind` (handled by `stringplanmodifier.RequiresReplace()`)
- `manifest.metadata.name` → inside `metadata` dynamic value
- `manifest.metadata.namespace` → inside `metadata` dynamic value (only if namespaced)

Since `metadata` is a `schema.DynamicAttribute`, standard plan modifiers won't detect changes to nested fields. Implement this in `ModifyPlan`:

```go
// In ModifyPlan, after reading plan and state:
priorMeta, _ := dynamicToMap(state.Metadata)
plannedMeta, _ := dynamicToMap(plan.Metadata)

if priorMeta["name"] != plannedMeta["name"] {
    resp.RequiresReplace = append(resp.RequiresReplace, path.Root("metadata"))
}
if priorMeta["namespace"] != plannedMeta["namespace"] {
    // Only for namespaced resources
    resp.RequiresReplace = append(resp.RequiresReplace, path.Root("metadata"))
}
```

---

## Phase 7: Attributes to Remove

The following attributes become unnecessary with the new schema:

| Attribute                 | Reason for Removal                                       |
|---------------------------|----------------------------------------------------------|
| `yaml_body`               | Replaced by `api_version`, `kind`, `metadata`, `spec`    |
| `yaml_body_parsed`        | No longer relevant — input is structured, not a string   |
| `yaml_incluster`          | Drift detection via `object` computed attribute instead   |
| `live_manifest_incluster` | Same as above                                            |
| `name`                    | Accessible via `metadata`                                |
| `namespace`               | Accessible via `metadata`                                |
| `uid`                     | Accessible via returned `object` or `metadata`           |
| `live_uid`                | Accessible via returned `object`                         |
| `override_namespace`      | User sets namespace directly in `metadata`               |
| `validate_schema`         | Validation happens automatically via OpenAPI resolution  |
| `sensitive_fields`        | Review: may still be needed, but simpler with structured input |
| `force_new`               | Replaced by proper RequiresReplace logic on metadata     |
| `wait_for_rollout`        | Merged into `wait { rollout = true }`                    |
| `wait` (Bool)             | Merged into `wait` block                                 |

### Attributes to Keep (Possibly Renamed)

| Attribute                 | Keep As                | Notes                        |
|---------------------------|------------------------|------------------------------|
| `id`                      | `id`                   | Unchanged                    |
| `server_side_apply`       | Remove                 | Always use SSA in v2         |
| `field_manager`           | → `field_manager` block| Nested block, not string     |
| `force_conflicts`         | → in `field_manager`   | Nested under field_manager   |
| `apply_only`              | `apply_only`           | Keep                         |
| `ignore_fields`           | `computed_fields`      | Rename/merge with computed_fields |
| `delete_cascade`          | `delete_cascade`       | Keep                         |
| `timeouts`                | `timeouts`             | Add `update` and `delete`    |

---

## Phase 8: Validation

Port validation logic from `kubectl/provider/validate.go`:

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

    // 1. Validate metadata has "name"
    if !config.Metadata.IsNull() && !config.Metadata.IsUnknown() {
        meta, err := dynamicToMap(config.Metadata)
        if err == nil {
            if _, ok := meta["name"]; !ok {
                resp.Diagnostics.AddAttributeError(
                    path.Root("metadata"),
                    "Missing required field",
                    "metadata must contain a 'name' field",
                )
            }
        }
    }

    // 2. Validate wait block (only one waiter type)
    // Port from validate.go: validateWaitFor
}
```

---

## Phase 9: Import

Update import to set split attributes:

```go
func (r *manifestResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
    // Format: apiVersion//kind//name[//namespace]
    // Parse → set api_version, kind, metadata = {name, namespace}
    // Fetch from API → set spec, status, object from response
}
```

---

## File Change Summary

| File                                | Action  | Description                                        |
|-------------------------------------|---------|-----------------------------------------------------|
| `kubectl/manifest_resource.go`      | Rewrite | New schema, model, CRUD, ModifyPlan, validation     |
| `kubectl/manifest_resource_test.go` | Rewrite | Tests for new schema structure                      |
| `kubectl/types/` (new)              | Create  | Optional: custom type for manifest dynamic values   |
| `kubectl/functions/decode.go`       | Export  | Export `decodeMapping`, `decodeScalar`, etc. or copy |
| `kubectl/functions/encode.go`       | Export  | Export `encodeValue`, `encodeObject`, etc. or copy   |
| `kubectl/provider/resource.go`      | Share   | Port `TFTypeFromOpenAPI`, `RemoveServerSideFields`   |
| `kubectl/provider_model.go`         | Update  | May need new provider data fields for OpenAPI access |
| `docs/resources/manifest.md`        | Rewrite | Document new HCL syntax                             |

---

## Key Implementation Concerns

### 1. `types.Dynamic` ↔ `tftypes.Value` Bridge

The morph and payload packages operate on `tftypes.Value`. The framework uses `attr.Value` / `types.Dynamic`. A clean bidirectional bridge is critical:

- **Framework → tftypes**: Use `terraform-plugin-framework`'s `attr.Value.ToTerraformValue(ctx)` method
- **tftypes → Framework**: Use the `decode.go` functions (`decodeMapping`/`decodeSequence`/`decodeScalar`) which already produce `types.ObjectValue`/`types.TupleValue`/etc.

### 2. Plan Stability

With `DynamicPseudoType`, the planned value's type can change between plan and apply if the OpenAPI schema changes. Ensure plan stability by:
- Caching the resolved type during planning
- Using `UseStateForUnknown` for stable computed fields

### 3. Unknown Value Handling

`types.DynamicUnknown()` represents the entire dynamic value as unknown. For partial unknowns (e.g., only `metadata.annotations` is unknown), you need to build a `types.Dynamic` wrapping a `types.ObjectValue` where specific attributes are `types.StringUnknown()`. This requires type-aware construction from the OpenAPI-resolved type.

### 4. State Upgrade

Since this is a breaking schema change (removing `yaml_body`), implement `UpgradeState` to migrate from the old schema:

```go
func (r *manifestResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
    return map[int64]resource.StateUpgrader{
        0: {
            // Parse yaml_body from v0 state
            // Split into api_version, kind, metadata, spec
            // Set status and object as null
            StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
                // ...
            },
        },
    }
}
```

---

## Migration Order

1. **Export/share conversion functions** from `kubectl/functions/` (decode.go, encode.go)
2. **Port `TFTypeFromOpenAPI`** and related helpers into a shared package
3. **Define new schema** with `schema.DynamicAttribute` attributes
4. **Implement `ModifyPlan`** with OpenAPI type resolution
5. **Implement CRUD operations** using `buildUnstructured` / `setStateFromResponse`
6. **Implement validation** (port from `validate.go`)
7. **Implement import**
8. **Implement state upgrader** (yaml_body → split attributes)
9. **Update wait block** and field_manager block
10. **Write tests**
11. **Update documentation**
