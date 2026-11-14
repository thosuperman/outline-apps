// Copyright 2018 The Outline Authors
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

import {unwrapAppLink, unwrapInvite, isOutlineAccessKey} from './app';

describe('unwrapAppLink', () => {
  it('extracts the key from proxygridvpn://add', () => {
    const key = 'ss://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTpwdw@203.0.113.5:443#Home';
    expect(
      unwrapAppLink(`proxygridvpn://add?key=${encodeURIComponent(key)}`)
    ).toEqual(key);
    expect(
      unwrapAppLink(
        `proxygridvpn://add/?key=${encodeURIComponent('ssconf://api.proxygrid.net/vpn/c/abc#ProxyGrid Tokyo')}`
      )
    ).toEqual('ssconf://api.proxygrid.net/vpn/c/abc#ProxyGrid Tokyo');
  });

  it('returns an empty string for app links without a key', () => {
    expect(unwrapAppLink('proxygridvpn://open')).toEqual('');
    expect(unwrapAppLink('proxygridvpn://add')).toEqual('');
  });

  it('returns null for other URLs', () => {
    expect(unwrapAppLink('ss://abc')).toBeNull();
    expect(unwrapAppLink('https://proxygrid.net/vpn')).toBeNull();
    expect(unwrapAppLink('')).toBeNull();
  });

  it('is applied by unwrapInvite', () => {
    expect(
      unwrapInvite(`proxygridvpn://add?key=${encodeURIComponent('ss://k@h:1')}`)
    ).toEqual('ss://k@h:1');
  });
});

describe('unwrapInvite', () => {
  it('ignores empty string', () => {
    expect(unwrapInvite('')).toEqual('');
  });

  it('ignores garbage', () => {
    const s = 'i am not a shadowsocks link';
    expect(unwrapInvite(s)).toEqual(s);
  });

  it('ignores url without fragment', () => {
    const s = 'https://whatever.com/invite.html';
    expect(unwrapInvite(s)).toEqual(s);
  });

  it('ignores non-ss fragment', () => {
    const s = 'https://whatever.com/invite.html#iamjustaname';
    expect(unwrapInvite(s)).toEqual(s);
  });

  it('detects ss fragment', () => {
    const s = 'ss://myhost.com:3333';
    expect(
      unwrapInvite(`https://whatever.com/invite.html#${encodeURIComponent(s)}`)
    ).toEqual(s);
  });

  it('handles fragment after redirect', () => {
    const s = 'ss://myhost.com:3333';
    expect(
      unwrapInvite(
        `https://whatever.com/invite.html#/en/invite/${encodeURIComponent(s)}`
      )
    ).toEqual(s);
  });
});

describe('isOutlineAccessKey', () => {
  it('ignores empty string', () => expect(isOutlineAccessKey('')).toBe(false));
  it('ignores garbage', () =>
    expect(isOutlineAccessKey('i am not a outline service location')).toBe(
      false
    ));
  it('ignores random https links', () =>
    expect(isOutlineAccessKey('https://example.com')).toBe(false));

  it('detects static keys', () =>
    expect(isOutlineAccessKey('ss://myhost.com:3333')).toBe(true));
  it('detects dynamic keys', () =>
    expect(
      isOutlineAccessKey('ssconf://my.cool.server.com:3423#veryfast')
    ).toBe(true));
});
