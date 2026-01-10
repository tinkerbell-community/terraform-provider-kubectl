// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package logging_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/alekc/terraform-provider-kubectl/kubectl/test/logging"
	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/terraform-plugin-log/tfsdklogtest"
)

func TestHelperSchemaDebug(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	ctx := tfsdklogtest.RootLogger(context.Background(), &output)
	ctx = logging.InitContext(ctx)

	logging.HelperSchemaDebug(ctx, "test message")

	entries, err := tfsdklogtest.MultilineJSONDecode(&output)
	if err != nil {
		t.Fatalf("unable to read multiple line JSON: %s", err)
	}

	expectedEntries := []map[string]any{
		{
			"@level":   "debug",
			"@message": "test message",
			"@module":  "sdk.helper_schema",
		},
	}

	if diff := cmp.Diff(entries, expectedEntries); diff != "" {
		t.Errorf("unexpected difference: %s", diff)
	}
}

func TestHelperSchemaError(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	ctx := tfsdklogtest.RootLogger(context.Background(), &output)
	ctx = logging.InitContext(ctx)

	logging.HelperSchemaError(ctx, "test message")

	entries, err := tfsdklogtest.MultilineJSONDecode(&output)
	if err != nil {
		t.Fatalf("unable to read multiple line JSON: %s", err)
	}

	expectedEntries := []map[string]any{
		{
			"@level":   "error",
			"@message": "test message",
			"@module":  "sdk.helper_schema",
		},
	}

	if diff := cmp.Diff(entries, expectedEntries); diff != "" {
		t.Errorf("unexpected difference: %s", diff)
	}
}

func TestHelperSchemaTrace(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	ctx := tfsdklogtest.RootLogger(context.Background(), &output)
	ctx = logging.InitContext(ctx)

	logging.HelperSchemaTrace(ctx, "test message")

	entries, err := tfsdklogtest.MultilineJSONDecode(&output)
	if err != nil {
		t.Fatalf("unable to read multiple line JSON: %s", err)
	}

	expectedEntries := []map[string]any{
		{
			"@level":   "trace",
			"@message": "test message",
			"@module":  "sdk.helper_schema",
		},
	}

	if diff := cmp.Diff(entries, expectedEntries); diff != "" {
		t.Errorf("unexpected difference: %s", diff)
	}
}

func TestHelperSchemaWarn(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	ctx := tfsdklogtest.RootLogger(context.Background(), &output)
	ctx = logging.InitContext(ctx)

	logging.HelperSchemaWarn(ctx, "test message")

	entries, err := tfsdklogtest.MultilineJSONDecode(&output)
	if err != nil {
		t.Fatalf("unable to read multiple line JSON: %s", err)
	}

	expectedEntries := []map[string]any{
		{
			"@level":   "warn",
			"@message": "test message",
			"@module":  "sdk.helper_schema",
		},
	}

	if diff := cmp.Diff(entries, expectedEntries); diff != "" {
		t.Errorf("unexpected difference: %s", diff)
	}
}
