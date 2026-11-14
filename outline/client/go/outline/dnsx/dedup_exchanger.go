// Copyright 2026 The Outline Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dnsx

import (
	"bytes"
	"context"
	"encoding/binary"
	"golang.getoutline.org/sdk/dns"
	"sync"
	"time"
)

// DedupOptions configures [NewDedupExchanger]. Zero values select the defaults.
type DedupOptions struct {
	// Timeout bounds each shared exchange. It is needed because the exchange outlives the
	// caller that started it. Defaults to 5 seconds.
	Timeout time.Duration
	// MaxInFlight bounds the number of distinct queries being exchanged at once, and so the
	// number of goroutines. Queries beyond it are passed to the base dns.Exchanger unshared.
	// Defaults to 256.
	MaxInFlight int
}

// NewDedupExchanger returns an [dns.Exchanger] that shares one exchange on base among concurrent
// queries that are identical in every byte except the message ID.
//
// A stub resolver retransmits a query that goes unanswered, and several clients often ask
// the same question at once. Relayed one by one, each copy becomes a separate exchange: more
// traffic, and more outstanding queries for the same question, which [RFC 5452, Section 5]
// identifies as what makes birthday attacks practical. Sharing the exchange avoids both.
//
// The comparison is on the whole wire format but the ID, so queries that differ in anything
// that can change the answer or its acceptance (header flags such as RD and CD, EDNS(0)
// options such as DO, the buffer size or cookies, the case of the name, the question) are
// never shared. There is no caching: a query that arrives after the shared exchange finished
// starts a new one.
//
// The shared exchange sends the query of the first caller. Because the response is handed to
// callers whose IDs differ, it must be validated here rather than by the caller: a response
// that doesn't match the query sent (ID, then question, as per [RFC 5452, Section 9.1]) fails
// the exchange instead of being delivered. Each caller receives a copy of the response with its
// own ID.
//
// A caller whose ctx is done stops waiting. The shared exchange is canceled when no caller is
// waiting for it any longer.
//
// [RFC 5452, Section 5]: https://datatracker.ietf.org/doc/html/rfc5452#section-5
// [RFC 5452, Section 9.1]: https://datatracker.ietf.org/doc/html/rfc5452#section-9.1
func NewDedupExchanger(base dns.Exchanger, opts DedupOptions) dns.Exchanger {
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.MaxInFlight <= 0 {
		opts.MaxInFlight = 256
	}
	return &dedupExchanger{base: base, opts: opts, calls: make(map[string]*sharedExchange)}
}

type dedupExchanger struct {
	base dns.Exchanger
	opts DedupOptions

	mu    sync.Mutex
	calls map[string]*sharedExchange // By query without its ID.
}

// sharedExchange is an exchange in flight and the callers waiting for it.
type sharedExchange struct {
	key      string
	done     chan struct{} // Closed when response and err are set.
	response []byte
	err      error

	waiters int // Protected by dedupExchanger.mu.
	cancel  context.CancelFunc
}

// Exchange implements [dns.Exchanger].
func (d *dedupExchanger) Exchange(ctx context.Context, query []byte) ([]byte, error) {
	info, err := parseQuery(query)
	if err != nil {
		// We could not validate the response, so we can't share it. Let base decide.
		return d.base.Exchange(ctx, query)
	}
	key := string(query[2:])

	d.mu.Lock()
	call, ok := d.calls[key]
	if !ok {
		if len(d.calls) >= d.opts.MaxInFlight {
			d.mu.Unlock()
			return d.base.Exchange(ctx, query)
		}
		exchangeCtx, cancel := context.WithTimeout(context.Background(), d.opts.Timeout)
		call = &sharedExchange{key: key, done: make(chan struct{}), cancel: cancel}
		d.calls[key] = call
		go d.run(exchangeCtx, call, bytes.Clone(query), info)
	}
	call.waiters++
	d.mu.Unlock()

	select {
	case <-call.done:
	case <-ctx.Done():
	}
	d.leave(call)

	select {
	case <-call.done:
	default:
		return nil, ctx.Err()
	}
	if call.err != nil {
		return nil, call.err
	}
	response := bytes.Clone(call.response)
	binary.BigEndian.PutUint16(response, info.id)
	return response, nil
}

// run performs the shared exchange of query, which is the query of the first caller.
func (d *dedupExchanger) run(ctx context.Context, call *sharedExchange, query []byte, info queryInfo) {
	defer call.cancel()
	response, err := d.base.Exchange(ctx, query)
	if err == nil {
		if checkErr := checkRawResponse(response, info); checkErr != nil {
			err = &nestedError{dns.ErrBadResponse, checkErr}
		}
	}

	d.mu.Lock()
	if d.calls[call.key] == call {
		delete(d.calls, call.key)
	}
	d.mu.Unlock()

	call.response, call.err = response, err
	close(call.done)
}

// leave unregisters a waiter. If it was the last one, it cancels the exchange, and forgets it so
// that a new caller doesn't join an exchange that is being canceled.
func (d *dedupExchanger) leave(call *sharedExchange) {
	d.mu.Lock()
	call.waiters--
	last := call.waiters == 0
	if last && d.calls[call.key] == call {
		delete(d.calls, call.key)
	}
	d.mu.Unlock()
	if last {
		call.cancel()
	}
}
