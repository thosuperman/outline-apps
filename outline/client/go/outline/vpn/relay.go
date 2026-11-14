// Copyright 2025 The Outline Authors
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
	"log/slog"
	"math/rand"
	"net"
	"os"

	"golang.getoutline.org/sdk/network"
)

// maxConsecutiveReadErrors bounds how long we keep reading a source that only returns errors,
// so a broken device cannot spin a goroutine forever.
const maxConsecutiveReadErrors = 100

// isClosedErr reports whether err means the device or connection is gone for good.
func isClosedErr(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) || errors.Is(err, network.ErrClosed)
}

// RelayTraffic copies packets from `src` to `dst` until `src` or `dst` is closed.
// It closes `dst` when complete.
//
// Each Read returns one IP packet. A packet that fails to write (for example a malformed packet
// the network stack rejects) is dropped and the relay goes on: stopping on the first such error
// used to black-hole the tunnel silently while the UI still showed it connected.
func RelayTraffic(dst io.WriteCloser, src io.Reader) {
	id := rand.Intn(1000) // A fake goroutine ID for logging
	slog.Debug("relaying traffic ...", "#", id)
	buf := make([]byte, 65535)
	var n int64
	var dropped, readErrs int
	var err error
	for {
		var nr int
		nr, err = src.Read(buf)
		if nr > 0 {
			readErrs = 0
			if _, werr := dst.Write(buf[:nr]); werr != nil {
				if isClosedErr(werr) {
					err = werr
					break
				}
				dropped++
				slog.Debug("dropped packet", "#", id, "err", werr)
			} else {
				n += int64(nr)
			}
		}
		if err != nil {
			if isClosedErr(err) {
				break
			}
			readErrs++
			if readErrs >= maxConsecutiveReadErrors {
				break
			}
			slog.Debug("relay read error", "#", id, "err", err)
		}
	}
	slog.Debug("relaying traffic done", "#", id, "n", n, "dropped", dropped, "err", err)
	dst.Close()
}
