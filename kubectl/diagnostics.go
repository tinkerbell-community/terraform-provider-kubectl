// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// APIStatusErrorToDiagnostics converts a Kubernetes API machinery StatusError into Terraform Diagnostics.
func APIStatusErrorToDiagnostics(s metav1.Status) diag.Diagnostics {
	var diags diag.Diagnostics
	diags.AddError(
		"API response status: "+s.Status,
		s.Message,
	)
	if s.Details == nil {
		return diags
	}
	gk := metav1.GroupKind{Group: s.Details.Group, Kind: s.Details.Kind}
	diags.AddError(
		fmt.Sprintf(
			"Kubernetes API Error: %s %s [%s]",
			string(s.Reason),
			gk.String(),
			s.Details.Name,
		),
		"",
	)
	for _, c := range s.Details.Causes {
		diags.AddError(c.Field, c.Message)
	}
	return diags
}
