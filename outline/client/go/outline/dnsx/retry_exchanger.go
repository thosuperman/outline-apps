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
	"context"
	"errors"
	"golang.getoutline.org/sdk/dns"
	"time"
)

// RetryOptions configures [NewRetryExchanger]. Zero values select the defaults.
type RetryOptions struct {
	// Attempts is the maximum number of exchanges on base for one query. Defaults to 3.
	Attempts int
	// Interval is how long an attempt runs before the next one starts. Defaults to 1 second.
	Interval time.Duration
}

// NewRetryExchanger returns an [dns.Exchanger] that retries unanswered queries on base, up to a
// bounded number of attempts.
//
// Attempts overlap: when an attempt has not been answered after Interval, the next one starts
// and the earlier ones keep waiting, so a late response to any of them is still accepted. An
// attempt that fails starts the next one at once. The first valid response wins and cancels
// the other attempts. Every attempt is a separate call to base: with a datagram transport such
// as [NewUDPExchanger], each uses its own unpredictable source port, as [RFC 5452, Section 9.2]
// requires.
//
// Because it must tell an answer from a failure, the returned dns.Exchanger validates responses
// against the query (ID, then question, as per [RFC 5452, Section 9.1]) and treats a response
// that doesn't match as a failed attempt. A query that cannot be parsed is passed to base once,
// without retries.
//
// The total time is bounded by ctx, which should carry a deadline.
//
// [RFC 5452, Section 9.1]: https://datatracker.ietf.org/doc/html/rfc5452#section-9.1
// [RFC 5452, Section 9.2]: https://datatracker.ietf.org/doc/html/rfc5452#section-9.2
func NewRetryExchanger(base dns.Exchanger, opts RetryOptions) dns.Exchanger {
	if opts.Attempts <= 0 {
		opts.Attempts = 3
	}
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	return dns.FuncExchanger(func(ctx context.Context, query []byte) ([]byte, error) {
		info, err := parseQuery(query)
		if err != nil {
			return base.Exchange(ctx, query)
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel() // Stops the attempts still running.

		type result struct {
			response []byte
			err      error
		}
		// Buffered for every attempt, so that no attempt blocks after we return.
		results := make(chan result, opts.Attempts)
		started, failed := 0, 0
		start := func() {
			started++
			go func() {
				response, err := base.Exchange(ctx, query)
				if err == nil {
					if checkErr := checkRawResponse(response, info); checkErr != nil {
						err = &nestedError{dns.ErrBadResponse, checkErr}
					}
				}
				results <- result{response, err}
			}()
		}

		start()
		timer := time.NewTimer(opts.Interval)
		defer timer.Stop()
		var errs error
		for {
			select {
			case r := <-results:
				if r.err == nil {
					return r.response, nil
				}
				failed++
				errs = errors.Join(errs, r.err)
				if failed < started {
					continue // Another attempt may still succeed.
				}
				if started == opts.Attempts {
					return nil, errs
				}
				start()
				timer.Reset(opts.Interval)
			case <-timer.C:
				if started < opts.Attempts {
					start()
					timer.Reset(opts.Interval)
				}
			case <-ctx.Done():
				return nil, errors.Join(ctx.Err(), errs)
			}
		}
	})
}
