// Copyright © 2017 Microsoft <wastore@microsoft.com>
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package common

import (
	"math/rand"
	"sync/atomic"
	"context"
	"time"
)

type Predicate func() bool

// Used to limit the amount of in-flight data in RAM, to keep it an an acceptable level.
// For downloads, network is producer and disk is consumer, while for uploads the roles are reversed.
// In either case, if the producer is faster than the consumer, this CacheLimiter is necessary
// prevent unbounded RAM usage
type CacheLimiter interface {
	TryAddBytes(count int64, useRelaxedLimit bool ) (added bool)
	WaitUntilAddBytes(ctx context.Context, count int64, useRelaxedLimit Predicate) error
	RemoveBytes(count int64 )
}

type cacheLimiter struct {
	value int64
	limit int64
}

func NewCacheLimiter(limit int64) CacheLimiter {
	return &cacheLimiter{limit: limit}
}

// TryAdd tries to add a memory allocation within the limit.  Returns true if it could be (and was) added
func (c *cacheLimiter) TryAddBytes(count int64, useRelaxedLimit bool) (added bool) {
	lim := c.limit

	// Above the "strict" limit, there's a bit of extra room, which we use
	// for high-priority things (i.e. things we deem to be allowable under a relaxed (non-strict) limit)
	strict := !useRelaxedLimit
	if strict {
		lim = int64(float32(lim)  * 0.75)
	}

	if atomic.AddInt64(&c.value, count) <= lim {
		return true
	}
	// else, we are over the limit, so immediately subtract back what we've added, and return false
	atomic.AddInt64(&c.value, -count)
	return false
}

/// WaitToAdd blocks until it completes a successful call to TryAdd
func (c *cacheLimiter) WaitUntilAddBytes(ctx context.Context, count int64, useRelaxedLimit Predicate) error {
	for {
		// Proceed if there's room in the cache
		if c.TryAddBytes(count, useRelaxedLimit()) {
			return nil
		}

		// else wait and repeat
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(2 * float32(time.Second) * rand.Float32())):
			// Duration of delay is somewhat arbitrary. Don't want to use anything very tiny (e.g. milliseconds) because that
			// just adds CPU load for no real benefit.  Is this value too big?  Probably not, because even at 10 Gbps,
			// it would take longer than this to fill or drain our full memory allocation.

			// Nothing to do, just loop around again
			// The wait is randomized to prevent the establishment of repetitive oscillations in cache size
			// Average wait is quite long (2 seconds) since context where we're using this does not require any timing more fine-grained
		}
	}
}

func (c *cacheLimiter) RemoveBytes(count int64) {
	negativeDelta := -count
	atomic.AddInt64(&c.value, negativeDelta)
}