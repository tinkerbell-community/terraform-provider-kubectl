// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubectl

import "sync"

// cache is a generic type that lazily initializes a value using sync.Once.
// It is used to cache expensive-to-create clients and other resources.
type cache[T any] struct {
	once  sync.Once
	value T
	err   error
}

// Get returns the cached value, initializing it on first call using the provided function.
func (c *cache[T]) Get(f func() (T, error)) (T, error) {
	c.once.Do(func() {
		c.value, c.err = f()
	})
	return c.value, c.err
}
