// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"github.com/alekc/terraform-provider-kubectl/kubectl/payload"
	"github.com/hashicorp/go-hclog"
	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/zclconf/go-cty/cty"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/kubectl/pkg/polymorphichelpers"
)

const waiterSleepTime = 1 * time.Second

// Waiter is a simple interface to implement a blocking wait operation.
type Waiter interface {
	Wait(context.Context) error
}

// WaiterError represents a timeout error while waiting for a condition.
type WaiterError struct {
	Reason string
}

func (e WaiterError) Error() string {
	return fmt.Sprintf("timed out waiting on %v", e.Reason)
}

// NewResourceWaiterFromConfig constructs an appropriate Waiter from framework wait model values.
// This adapts the raw provider's NewResourceWaiter for use with the framework resource.
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
			resource:     resource,
			resourceName: resourceName,
			logger:       logger,
		}
	}

	if len(conditions) > 0 {
		return &ConditionsWaiterV2{
			resource:     resource,
			resourceName: resourceName,
			conditions:   conditions,
			logger:       logger,
		}
	}

	if len(fieldMatchers) > 0 {
		return &FieldWaiter{
			resource:      resource,
			resourceName:  resourceName,
			resourceType:  resourceType,
			typeHints:     typeHints,
			fieldMatchers: fieldMatchers,
			logger:        logger,
		}
	}

	return &NoopWaiter{}
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

// FieldWaiter will wait for a set of fields to be set,
// or have a particular value.
type FieldWaiter struct {
	resource      dynamic.ResourceInterface
	resourceName  string
	resourceType  tftypes.Type
	typeHints     map[string]string
	fieldMatchers []FieldMatcher
	logger        hclog.Logger
}

// Wait blocks until all of the FieldMatchers configured evaluate to true.
func (w *FieldWaiter) Wait(ctx context.Context) error {
	w.logger.Info("[Wait] Waiting until fields match...\n")
	for {
		if deadline, ok := ctx.Deadline(); ok {
			if time.Now().After(deadline) {
				return WaiterError{Reason: "field matchers"}
			}
		}

		res, err := w.resource.Get(ctx, w.resourceName, v1.GetOptions{})
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

		w.logger.Trace("[Wait]", "API Response", resObj)

		obj, err := payload.ToTFValue(
			resObj,
			w.resourceType,
			w.typeHints,
			tftypes.NewAttributePath(),
		)
		if err != nil {
			return err
		}

		done, err := func(obj tftypes.Value) (bool, error) {
			for _, m := range w.fieldMatchers {
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
					v.As(&s)
				case v.Type().Is(tftypes.Bool):
					var vb bool
					v.As(&vb)
					s = fmt.Sprintf("%t", vb)
				case v.Type().Is(tftypes.Number):
					var f big.Float
					v.As(&f)
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
			w.logger.Info("[Wait] Done waiting.\n")
			return err
		}

		time.Sleep(waiterSleepTime)
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
	resource     dynamic.ResourceInterface
	resourceName string
	logger       hclog.Logger
}

// Wait uses StatusViewer to determine if the rollout is done.
func (w *RolloutWaiter) Wait(ctx context.Context) error {
	w.logger.Info("[Wait] Waiting until rollout complete...\n")
	for {
		if deadline, ok := ctx.Deadline(); ok {
			if time.Now().After(deadline) {
				return WaiterError{Reason: "rollout to complete"}
			}
		}

		res, err := w.resource.Get(ctx, w.resourceName, v1.GetOptions{})
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

		time.Sleep(waiterSleepTime)
	}

	w.logger.Info("[Wait] Rollout complete\n")
	return nil
}

// ConditionsWaiterV2 will wait for the specified conditions on
// the resource to be met, using ConditionMatcher values.
type ConditionsWaiterV2 struct {
	resource     dynamic.ResourceInterface
	resourceName string
	conditions   []ConditionMatcher
	logger       hclog.Logger
}

// Wait checks all the configured conditions have been met.
func (w *ConditionsWaiterV2) Wait(ctx context.Context) error {
	w.logger.Info("[Wait] Waiting for conditions...\n")

	for {
		if deadline, ok := ctx.Deadline(); ok {
			if time.Now().After(deadline) {
				return WaiterError{Reason: "conditions"}
			}
		}

		res, err := w.resource.Get(ctx, w.resourceName, v1.GetOptions{})
		if err != nil {
			return err
		}
		if errors.IsGone(err) {
			return fmt.Errorf("resource was deleted")
		}

		if status, ok := res.Object["status"].(map[string]any); ok {
			if conditions, ok := status["conditions"].([]any); ok && len(conditions) > 0 {
				conditionsMet := true
				for _, c := range w.conditions {
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
		time.Sleep(waiterSleepTime)
	}

	w.logger.Info("[Wait] All conditions met.\n")
	return nil
}
