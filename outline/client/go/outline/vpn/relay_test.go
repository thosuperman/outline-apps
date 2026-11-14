// Copyright 2026 ProxyGrid
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vpn

import (
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// packetSource returns one queued packet or error per Read, then io.EOF.
type packetSource struct {
	mu    sync.Mutex
	items []any // []byte or error
}

func (s *packetSource) Read(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) == 0 {
		return 0, io.EOF
	}
	it := s.items[0]
	s.items = s.items[1:]
	if err, ok := it.(error); ok {
		return 0, err
	}
	return copy(b, it.([]byte)), nil
}

// packetSink rejects packets that start with 0xFF, like a stack rejecting a malformed packet.
type packetSink struct {
	mu      sync.Mutex
	got     [][]byte
	closed  bool
	closeAt int // close after this many accepted packets (0 = never)
}

func (s *packetSink) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, os.ErrClosed
	}
	if len(b) > 0 && b[0] == 0xFF {
		return 0, errors.New("unknown IP version")
	}
	s.got = append(s.got, append([]byte(nil), b...))
	if s.closeAt > 0 && len(s.got) >= s.closeAt {
		s.closed = true
	}
	return len(b), nil
}

func (s *packetSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func runRelay(t *testing.T, dst io.WriteCloser, src io.Reader) {
	done := make(chan struct{})
	go func() { RelayTraffic(dst, src); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not finish")
	}
}

func TestRelayTraffic_DropsRejectedPacketAndContinues(t *testing.T) {
	src := &packetSource{items: []any{[]byte{0x45, 1}, []byte{0xFF, 2}, []byte{0x45, 3}}}
	dst := &packetSink{}
	runRelay(t, dst, src)
	require.Equal(t, [][]byte{{0x45, 1}, {0x45, 3}}, dst.got)
	require.True(t, dst.closed, "dst must be closed when the source ends")
}

func TestRelayTraffic_ContinuesAfterTransientReadError(t *testing.T) {
	src := &packetSource{items: []any{errors.New("EAGAIN"), []byte{0x45, 1}}}
	dst := &packetSink{}
	runRelay(t, dst, src)
	require.Equal(t, [][]byte{{0x45, 1}}, dst.got)
}

func TestRelayTraffic_StopsWhenSourceClosed(t *testing.T) {
	src := &packetSource{items: []any{os.ErrClosed, []byte{0x45, 1}}}
	dst := &packetSink{}
	runRelay(t, dst, src)
	require.Empty(t, dst.got)
}

func TestRelayTraffic_StopsWhenDestinationClosed(t *testing.T) {
	src := &packetSource{items: []any{[]byte{0x45, 1}, []byte{0x45, 2}, []byte{0x45, 3}}}
	dst := &packetSink{closeAt: 1}
	runRelay(t, dst, src)
	require.Equal(t, [][]byte{{0x45, 1}}, dst.got)
}

type errorSource struct{ reads int }

func (s *errorSource) Read([]byte) (int, error) { s.reads++; return 0, errors.New("broken") }

func TestRelayTraffic_GivesUpOnPersistentReadErrors(t *testing.T) {
	src := &errorSource{}
	runRelay(t, &packetSink{}, src)
	require.Equal(t, maxConsecutiveReadErrors, src.reads)
}
