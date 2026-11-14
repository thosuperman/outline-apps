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
	"encoding/binary"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// attemptExchanger answers attempt i (0-based) with answers[i]. A nil answer never responds.
type attemptExchanger struct {
	calls   atomic.Int32
	answers []func(query []byte) ([]byte, error)
}

func (a *attemptExchanger) Exchange(ctx context.Context, query []byte) ([]byte, error) {
	i := int(a.calls.Add(1)) - 1
	if i >= len(a.answers) || a.answers[i] == nil {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return a.answers[i](query)
}

func ok(q []byte) ([]byte, error) { return echo(q, 0xee), nil }

func TestRetry_FirstAttemptAnswered(t *testing.T) {
	base := &attemptExchanger{answers: []func([]byte) ([]byte, error){ok}}
	ex := NewRetryExchanger(base, RetryOptions{Interval: time.Hour})
	q := wireQuery(t, wireQueryOpts{id: 9})
	resp, err := ex.Exchange(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, echo(q, 0xee), resp)
	require.Equal(t, int32(1), base.calls.Load())
}

func TestRetry_UnansweredAttemptIsRetriedAfterInterval(t *testing.T) {
	base := &attemptExchanger{answers: []func([]byte) ([]byte, error){nil, ok}}
	ex := NewRetryExchanger(base, RetryOptions{Interval: 30 * time.Millisecond})
	start := time.Now()
	resp, err := ex.Exchange(context.Background(), wireQuery(t, wireQueryOpts{id: 9}))
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.GreaterOrEqual(t, time.Since(start), 30*time.Millisecond)
	require.Equal(t, int32(2), base.calls.Load())
}

func TestRetry_LateAnswerToEarlierAttemptIsAccepted(t *testing.T) {
	first := make(chan struct{})
	base := &attemptExchanger{answers: []func([]byte) ([]byte, error){
		func(q []byte) ([]byte, error) { <-first; return echo(q, 1), nil },
		nil, nil,
	}}
	ex := NewRetryExchanger(base, RetryOptions{Interval: 20 * time.Millisecond, Attempts: 3})
	go func() { time.Sleep(50 * time.Millisecond); close(first) }()
	q := wireQuery(t, wireQueryOpts{id: 9})
	resp, err := ex.Exchange(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, echo(q, 1), resp, "the first attempt answered late, and it still counts")
}

func TestRetry_AttemptsAreBounded(t *testing.T) {
	base := &attemptExchanger{} // Never answers.
	ex := NewRetryExchanger(base, RetryOptions{Interval: 5 * time.Millisecond, Attempts: 3})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := ex.Exchange(ctx, wireQuery(t, wireQueryOpts{id: 9}))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, int32(3), base.calls.Load())
}

func TestRetry_FailedAttemptStartsTheNextOneAtOnce(t *testing.T) {
	fail := func([]byte) ([]byte, error) { return nil, errors.New("send failed") }
	base := &attemptExchanger{answers: []func([]byte) ([]byte, error){fail, fail, ok}}
	ex := NewRetryExchanger(base, RetryOptions{Interval: time.Hour, Attempts: 3})
	start := time.Now()
	_, err := ex.Exchange(context.Background(), wireQuery(t, wireQueryOpts{id: 9}))
	require.NoError(t, err)
	require.Less(t, time.Since(start), time.Second)
}

func TestRetry_AllAttemptsFail(t *testing.T) {
	fail := func([]byte) ([]byte, error) { return nil, errors.New("send failed") }
	base := &attemptExchanger{answers: []func([]byte) ([]byte, error){fail, fail, fail, ok}}
	ex := NewRetryExchanger(base, RetryOptions{Interval: time.Hour, Attempts: 3})
	_, err := ex.Exchange(context.Background(), wireQuery(t, wireQueryOpts{id: 9}))
	require.ErrorContains(t, err, "send failed")
	require.Equal(t, int32(3), base.calls.Load())
}

// A forged or mismatched datagram must not end the exchange: it counts as a failed attempt.
func TestRetry_InvalidResponseIsAFailedAttempt(t *testing.T) {
	forged := func(q []byte) ([]byte, error) {
		r := echo(q, 0)
		binary.BigEndian.PutUint16(r, binary.BigEndian.Uint16(q)^0xffff)
		return r, nil
	}
	base := &attemptExchanger{answers: []func([]byte) ([]byte, error){forged, ok}}
	ex := NewRetryExchanger(base, RetryOptions{Interval: time.Hour})
	q := wireQuery(t, wireQueryOpts{id: 9})
	resp, err := ex.Exchange(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, echo(q, 0xee), resp)
}

func TestRetry_CancellationStopsAttempts(t *testing.T) {
	base := &attemptExchanger{}
	ex := NewRetryExchanger(base, RetryOptions{Interval: 5 * time.Millisecond, Attempts: 3})
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, err := ex.Exchange(ctx, wireQuery(t, wireQueryOpts{id: 9}))
	require.ErrorIs(t, err, context.Canceled)
	// Eventually runs the condition on a goroutine of its own.
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+1 }, time.Second, 5*time.Millisecond)
}

func TestRetry_UnparseableQueryIsTriedOnce(t *testing.T) {
	base := &attemptExchanger{answers: []func([]byte) ([]byte, error){
		func([]byte) ([]byte, error) { return nil, errors.New("bad") },
	}}
	ex := NewRetryExchanger(base, RetryOptions{Interval: time.Millisecond})
	_, err := ex.Exchange(context.Background(), []byte{1, 2})
	require.Error(t, err)
	require.Equal(t, int32(1), base.calls.Load())
}
