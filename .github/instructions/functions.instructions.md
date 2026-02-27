Review the implementation in the hashicorp/kubernetes provider to compare the functionality with what is present in the tinkerbell-community/kubectl provider.

Consider the kubernetes provider's implemention of the yaml convertion:

- Repo Link: https://github.com/hashicorp/terraform-provider-kubernetes/blob/main/manifest/provider/datasource.go
- Raw Link: https://raw.githubusercontent.com/hashicorp/terraform-provider-kubernetes/8496b96e808c5ef3bc7bc7b9a98c7e236ebf79f5/manifest/provider/datasource.go

This looks like a more modern approach, which has seemed more stable with the inclusion of the functions it introduced for the plugin framework. We should replace the custom yaml conversion 

The code for [`ManifestDecodeMulti`](https://github.com/hashicorp/terraform-provider-kubernetes/blob/main/internal/framework/provider/functions/manifest_decode_multi.go) looks possibly ideal:

```go
func (f ManifestDecodeMultiFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
  var manifest string

  resp.Error = req.Arguments.Get(ctx, &manifest)
  if resp.Error != nil {
    return
  }

  tv, diags := decode(ctx, manifest)
  if diags.HasError() {
    resp.Error = function.FuncErrorFromDiags(ctx, diags)
    return
  }

  dynamResp := types.DynamicValue(tv)
  resp.Error = resp.Result.Set(ctx, &dynamResp)
}
```
- [Encode](internal/framework/provider/functions/encode.go)
- [Decode](https://github.com/hashicorp/terraform-provider-kubernetes/blob/8496b96e808c5ef3bc7bc7b9a98c7e236ebf79f5/internal/framework/provider/functions/decode.go#L20-L54):

```go
func decode(ctx context.Context, manifest string) (v types.Tuple, diags diag.Diagnostics) {
  docs := documentSeparator.Split(manifest, -1)
  dtypes := []attr.Type{}
  dvalues := []attr.Value{}
  diags = diag.Diagnostics{}

  for _, d := range docs {
    var data map[string]any
    err := yaml.Unmarshal([]byte(d), &data)
    if err != nil {
      diags.Append(diag.NewErrorDiagnostic("Invalid YAML document", err.Error()))
      return
    }

    if len(data) == 0 {
      diags.Append(diag.NewWarningDiagnostic("Empty document", "encountered a YAML document with no values"))
      continue
    }

    if err := validateKubernetesManifest(data); err != nil {
      diags.Append(diag.NewErrorDiagnostic("Invalid Kubernetes manifest", err.Error()))
      return
    }

    obj, d := decodeScalar(ctx, data)
    diags.Append(d...)
    if diags.HasError() {
      return
    }
    dtypes = append(dtypes, obj.Type(ctx))
    dvalues = append(dvalues, obj)
  }

  return types.TupleValue(dtypes, dvalues)
}
```

Consider whether the simplicity of this implementation may assist in reconciling the erros we're seeing in the plugin framework.

Change the data sources for #file:file_documents_data_source.go , #file:filename_list_data_source.go and #file:path_documents_data_source.go to all be functions instead of data sources. Follow the pattern from the kubernetes provider

Validate all acceptance tests are passing before considering anything a success.