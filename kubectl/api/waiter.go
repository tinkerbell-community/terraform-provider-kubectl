// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//nolint:forcetypeassert
package api

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"github.com/hashicorp/go-hclog"
	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/tinkerbell-community/terraform-provider-kubectl/kubectl/payload"
	"github.com/zclconf/go-cty/cty"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/kubectl/pkg/polymorphichelpers"
)

// WaiterSleepTime is the default sleep interval between status checks.
const WaiterSleepTime = 1 * time.Second

// Waiter is a simple interface to implement a blocking wait operation.
type Waiter interface {
	Wait(context.Context) error
}

// WaiterError represents a timeout error while waiting for a condition.
type WaiterError struct {
	Reason string
}

// Error implements the error interface.
func (e WaiterError) Error() string {
	return fmt.Sprintf("timed out waiting on %v", e.Reason)
}

// FieldMatcher contains a tftypes.AttributePath to a field and a regexp to match on it.
type FieldMatcher struct {
	Path         *tftypes.AttributePath
	ValueMatcher *regexp.Regexp
}

// ConditionMatcher describes a condition type/status pair to wait for.
type ConditionMatcher struct {
	Type   string
	Status string
}

// NewResourceWaiterFromConfig constructs an appropriate Waiter from framework wait model values.
// This is used by the terraform-plugin-framework resource implementation.
func NewResourceWaiterFromConfig(
	resource dynamic.ResourceInterface,
	resourceName string,
	resourceType tftypes.Type,
	typeHints map[string]string,
	rollout bool,
	fieldMatchers []FieldMatcher,
	conditions []ConditionMatcher,
	logger hclog.Logger,
) Waiter {
	if rollout {
		return &RolloutWaiter{
			Resource:     resource,
			ResourceName: resourceName,
			Logger:       logger,
		}
	}

	if len(conditions) > 0 {
		return &ConditionsWaiterV2{
			Resource:     resource,
			ResourceName: resourceName,
			Conditions:   conditions,
			Logger:       logger,
		}
	}

	if len(fieldMatchers) > 0 {
		return &FieldWaiter{
			Resource:      resource,
			ResourceName:  resourceName,
			ResourceType:  resourceType,
			TypeHints:     typeHints,
			FieldMatchers: fieldMatchers,
			Logger:        logger,
		}
	}

	return &NoopWaiter{}
}

// NewResourceWaiter constructs an appropriate Waiter using the supplied waitForBlock
// configuration from a tftypes.Value. This is used by the raw tfprotov6 provider.
func NewResourceWaiter(
	resource dynamic.ResourceInterface,
	resourceName string,
	resourceType tftypes.Type,
	th map[string]string,
	waitForBlock tftypes.Value,
	hl hclog.Logger,
) (Waiter, error) {
	var waitForBlockVal map[string]tftypes.Value
	err := waitForBlock.As(&waitForBlockVal)
	if err != nil {
		return nil, err
	}

	if v, ok := waitForBlockVal["rollout"]; ok {
		var rollout bool
		_ = v.As(&rollout)
		if rollout {
			return &RolloutWaiter{
				Resource:     resource,
				ResourceName: resourceName,
				Logger:       hl,
			}, nil
		}
	}

	if v, ok := waitForBlockVal["condition"]; ok {
		var conditionsBlocks []tftypes.Value
		_ = v.As(&conditionsBlocks)
		if len(conditionsBlocks) > 0 {
			return &ConditionsWaiter{
				Resource:     resource,
				ResourceName: resourceName,
				Conditions:   conditionsBlocks,
				Logger:       hl,
			}, nil
		}
	}

	fields, ok := waitForBlockVal["fields"]
	if !ok || fields.IsNull() || !fields.IsKnown() {
		return &NoopWaiter{}, nil
	}

	if !fields.Type().Is(tftypes.Map{}) {
		return nil, fmt.Errorf(`"fields" should be a map of strings`)
	}

	var vm map[string]tftypes.Value
	_ = fields.As(&vm)
	var matchers []FieldMatcher

	for k, v := range vm {
		var expr string
		_ = v.As(&expr)
		var re *regexp.Regexp
		if expr == "*" {
			re = regexp.MustCompile("(.*)?")
		} else {
			var err error
			re, err = regexp.Compile(expr)
			if err != nil {
				return nil, fmt.Errorf("invalid regular expression: %q", expr)
			}
		}

		p, err := FieldPathToTftypesPath(k)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, FieldMatcher{Path: p, ValueMatcher: re})
	}

	return &FieldWaiter{
		Resource:      resource,
		ResourceName:  resourceName,
		ResourceType:  resourceType,
		TypeHints:     th,
		FieldMatchers: matchers,
		Logger:        hl,
	}, nil
}

// FieldWaiter will wait for a set of fields to be set,
// or have a particular value.
type FieldWaiter struct {
	Resource      dynamic.ResourceInterface
	ResourceName  string
	ResourceType  tftypes.Type
	TypeHints     map[string]string
	FieldMatchers []FieldMatcher
	Logger        hclog.Logger
}

// Wait blocks until all of the FieldMatchers configured evaluate to true.
func (w *FieldWaiter) Wait(ctx context.Context) error {
	w.Logger.Info("[Wait] Waiting until fields match...\n")
	for {
		if deadline, ok := ctx.Deadline(); ok {
			if time.Now().After(deadline) {
				return WaiterError{Reason: "field matchers"}
			}
		}

		res, err := w.Resource.Get(ctx, w.ResourceName, v1.GetOptions{})
		if err != nil {
			return err
		}
		if errors.IsGone(err) {
			return fmt.Errorf("resource was deleted")
		}
		resObj := res.Object
		if meta, ok := resObj["metadata"].(map[string]any); ok {
			delete(meta, "managedFields")
		}

		w.Logger.Trace("[Wait]", "API Response", resObj)

		obj, err := payload.ToTFValue(
			resObj,
			w.ResourceType,
			w.TypeHints,
			tftypes.NewAttributePath(),
		)
		if err != nil {
			return err
		}

		done, err := func(obj tftypes.Value) (bool, error) {
			for _, m := range w.FieldMatchers {
				vi, rp, err := tftypes.WalkAttributePath(obj, m.Path)
				if err != nil {
					return false, err
				}
				if len(rp.Steps()) > 0 {
					return false, fmt.Errorf("attribute not present at path '%s'", m.Path.String())
				}

				var s string
				v := vi.(tftypes.Value)
				switch {
				case v.Type().Is(tftypes.String):
					_ = v.As(&s)
				case v.Type().Is(tftypes.Bool):
					var vb bool
					_ = v.As(&vb)
					s = fmt.Sprintf("%t", vb)
				case v.Type().Is(tftypes.Number):
					var f big.Float
					_ = v.As(&f)
					if f.IsInt() {
						i, _ := f.Int64()
						s = fmt.Sprintf("%d", i)
					} else {
						i, _ := f.Float64()
						s = fmt.Sprintf("%f", i)
					}
				default:
					return true, fmt.Errorf("wait_for: cannot match on type %q", v.Type().String())
				}

				if !m.ValueMatcher.Match([]byte(s)) {
					return false, nil
				}
			}

			return true, nil
		}(obj)

		if done {
			w.Logger.Info("[Wait] Done waiting.\n")
			return err
		}

		time.Sleep(WaiterSleepTime)
	}
}

// NoopWaiter is a placeholder for when there is nothing to wait on.
type NoopWaiter struct{}

// Wait returns immediately.
func (w *NoopWaiter) Wait(_ context.Context) error {
	return nil
}

// FieldPathToTftypesPath takes a string representation of
// a path to a field in dot/square bracket notation
// and returns a tftypes.AttributePath.
func FieldPathToTftypesPath(fieldPath string) (*tftypes.AttributePath, error) {
	t, d := hclsyntax.ParseTraversalAbs([]byte(fieldPath), "", hcl.Pos{Line: 1, Column: 1})
	if d.HasErrors() {
		return tftypes.NewAttributePath(), fmt.Errorf(
			"invalid field path %q: %s",
			fieldPath,
			d.Error(),
		)
	}

	path := tftypes.NewAttributePath()
	for _, p := range t {
		switch t := p.(type) {
		case hcl.TraverseRoot:
			path = path.WithAttributeName(t.Name)
		case hcl.TraverseIndex:
			indexKey := p.(hcl.TraverseIndex).Key
			indexKeyType := indexKey.Type()
			if indexKeyType.Equals(cty.String) {
				path = path.WithElementKeyString(indexKey.AsString())
			} else if indexKeyType.Equals(cty.Number) {
				f := indexKey.AsBigFloat()
				if f.IsInt() {
					i, _ := f.Int64()
					path = path.WithElementKeyInt(int(i))
				} else {
					return tftypes.NewAttributePath(), fmt.Errorf(
						"index in field path must be an integer",
					)
				}
			} else {
				return tftypes.NewAttributePath(), fmt.Errorf(
					"unsupported type in field path: %s",
					indexKeyType.FriendlyName(),
				)
			}
		case hcl.TraverseAttr:
			path = path.WithAttributeName(t.Name)
		case hcl.TraverseSplat:
			return tftypes.NewAttributePath(), fmt.Errorf("splat is not supported")
		}
	}

	return path, nil
}

// RolloutWaiter will wait for a resource that has a StatusViewer to
// finish rolling out.
type RolloutWaiter struct {
	Resource     dynamic.ResourceInterface
	ResourceName string
	Logger       hclog.Logger
}

// Wait uses StatusViewer to determine if the rollout is done.
func (w *RolloutWaiter) Wait(ctx context.Context) error {
	w.Logger.Info("[Wait] Waiting until rollout complete...\n")
	for {
		if deadline, ok := ctx.Deadline(); ok {
			if time.Now().After(deadline) {
				return WaiterError{Reason: "rollout to complete"}
			}
		}

		res, err := w.Resource.Get(ctx, w.ResourceName, v1.GetOptions{})
		if err != nil {
			return err
		}
		if errors.IsGone(err) {
			return fmt.Errorf("resource was deleted")
		}

		gk := res.GetObjectKind().GroupVersionKind().GroupKind()
		statusViewer, err := polymorphichelpers.StatusViewerFor(gk)
		if err != nil {
			return fmt.Errorf("error getting resource status: %v", err)
		}

		_, done, err := statusViewer.Status(res, 0)
		if err != nil {
			return fmt.Errorf("error getting resource status: %v", err)
		}

		if done {
			break
		}

		time.Sleep(WaiterSleepTime)
	}

	w.Logger.Info("[Wait] Rollout complete\n")
	return nil
}

// ConditionsWaiterV2 will wait for the specified conditions on
// the resource to be met, using ConditionMatcher values.
// Used by the terraform-plugin-framework resource.
type ConditionsWaiterV2 struct {
	Resource     dynamic.ResourceInterface
	ResourceName string
	Conditions   []ConditionMatcher
	Logger       hclog.Logger
}

// Wait checks all the configured conditions have been met.
func (w *ConditionsWaiterV2) Wait(ctx context.Context) error {
	w.Logger.Info("[Wait] Waiting for conditions...\n")

	for {
		if deadline, ok := ctx.Deadline(); ok {
			if time.Now().After(deadline) {
				return WaiterError{Reason: "conditions"}
			}
		}

		res, err := w.Resource.Get(ctx, w.ResourceName, v1.GetOptions{})
		if err != nil {
			return err
		}
		if errors.IsGone(err) {
			return fmt.Errorf("resource was deleted")
		}

		if status, ok := res.Object["status"].(map[string]any); ok {
			if conditions, ok := status["conditions"].([]any); ok && len(conditions) > 0 {
				conditionsMet := true
				for _, c := range w.Conditions {
					conditionMet := false
					for _, cc := range conditions {
						ccc, ok := cc.(map[string]any)
						if !ok {
							continue
						}
						if ccc["type"].(string) == c.Type {
							conditionMet = ccc["status"].(string) == c.Status
							break
						}
					}
					conditionsMet = conditionsMet && conditionMet
				}
				if conditionsMet {
					break
				}
			}
		}
		time.Sleep(WaiterSleepTime)
	}

	w.Logger.Info("[Wait] All conditions met.\n")
	return nil
}

// ConditionsWaiter will wait for the specified conditions on
// the resource to be met, using tftypes.Value condition blocks.
// Used by the raw tfprotov6 provider.
type ConditionsWaiter struct {
	Resource     dynamic.ResourceInterface
	ResourceName string
	Conditions   []tftypes.Value
	Logger       hclog.Logger
}

// Wait checks all the configured conditions have been met.
func (w *ConditionsWaiter) Wait(ctx context.Context) error {
	w.Logger.Info("[Wait] Waiting for conditions...\n")

	for {
		if deadline, ok := ctx.Deadline(); ok {
			if time.Now().After(deadline) {
				return WaiterError{Reason: "conditions"}
			}
		}

		res, err := w.Resource.Get(ctx, w.ResourceName, v1.GetOptions{})
		if err != nil {
			return err
		}
		if errors.IsGone(err) {
			return fmt.Errorf("resource was deleted")
		}

		if status, ok := res.Object["status"].(map[string]any); ok {
			if conditions, ok := status["conditions"].([]any); ok && len(conditions) > 0 {
				conditionsMet := true
				for _, c := range w.Conditions {
					var condition map[string]tftypes.Value
					_ = c.As(&condition)
					var conditionType, conditionStatus string
					_ = condition["type"].As(&conditionType)
					_ = condition["status"].As(&conditionStatus)
					conditionMet := false
					for _, cc := range conditions {
						ccc := cc.(map[string]any)
						if ccc["type"].(string) == conditionType {
							conditionMet = ccc["status"].(string) == conditionStatus
							break
						}
					}
					conditionsMet = conditionsMet && conditionMet
				}
				if conditionsMet {
					break
				}
			}
		}
		time.Sleep(WaiterSleepTime)
	}

	w.Logger.Info("[Wait] All conditions met.\n")
	return nil
}
