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

import {
  formatBytes,
  isProxyGridKey,
  ProxyGridApi,
  ProxyGridApiError,
} from './api';

function recordingFetch(status: number, body: unknown) {
  const calls: Array<{url: string; init: RequestInit}> = [];
  const fn = (async (url: string, init: RequestInit) => {
    calls.push({url, init});
    return new Response(body === undefined ? '' : JSON.stringify(body), {
      status,
    });
  }) as unknown as typeof fetch;
  return {fn, calls};
}

describe('ProxyGridApi', () => {
  it('sends the id token as a bearer token', async () => {
    const {fn, calls} = recordingFetch(200, {enabled: true});
    const api = new ProxyGridApi(async () => 'tok', 'https://api.test', fn);
    await api.status();
    expect(calls[0].url).toEqual('https://api.test/vpn/status');
    expect(
      (calls[0].init.headers as Record<string, string>).Authorization
    ).toEqual('Bearer tok');
  });

  it('posts the region when creating a key', async () => {
    const {fn, calls} = recordingFetch(201, {
      key: {key_id: 'k'},
      ssconf_url: 'ssconf://x',
    });
    const api = new ProxyGridApi(async () => 't', 'https://api.test', fn);
    const r = await api.createKey('ap-northeast-1');
    expect(r.ssconf_url).toEqual('ssconf://x');
    expect(calls[0].init.method).toEqual('POST');
    expect(JSON.parse(calls[0].init.body as string)).toEqual({
      region: 'ap-northeast-1',
    });
  });

  it('escapes key ids in paths', async () => {
    const {fn, calls} = recordingFetch(200, {});
    const api = new ProxyGridApi(async () => 't', 'https://api.test', fn);
    await api.keyAccess('a/b');
    expect(calls[0].url).toEqual('https://api.test/vpn/keys/a%2Fb/access');
  });

  it('public pricing needs no token', async () => {
    const {fn, calls} = recordingFetch(200, {vpn: {plans: []}});
    const api = new ProxyGridApi(async () => null, 'https://api.test', fn);
    await api.publicPricing();
    expect(
      (calls[0].init.headers as Record<string, string>).Authorization
    ).toBeUndefined();
  });

  it('fails fast without a token', async () => {
    const {fn, calls} = recordingFetch(200, {});
    const api = new ProxyGridApi(async () => null, 'https://api.test', fn);
    await expectAsync(api.status()).toBeRejectedWith(
      jasmine.objectContaining({status: 401})
    );
    expect(calls.length).toEqual(0);
  });

  it('surfaces the backend error message', async () => {
    const {fn} = recordingFetch(429, {
      error: 'Too many key changes today. Try again tomorrow.',
    });
    const api = new ProxyGridApi(async () => 't', 'https://api.test', fn);
    try {
      await api.createKey('us-east-1');
      fail('expected an error');
    } catch (e) {
      expect(e instanceof ProxyGridApiError).toBeTrue();
      expect((e as ProxyGridApiError).status).toEqual(429);
      expect((e as ProxyGridApiError).message).toContain(
        'Too many key changes'
      );
      expect((e as ProxyGridApiError).isAuthError).toBeFalse();
    }
  });

  it('network failures become status 0', async () => {
    const fn = (async () => {
      throw new TypeError('offline');
    }) as unknown as typeof fetch;
    const api = new ProxyGridApi(async () => 't', 'https://api.test', fn);
    await expectAsync(api.status()).toBeRejectedWith(
      jasmine.objectContaining({status: 0})
    );
  });
});

describe('isProxyGridKey', () => {
  it('recognizes ProxyGrid dynamic keys only', () => {
    expect(
      isProxyGridKey('ssconf://api.proxygrid.net/vpn/c/abc#ProxyGrid Tokyo')
    ).toBeTrue();
    expect(isProxyGridKey('ssconf://example.com/vpn/c/abc')).toBeFalse();
    expect(isProxyGridKey('ssconf://api.proxygrid.net/other')).toBeFalse();
    expect(isProxyGridKey('ss://YWVzOnB3@1.2.3.4:443')).toBeFalse();
    expect(isProxyGridKey('https://api.proxygrid.net/vpn/c/abc')).toBeFalse();
  });
});

describe('formatBytes', () => {
  it('formats decimal units', () => {
    expect(formatBytes(0)).toEqual('0 B');
    expect(formatBytes(1500)).toEqual('1.5 KB');
    expect(formatBytes(3e9)).toEqual('3.0 GB');
    expect(formatBytes(123e9)).toEqual('123 GB');
    expect(formatBytes(-1)).toEqual('—');
  });
});
