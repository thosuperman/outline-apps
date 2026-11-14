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

// Package dnsx holds DNS Exchanger decorators that the client layers over the SDK's
// dns.Exchanger (outline-sdk#637). They are proposed upstream; this copy lets the client pin
// a released SDK commit instead of an unpublished local checkout.
package dnsx

import (
	"errors"
	"fmt"

	"golang.org/x/net/dns/dnsmessage"
)

// queryInfo is what a response must match for us to accept it, as per [RFC 5452].
//
// [RFC 5452]: https://datatracker.ietf.org/doc/html/rfc5452#section-4
type queryInfo struct {
	id uint16
	// question is nil if the query carries no question section.
	question *dnsmessage.Question
}

// parseQuery extracts from a wire-format query what is needed to match a response to it.
func parseQuery(query []byte) (queryInfo, error) {
	var info queryInfo
	var p dnsmessage.Parser
	hdr, err := p.Start(query)
	if err != nil {
		return info, fmt.Errorf("query failed to parse: %w", err)
	}
	if hdr.Response {
		return info, errors.New("message is a response, not a query")
	}
	info.id = hdr.ID
	q, err := p.Question()
	switch {
	case err == nil:
		info.question = &q
	case errors.Is(err, dnsmessage.ErrSectionDone):
		// No question: match the response on the ID alone.
	default:
		return info, fmt.Errorf("query question failed to parse: %w", err)
	}
	return info, nil
}

// checkRawResponse reports whether response is a valid answer to the query described by info.
func checkRawResponse(response []byte, info queryInfo) error {
	var p dnsmessage.Parser
	hdr, err := p.Start(response)
	if err != nil {
		return fmt.Errorf("response failed to parse: %w", err)
	}
	if !hdr.Response {
		return errors.New("response bit not set")
	}
	// https://datatracker.ietf.org/doc/html/rfc5452#section-4.3
	if hdr.ID != info.id {
		return fmt.Errorf("message id does not match. Expected %v, got %v", info.id, hdr.ID)
	}
	if info.question == nil {
		return nil
	}
	// https://datatracker.ietf.org/doc/html/rfc5452#section-4.2
	respQ, err := p.Question()
	if err != nil {
		return fmt.Errorf("response had no questions: %w", err)
	}
	if info.question.Type != respQ.Type || info.question.Class != respQ.Class ||
		!equalASCIIName(info.question.Name, respQ.Name) {
		return errors.New("response question doesn't match request")
	}
	return nil
}

func foldCase(char byte) byte {
	if 'a' <= char && char <= 'z' {
		return char - 'a' + 'A'
	}
	return char
}

// equalASCIIName compares DNS names case-insensitively (RFC 1035 §3.1, RFC 4343 §3).
func equalASCIIName(x, y dnsmessage.Name) bool {
	if x.Length != y.Length {
		return false
	}
	for i := 0; i < int(x.Length); i++ {
		if foldCase(x.Data[i]) != foldCase(y.Data[i]) {
			return false
		}
	}
	return true
}

// nestedError allows errors.Is on a sentinel while preserving the cause.
type nestedError struct {
	is      error
	wrapped error
}

func (e *nestedError) Is(target error) bool { return target == e.is }

func (e *nestedError) Unwrap() error { return e.wrapped }

func (e *nestedError) Error() string { return e.is.Error() + ": " + e.wrapped.Error() }
