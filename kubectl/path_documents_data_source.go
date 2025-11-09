package kubectl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/alekc/terraform-provider-kubectl/yaml"
	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/tryfunc"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform/lang/funcs"
	ctyyaml "github.com/zclconf/go-cty-yaml"
	"github.com/zclconf/go-cty/cty"
	ctyconvert "github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

// Ensure provider defined types fully satisfy framework interfaces
var _ datasource.DataSource = &pathDocumentsDataSource{}

// pathDocumentsDataSource defines the data source implementation
type pathDocumentsDataSource struct{}

// pathDocumentsDataSourceModel describes the data source data model
type pathDocumentsDataSourceModel struct {
	ID              types.String `tfsdk:"id"`
	Pattern         types.String `tfsdk:"pattern"`
	Documents       types.List   `tfsdk:"documents"`
	Manifests       types.Map    `tfsdk:"manifests"`
	Vars            types.Map    `tfsdk:"vars"`
	SensitiveVars   types.Map    `tfsdk:"sensitive_vars"`
	DisableTemplate types.Bool   `tfsdk:"disable_template"`
}

// NewPathDocumentsDataSource returns a new path documents data source
func NewPathDocumentsDataSource() datasource.DataSource {
	return &pathDocumentsDataSource{}
}

// Metadata returns the data source type name
func (d *pathDocumentsDataSource) Metadata(
	ctx context.Context,
	req datasource.MetadataRequest,
	resp *datasource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_path_documents"
}

// Schema defines the data source schema
func (d *pathDocumentsDataSource) Schema(
	ctx context.Context,
	req datasource.SchemaRequest,
	resp *datasource.SchemaResponse,
) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Loads and parses Kubernetes YAML manifests from files matching a glob pattern. " +
			"Supports template variable substitution using HCL template syntax.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique identifier for this data source (hash of all documents)",
			},
			"pattern": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Glob pattern to search for YAML files (e.g., `manifests/*.yaml`). " +
					"Uses filepath.Glob syntax.",
			},
			"documents": schema.ListAttribute{
				ElementType:         types.StringType,
				Computed:            true,
				MarkdownDescription: "List of all YAML documents from all matched files.",
			},
			"manifests": schema.MapAttribute{
				ElementType: types.StringType,
				Computed:    true,
				MarkdownDescription: "Map of manifest self-link to YAML content. " +
					"Key format: apiVersion_kind_namespace_name or apiVersion_kind_name for cluster-scoped resources.",
			},
			"vars": schema.MapAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "Variables to substitute in templates using `${var}` syntax. Only primitive types supported.",
				Validators: []validator.Map{
					mapvalidator.ValueStringsAre(),
				},
			},
			"sensitive_vars": schema.MapAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "Sensitive variables to substitute in templates, hidden from terraform output.",
				Validators: []validator.Map{
					mapvalidator.ValueStringsAre(),
				},
			},
			"disable_template": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Flag to disable template parsing of the loaded documents. Defaults to false.",
			},
		},
	}
}

// Read executes the data source logic
func (d *pathDocumentsDataSource) Read(
	ctx context.Context,
	req datasource.ReadRequest,
	resp *datasource.ReadResponse,
) {
	var data pathDocumentsDataSourceModel

	diags := req.Config.Get(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	pattern := data.Pattern.ValueString()

	// Merge vars and sensitive_vars
	vars := make(map[string]interface{})
	if !data.Vars.IsNull() && !data.Vars.IsUnknown() {
		var varsMap map[string]string
		diags := data.Vars.ElementsAs(ctx, &varsMap, false)
		if !diags.HasError() {
			for k, v := range varsMap {
				vars[k] = v
			}
		}
		resp.Diagnostics.Append(diags...)
	}

	if !data.SensitiveVars.IsNull() && !data.SensitiveVars.IsUnknown() {
		var sensitiveVarsMap map[string]string
		diags := data.SensitiveVars.ElementsAs(ctx, &sensitiveVarsMap, false)
		if !diags.HasError() {
			for k, v := range sensitiveVarsMap {
				vars[k] = v
			}
		}
		resp.Diagnostics.Append(diags...)
	}

	disableTemplate := false
	if !data.DisableTemplate.IsNull() && !data.DisableTemplate.IsUnknown() {
		disableTemplate = data.DisableTemplate.ValueBool()
	}

	// Find files matching the glob pattern
	items, err := filepath.Glob(pattern)
	if err != nil {
		resp.Diagnostics.AddError(
			"Glob Pattern Error",
			fmt.Sprintf("Failed to evaluate glob pattern '%s': %s", pattern, err),
		)
		return
	}

	sort.Strings(items)

	// Load and parse all documents
	var allDocuments []string
	for _, item := range items {
		content, err := ioutil.ReadFile(item)
		if err != nil {
			resp.Diagnostics.AddError(
				"File Read Error",
				fmt.Sprintf("Error loading document from file '%s': %s", item, err),
			)
			return
		}

		// Parse template if enabled
		rendered := string(content)
		if !disableTemplate {
			rendered, err = parseTemplate(rendered, vars)
			if err != nil {
				resp.Diagnostics.AddError(
					"Template Parse Error",
					fmt.Sprintf("Failed to render template in '%s': %s", item, err),
				)
				return
			}
		}

		// Split multi-document YAML
		documents, err := yaml.SplitMultiDocumentYAML(rendered)
		if err != nil {
			resp.Diagnostics.AddError(
				"YAML Parse Error",
				fmt.Sprintf("Failed to split YAML from '%s': %s", item, err),
			)
			return
		}

		allDocuments = append(allDocuments, documents...)
	}

	// Parse manifests and build map
	manifests := make(map[string]string)
	for _, doc := range allDocuments {
		manifest, err := yaml.ParseYAML(doc)
		if err != nil {
			resp.Diagnostics.AddError(
				"Manifest Parse Error",
				fmt.Sprintf("Failed to parse YAML as Kubernetes manifest: %s", err),
			)
			return
		}

		parsed, err := manifest.AsYAML()
		if err != nil {
			resp.Diagnostics.AddError(
				"Manifest Conversion Error",
				fmt.Sprintf("Failed to convert manifest to YAML: %s", err),
			)
			return
		}

		selfLink := manifest.GetSelfLink()
		if _, exists := manifests[selfLink]; exists {
			resp.Diagnostics.AddError(
				"Duplicate Manifest",
				fmt.Sprintf("Duplicate manifest found with ID: %s", selfLink),
			)
			return
		}

		manifests[selfLink] = parsed
	}

	// Generate ID from hash of all documents
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(allDocuments, ""))))

	// Convert to Framework types
	docsList, diags := types.ListValueFrom(ctx, types.StringType, allDocuments)
	resp.Diagnostics.Append(diags...)

	manifestsMap, diags := types.MapValueFrom(ctx, types.StringType, manifests)
	resp.Diagnostics.Append(diags...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Set computed values
	data.ID = types.StringValue(id)
	data.Documents = docsList
	data.Manifests = manifestsMap

	// Set state
	diags = resp.State.Set(ctx, &data)
	resp.Diagnostics.Append(diags...)
}

// parseTemplate parses and executes a template using vars
func parseTemplate(s string, vars map[string]interface{}) (string, error) {
	expr, diags := hclsyntax.ParseTemplate([]byte(s), "<template_file>", hcl.Pos{Line: 1, Column: 1})
	if expr == nil || (diags != nil && diags.HasErrors()) {
		return "", diags
	}

	ctx := &hcl.EvalContext{
		Variables: map[string]cty.Value{},
	}

	for k, v := range vars {
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("unexpected type for variable %q: %T", k, v)
		}
		ctx.Variables[k] = cty.StringVal(s)
	}

	ctx.Functions = kubectlPathDocumentsFunctions()

	result, diags := expr.Value(ctx)
	if diags != nil && diags.HasErrors() {
		return "", diags
	}

	// Result must be a string
	var err error
	result, err = ctyconvert.Convert(result, cty.String)
	if err != nil {
		return "", fmt.Errorf("invalid template result: %s", err)
	}

	return result.AsString(), nil
}

var (
	pathDocsFuncsLock                              = &sync.Mutex{}
	pathDocsFuncs     map[string]function.Function = nil
)

// kubectlPathDocumentsFunctions returns the set of functions for template evaluation
func kubectlPathDocumentsFunctions() map[string]function.Function {
	pathDocsFuncsLock.Lock()
	if pathDocsFuncs == nil {
		baseDir := "."
		pathDocsFuncs = map[string]function.Function{
			"abs":              stdlib.AbsoluteFunc,
			"abspath":          funcs.AbsPathFunc,
			"basename":         funcs.BasenameFunc,
			"base64decode":     funcs.Base64DecodeFunc,
			"base64encode":     funcs.Base64EncodeFunc,
			"base64gzip":       funcs.Base64GzipFunc,
			"base64sha256":     funcs.Base64Sha256Func,
			"base64sha512":     funcs.Base64Sha512Func,
			"bcrypt":           funcs.BcryptFunc,
			"can":              tryfunc.CanFunc,
			"ceil":             funcs.CeilFunc,
			"chomp":            funcs.ChompFunc,
			"cidrhost":         funcs.CidrHostFunc,
			"cidrnetmask":      funcs.CidrNetmaskFunc,
			"cidrsubnet":       funcs.CidrSubnetFunc,
			"cidrsubnets":      funcs.CidrSubnetsFunc,
			"coalesce":         funcs.CoalesceFunc,
			"coalescelist":     funcs.CoalesceListFunc,
			"compact":          funcs.CompactFunc,
			"concat":           stdlib.ConcatFunc,
			"contains":         funcs.ContainsFunc,
			"csvdecode":        stdlib.CSVDecodeFunc,
			"dirname":          funcs.DirnameFunc,
			"distinct":         funcs.DistinctFunc,
			"element":          funcs.ElementFunc,
			"chunklist":        funcs.ChunklistFunc,
			"file":             funcs.MakeFileFunc(baseDir, false),
			"fileexists":       funcs.MakeFileExistsFunc(baseDir),
			"fileset":          funcs.MakeFileSetFunc(baseDir),
			"filebase64":       funcs.MakeFileFunc(baseDir, true),
			"filebase64sha256": funcs.MakeFileBase64Sha256Func(baseDir),
			"filebase64sha512": funcs.MakeFileBase64Sha512Func(baseDir),
			"filemd5":          funcs.MakeFileMd5Func(baseDir),
			"filesha1":         funcs.MakeFileSha1Func(baseDir),
			"filesha256":       funcs.MakeFileSha256Func(baseDir),
			"filesha512":       funcs.MakeFileSha512Func(baseDir),
			"flatten":          funcs.FlattenFunc,
			"floor":            funcs.FloorFunc,
			"format":           stdlib.FormatFunc,
			"formatdate":       stdlib.FormatDateFunc,
			"formatlist":       stdlib.FormatListFunc,
			"indent":           funcs.IndentFunc,
			"index":            funcs.IndexFunc,
			"join":             funcs.JoinFunc,
			"jsondecode":       stdlib.JSONDecodeFunc,
			"jsonencode":       stdlib.JSONEncodeFunc,
			"keys":             funcs.KeysFunc,
			"length":           funcs.LengthFunc,
			"list":             funcs.ListFunc,
			"log":              funcs.LogFunc,
			"lookup":           funcs.LookupFunc,
			"lower":            stdlib.LowerFunc,
			"map":              funcs.MapFunc,
			"matchkeys":        funcs.MatchkeysFunc,
			"max":              stdlib.MaxFunc,
			"md5":              funcs.Md5Func,
			"merge":            funcs.MergeFunc,
			"min":              stdlib.MinFunc,
			"parseint":         funcs.ParseIntFunc,
			"pathexpand":       funcs.PathExpandFunc,
			"pow":              funcs.PowFunc,
			"range":            stdlib.RangeFunc,
			"regex":            stdlib.RegexFunc,
			"regexall":         stdlib.RegexAllFunc,
			"replace":          funcs.ReplaceFunc,
			"reverse":          funcs.ReverseFunc,
			"rsadecrypt":       funcs.RsaDecryptFunc,
			"setintersection":  stdlib.SetIntersectionFunc,
			"setproduct":       funcs.SetProductFunc,
			"setsubtract":      stdlib.SetSubtractFunc,
			"setunion":         stdlib.SetUnionFunc,
			"sha1":             funcs.Sha1Func,
			"sha256":           funcs.Sha256Func,
			"sha512":           funcs.Sha512Func,
			"signum":           funcs.SignumFunc,
			"slice":            funcs.SliceFunc,
			"sort":             funcs.SortFunc,
			"split":            funcs.SplitFunc,
			"strrev":           stdlib.ReverseFunc,
			"substr":           stdlib.SubstrFunc,
			"timestamp":        funcs.TimestampFunc,
			"timeadd":          funcs.TimeAddFunc,
			"title":            funcs.TitleFunc,
			"tostring":         funcs.MakeToFunc(cty.String),
			"tonumber":         funcs.MakeToFunc(cty.Number),
			"tobool":           funcs.MakeToFunc(cty.Bool),
			"toset":            funcs.MakeToFunc(cty.Set(cty.DynamicPseudoType)),
			"tolist":           funcs.MakeToFunc(cty.List(cty.DynamicPseudoType)),
			"tomap":            funcs.MakeToFunc(cty.Map(cty.DynamicPseudoType)),
			"transpose":        funcs.TransposeFunc,
			"trim":             funcs.TrimFunc,
			"trimprefix":       funcs.TrimPrefixFunc,
			"trimspace":        funcs.TrimSpaceFunc,
			"trimsuffix":       funcs.TrimSuffixFunc,
			"try":              tryfunc.TryFunc,
			"upper":            stdlib.UpperFunc,
			"urlencode":        funcs.URLEncodeFunc,
			"uuid":             funcs.UUIDFunc,
			"uuidv5":           funcs.UUIDV5Func,
			"values":           funcs.ValuesFunc,
			"yamldecode":       ctyyaml.YAMLDecodeFunc,
			"yamlencode":       ctyyaml.YAMLEncodeFunc,
			"zipmap":           funcs.ZipmapFunc,
		}

		pathDocsFuncs["templatefile"] = funcs.MakeTemplateFileFunc(baseDir, func() map[string]function.Function {
			return pathDocsFuncs
		})
	}
	pathDocsFuncsLock.Unlock()
	return pathDocsFuncs
}
