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
	"fmt"
	"golang.getoutline.org/sdk/dns"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

// wireQuery builds a wire-format query. Options let tests vary single fields.
type wireQueryOpts struct {
	id       uint16
	name     string
	qtype    dnsmessage.Type
	rd, cd   bool
	edns     bool
	ednsSize uint16
	do       bool
	cookie   []byte
}

func wireQuery(t testing.TB, o wireQueryOpts) []byte {
	t.Helper()
	if o.name == "" {
		o.name = "example.com."
	}
	if o.qtype == 0 {
		o.qtype = dnsmessage.TypeA
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: o.id, RecursionDesired: o.rd, CheckingDisabled: o.cd})
	b.EnableCompression()
	require.NoError(t, b.StartQuestions())
	require.NoError(t, b.Question(dnsmessage.Question{
		Name: dnsmessage.MustNewName(o.name), Type: o.qtype, Class: dnsmessage.ClassINET}))
	if o.edns {
		require.NoError(t, b.StartAdditionals())
		var rh dnsmessage.ResourceHeader
		require.NoError(t, rh.SetEDNS0(int(o.ednsSize), dnsmessage.RCodeSuccess, o.do))
		var opts []dnsmessage.Option
		if o.cookie != nil {
			opts = append(opts, dnsmessage.Option{Code: 10, Data: o.cookie})
		}
		require.NoError(t, b.OPTResource(rh, dnsmessage.OPTResource{Options: opts}))
	}
	q, err := b.Finish()
	require.NoError(t, err)
	return q
}

// echo returns a response to query: the query with the QR bit set, plus a marker byte so tests
// can tell responses apart.
func echo(query []byte, marker byte) []byte {
	r := append([]byte(nil), query...)
	r[2] |= 0x80
	return append(r, marker)
}

// fakeExchanger records the queries it gets and answers as told.
type fakeExchanger struct {
	calls   atomic.Int32
	release chan struct{} // If not nil, Exchange waits for it (or ctx) before answering.
	answer  func(query []byte) ([]byte, error)

	mu      sync.Mutex
	queries [][]byte
	ctxErrs []error
}

func (f *fakeExchanger) Exchange(ctx context.Context, query []byte) ([]byte, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.queries = append(f.queries, append([]byte(nil), query...))
	f.mu.Unlock()
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			f.mu.Lock()
			f.ctxErrs = append(f.ctxErrs, ctx.Err())
			f.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	if f.answer != nil {
		return f.answer(query)
	}
	return echo(query, 0xee), nil
}

func idOf(msg []byte) uint16 { return binary.BigEndian.Uint16(msg) }

// exchangeConcurrently runs Exchange for every query at once and waits until base has seen
// `expectCalls` calls before releasing it.
func exchangeConcurrently(t *testing.T, ex dns.Exchanger, base *fakeExchanger, queries [][]byte, expectCalls int32) ([][]byte, []error) {
	t.Helper()
	responses := make([][]byte, len(queries))
	errs := make([]error, len(queries))
	var wg sync.WaitGroup
	var started sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		started.Add(1)
		go func() {
			defer wg.Done()
			started.Done()
			responses[i], errs[i] = ex.Exchange(context.Background(), q)
		}()
	}
	started.Wait()
	require.Eventually(t, func() bool { return base.calls.Load() == expectCalls }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond) // Give stragglers the chance to (wrongly) start more calls.
	close(base.release)
	wg.Wait()
	return responses, errs
}

func TestDedup_IdenticalQueriesShareOneExchangeAndKeepTheirIDs(t *testing.T) {
	base := &fakeExchanger{release: make(chan struct{})}
	ex := NewDedupExchanger(base, DedupOptions{})

	const n = 50
	queries := make([][]byte, n)
	for i := range queries {
		// Same question and options, different IDs: what retransmissions and concurrent clients send.
		queries[i] = wireQuery(t, wireQueryOpts{id: uint16(1000 + i), rd: true, edns: true, ednsSize: 1232})
	}
	responses, errs := exchangeConcurrently(t, ex, base, queries, 1)

	require.Equal(t, int32(1), base.calls.Load(), "identical queries must share one exchange")
	for i := range queries {
		require.NoError(t, errs[i])
		require.Equal(t, idOf(queries[i]), idOf(responses[i]), "each caller must get its own ID")
		require.Equal(t, echo(queries[i], 0xee)[2:], responses[i][2:], "the rest of the response is the shared one")
	}
}

// Queries that differ in anything but the ID can have different answers, or answers the client
// would treat differently, so they must not be shared.
func TestDedup_QueriesThatDifferBeyondTheIDAreNotShared(t *testing.T) {
	variants := []wireQueryOpts{
		{rd: true},
		{rd: false},
		{rd: true, cd: true},
		{rd: true, qtype: dnsmessage.TypeAAAA},
		{rd: true, name: "EXAMPLE.com."}, // 0x20 case: the client checks the case it sent.
		{rd: true, name: "example.org."},
		{rd: true, edns: true, ednsSize: 1232},
		{rd: true, edns: true, ednsSize: 4096},
		{rd: true, edns: true, ednsSize: 1232, do: true},
		{rd: true, edns: true, ednsSize: 1232, cookie: []byte{1, 2, 3, 4, 5, 6, 7, 8}},
		{rd: true, edns: true, ednsSize: 1232, cookie: []byte{8, 7, 6, 5, 4, 3, 2, 1}},
	}
	base := &fakeExchanger{release: make(chan struct{})}
	ex := NewDedupExchanger(base, DedupOptions{})
	queries := make([][]byte, len(variants))
	for i, v := range variants {
		v.id = 7 // Same ID everywhere: only the other fields differ.
		queries[i] = wireQuery(t, v)
	}
	responses, errs := exchangeConcurrently(t, ex, base, queries, int32(len(variants)))
	require.Equal(t, int32(len(variants)), base.calls.Load())
	for i := range queries {
		require.NoError(t, errs[i])
		require.Equal(t, echo(queries[i], 0xee), responses[i], "variant %d got another query's answer", i)
	}
}

func TestDedup_InvalidResponsesAreNotDelivered(t *testing.T) {
	q := wireQuery(t, wireQueryOpts{id: 42, rd: true})
	tests := map[string]func([]byte) []byte{
		"wrong ID": func(q []byte) []byte {
			r := echo(q, 0)
			binary.BigEndian.PutUint16(r, idOf(q)+1)
			return r
		},
		"wrong question": func([]byte) []byte {
			return echo(wireQuery(t, wireQueryOpts{id: 42, rd: true, name: "evil.example."}), 0)
		},
		"not a response": func(q []byte) []byte { return append([]byte(nil), q...) },
		"garbage":        func([]byte) []byte { return []byte{1, 2, 3} },
	}
	for name, forge := range tests {
		t.Run(name, func(t *testing.T) {
			base := &fakeExchanger{answer: func(q []byte) ([]byte, error) { return forge(q), nil }}
			ex := NewDedupExchanger(base, DedupOptions{})
			// A second caller with another ID would get the forged answer with its own ID, so
			// it must be validated here.
			other := append([]byte(nil), q...)
			binary.BigEndian.PutUint16(other, 4242)
			for _, query := range [][]byte{q, other} {
				resp, err := ex.Exchange(context.Background(), query)
				require.ErrorIs(t, err, dns.ErrBadResponse)
				require.Nil(t, resp)
			}
		})
	}
}

func TestDedup_NoCaching(t *testing.T) {
	base := &fakeExchanger{}
	ex := NewDedupExchanger(base, DedupOptions{})
	q := wireQuery(t, wireQueryOpts{id: 1, rd: true})
	for range 3 {
		_, err := ex.Exchange(context.Background(), q)
		require.NoError(t, err)
	}
	require.Equal(t, int32(3), base.calls.Load(), "finished exchanges must not be reused")
}

func TestDedup_CallerCancellation(t *testing.T) {
	base := &fakeExchanger{release: make(chan struct{})}
	ex := NewDedupExchanger(base, DedupOptions{Timeout: time.Minute})
	q := wireQuery(t, wireQueryOpts{id: 1, rd: true})

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	errs := make(chan error, 2)
	go func() { _, err := ex.Exchange(ctx1, q); errs <- err }()
	go func() { _, err := ex.Exchange(ctx2, q); errs <- err }()
	require.Eventually(t, func() bool { return base.calls.Load() == 1 }, time.Second, time.Millisecond)

	// One caller leaves: the exchange goes on for the other.
	cancel1()
	require.ErrorIs(t, <-errs, context.Canceled)
	time.Sleep(20 * time.Millisecond)
	base.mu.Lock()
	require.Empty(t, base.ctxErrs, "the exchange must continue while someone waits")
	base.mu.Unlock()

	// The last caller leaves: the exchange is canceled.
	cancel2()
	require.ErrorIs(t, <-errs, context.Canceled)
	require.Eventually(t, func() bool {
		base.mu.Lock()
		defer base.mu.Unlock()
		return len(base.ctxErrs) == 1
	}, time.Second, time.Millisecond)

	// A new caller starts a new exchange instead of joining the canceled one.
	close(base.release)
	resp, err := ex.Exchange(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, idOf(q), idOf(resp))
	require.Equal(t, int32(2), base.calls.Load())
}

func TestDedup_SharedExchangeHasATimeout(t *testing.T) {
	base := &fakeExchanger{release: make(chan struct{})}
	defer close(base.release)
	ex := NewDedupExchanger(base, DedupOptions{Timeout: 30 * time.Millisecond})
	_, err := ex.Exchange(context.Background(), wireQuery(t, wireQueryOpts{id: 1}))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDedup_MaxInFlightPassesExtraQueriesThrough(t *testing.T) {
	base := &fakeExchanger{release: make(chan struct{})}
	ex := NewDedupExchanger(base, DedupOptions{MaxInFlight: 2})
	type res struct {
		resp []byte
		err  error
	}
	results := make([]chan res, 4)
	queries := [][]byte{
		wireQuery(t, wireQueryOpts{id: 1, name: "a.example."}),
		wireQuery(t, wireQueryOpts{id: 2, name: "b.example."}),
		wireQuery(t, wireQueryOpts{id: 3, name: "c.example."}),
		wireQuery(t, wireQueryOpts{id: 4, name: "c.example."}),
	}
	send := func(i int) {
		results[i] = make(chan res, 1)
		go func() {
			r, err := ex.Exchange(context.Background(), queries[i])
			results[i] <- res{r, err}
		}()
	}
	// Two distinct queries fill the in-flight table.
	send(0)
	send(1)
	require.Eventually(t, func() bool { return base.calls.Load() == 2 }, time.Second, time.Millisecond)
	// Beyond the limit, even identical queries go to base unshared.
	send(2)
	send(3)
	require.Eventually(t, func() bool { return base.calls.Load() == 4 }, time.Second, time.Millisecond)
	close(base.release)
	for i := range queries {
		r := <-results[i]
		require.NoError(t, r.err)
		require.Equal(t, echo(queries[i], 0xee), r.resp)
	}
	d := ex.(*dedupExchanger)
	d.mu.Lock()
	defer d.mu.Unlock()
	require.Empty(t, d.calls)
}

func TestDedup_UnparseableQueriesArePassedThrough(t *testing.T) {
	base := &fakeExchanger{answer: func(q []byte) ([]byte, error) { return []byte("raw"), nil }}
	ex := NewDedupExchanger(base, DedupOptions{})
	resp, err := ex.Exchange(context.Background(), []byte{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, []byte("raw"), resp)
}

func TestDedup_BaseErrorReachesEveryWaiter(t *testing.T) {
	base := &fakeExchanger{release: make(chan struct{}), answer: func([]byte) ([]byte, error) { return nil, errors.New("boom") }}
	ex := NewDedupExchanger(base, DedupOptions{})
	q := wireQuery(t, wireQueryOpts{id: 1})
	_, errs := exchangeConcurrently(t, ex, base, [][]byte{q, q, q}, 1)
	for _, err := range errs {
		require.ErrorContains(t, err, "boom")
	}
}

func TestDedup_NoGoroutineLeak(t *testing.T) {
	base := &fakeExchanger{}
	ex := NewDedupExchanger(base, DedupOptions{})
	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	for i := range 500 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(i%3)*time.Millisecond)
			defer cancel()
			_, _ = ex.Exchange(ctx, wireQuery(t, wireQueryOpts{id: uint16(i), name: fmt.Sprintf("h%d.example.", i%20)}))
		}()
	}
	wg.Wait()
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+2 }, 2*time.Second, 10*time.Millisecond)
	d := ex.(*dedupExchanger)
	d.mu.Lock()
	defer d.mu.Unlock()
	require.Empty(t, d.calls)
}
