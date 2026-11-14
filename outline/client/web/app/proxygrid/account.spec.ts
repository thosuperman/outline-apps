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

import {InMemoryStorage} from '@outline/infrastructure/memory_storage';

import {AccountServerStore, ProxyGridAccount} from './account';
import {ProxyGridApi, ProxyGridApiError, VpnStatus} from './api';
import {CognitoClient, CognitoError, CognitoTokens} from './cognito';
import {PROXYGRID_SESSION_STORAGE_NAME, ProxyGridSession} from './session';
import {ServerAlreadyAdded} from '../../model/errors';

const TOKENS: CognitoTokens = {
  idToken: 'id-1',
  accessToken: 'a',
  refreshToken: 'r-1',
  expiresAt: 10_000_000,
};

class FakeCognito {
  refreshCalls = 0;
  refreshResult: CognitoTokens | Error = {...TOKENS, idToken: 'id-2'};
  revoked: string[] = [];
  async signIn() {
    return {kind: 'signedIn' as const, tokens: TOKENS};
  }
  async refresh() {
    this.refreshCalls++;
    await Promise.resolve();
    if (this.refreshResult instanceof Error) throw this.refreshResult;
    return this.refreshResult;
  }
  async revoke(t: string) {
    this.revoked.push(t);
  }
}

function status(overrides: Partial<VpnStatus> = {}): VpnStatus {
  return {
    enabled: true,
    suspended: false,
    plan: {
      id: 'free',
      name: 'Free',
      quota_bytes: 3e9,
      period_start: '2026-11-01',
      period_end: '2026-12-01',
    },
    used_bytes: 1e9,
    remaining_bytes: 2e9,
    over_quota: false,
    regions: [
      {
        region: 'ap-northeast-1',
        label: 'Tokyo',
        node_id: 'tyo1',
        node_status: 'active',
        key: null,
      },
    ],
    reactivated: false,
    queued_plan: null,
    ...overrides,
  };
}

class FakeServers implements AccountServerStore {
  servers: Array<{id: string; name: string; key: string}> = [];
  forgotten: string[] = [];
  async add(key: string) {
    if (this.servers.some(s => s.key === key)) {
      throw new ServerAlreadyAdded({id: 'x', name: 'x'} as never);
    }
    const name = decodeURIComponent(key.split('#')[1] ?? 'Server');
    this.servers.push({id: `s${this.servers.length + 1}`, name, key});
  }
  async forget(id: string) {
    this.forgotten.push(id);
    this.servers = this.servers.filter(s => s.id !== id);
  }
  getAll() {
    return this.servers;
  }
  getAccessKey(id: string) {
    return this.servers.find(s => s.id === id)?.key;
  }
}

function setup(now = 0) {
  const cognito = new FakeCognito();
  const storage = new InMemoryStorage();
  const session = new ProxyGridSession(
    cognito as unknown as CognitoClient,
    storage,
    () => now
  );
  const api = jasmine.createSpyObj<ProxyGridApi>('api', [
    'status',
    'createKey',
    'keyAccess',
  ]);
  api.status.and.resolveTo(status());
  api.createKey.and.resolveTo({
    key: {key_id: 'k1', region: 'ap-northeast-1', status: 'active'},
    ssconf_url:
      'ssconf://api.proxygrid.net/vpn/c/' +
      'a'.repeat(43) +
      '#ProxyGrid%20Tokyo',
  });
  const servers = new FakeServers();
  const prefs = new InMemoryStorage();
  const account = new ProxyGridAccount(
    session,
    api,
    servers,
    {get: n => prefs.getItem(n), set: (n, v) => prefs.setItem(n, v)},
    () => now
  );
  return {cognito, storage, session, api, servers, prefs, account};
}

describe('ProxyGridSession', () => {
  it('persists the session and restores it', async () => {
    const {session, storage, cognito} = setup();
    await session.signIn(' A@B.com ', 'pw');
    expect(session.state).toEqual({kind: 'signedIn', email: 'a@b.com'});
    expect(storage.getItem(PROXYGRID_SESSION_STORAGE_NAME)).toContain('r-1');
    const restored = new ProxyGridSession(
      cognito as unknown as CognitoClient,
      storage
    );
    expect(restored.state.kind).toEqual('signedIn');
  });

  it('refreshes once for concurrent callers when the token is near expiry', async () => {
    const {session, cognito} = setup(TOKENS.expiresAt - 1000);
    await session.signIn('a@b.c', 'pw');
    const [t1, t2] = await Promise.all([
      session.getIdToken(),
      session.getIdToken(),
    ]);
    expect(t1).toEqual('id-2');
    expect(t2).toEqual('id-2');
    expect(cognito.refreshCalls).toEqual(1);
  });

  it('signs out when Cognito rejects the refresh token', async () => {
    const {session, cognito, storage} = setup(TOKENS.expiresAt);
    await session.signIn('a@b.c', 'pw');
    cognito.refreshResult = new CognitoError(
      'NotAuthorizedException',
      'revoked'
    );
    expect(await session.getIdToken()).toBeNull();
    expect(session.state.kind).toEqual('signedOut');
    expect(storage.getItem(PROXYGRID_SESSION_STORAGE_NAME)).toBeNull();
  });

  it('keeps the session on a network failure during refresh', async () => {
    const {session, cognito} = setup(TOKENS.expiresAt);
    await session.signIn('a@b.c', 'pw');
    cognito.refreshResult = new CognitoError('NetworkError', 'offline');
    await expectAsync(session.getIdToken()).toBeRejected();
    expect(session.state.kind).toEqual('signedIn');
  });

  it('revokes the refresh token on sign-out', async () => {
    const {session, cognito} = setup();
    await session.signIn('a@b.c', 'pw');
    await session.signOut();
    expect(cognito.revoked).toEqual(['r-1']);
    expect(session.state.kind).toEqual('signedOut');
  });
});

describe('ProxyGridAccount', () => {
  it('creates a key for the selected region once and reuses it', async () => {
    const {session, account, api, servers} = setup();
    await session.signIn('a@b.c', 'pw');
    await account.refresh();
    const id = await account.ensureRegionServer();
    expect(servers.servers.length).toEqual(1);
    expect(api.createKey).toHaveBeenCalledOnceWith('ap-northeast-1');
    expect(await account.ensureRegionServer()).toEqual(id);
    expect(api.createKey).toHaveBeenCalledTimes(1);
    expect(account.proxyGridServerIds().has(id)).toBeTrue();
  });

  it('reuses the existing backend key instead of creating a new one', async () => {
    const {session, account, api} = setup();
    api.status.and.resolveTo(
      status({
        regions: [
          {
            region: 'ap-northeast-1',
            label: 'Tokyo',
            node_id: 'tyo1',
            node_status: 'active',
            key: {key_id: 'old', region: 'ap-northeast-1', status: 'active'},
          },
        ],
      })
    );
    api.keyAccess.and.resolveTo({
      key: {key_id: 'old', region: 'ap-northeast-1', status: 'active'},
      ssconf_url:
        'ssconf://api.proxygrid.net/vpn/c/' +
        'b'.repeat(43) +
        '#ProxyGrid%20Tokyo',
    });
    await session.signIn('a@b.c', 'pw');
    await account.refresh();
    await account.ensureRegionServer();
    expect(api.keyAccess).toHaveBeenCalledOnceWith('old');
    expect(api.createKey).not.toHaveBeenCalled();
  });

  it('refuses to provision when over quota', async () => {
    const {session, account, api} = setup();
    api.status.and.resolveTo(status({over_quota: true, remaining_bytes: 0}));
    await session.signIn('a@b.c', 'pw');
    await account.refresh();
    await expectAsync(account.ensureRegionServer()).toBeRejectedWithError(
      /allowance/
    );
  });

  it('retries once with a fresh token after a 401', async () => {
    const {session, account, api} = setup();
    await session.signIn('a@b.c', 'pw');
    let calls = 0;
    api.status.and.callFake(async () => {
      // Create the rejection lazily so it is never an unhandled promise.
      if (calls++ === 0) throw new ProxyGridApiError(401, 'Unauthorized');
      return status();
    });
    await account.refresh();
    expect(account.current.status).not.toBeNull();
    expect(account.current.error).toBeNull();
  });

  it('keeps the last status and flags errors when the API is unreachable', async () => {
    const {session, account, api} = setup();
    await session.signIn('a@b.c', 'pw');
    await account.refresh();
    api.status.and.rejectWith(new ProxyGridApiError(0, 'offline'));
    await account.refresh();
    expect(account.current.status).not.toBeNull();
    expect(account.current.error).toEqual('offline');
  });

  it('remembers the selected region', () => {
    const {account, prefs} = setup();
    account.selectRegion('eu-central-1');
    expect(account.current.selectedRegion).toEqual('eu-central-1');
    expect(prefs.getItem('proxygrid_region')).toEqual('eu-central-1');
  });

  it('sign-out disconnects and removes every ProxyGrid key but keeps own servers', async () => {
    const {session, account, servers} = setup();
    await session.signIn('a@b.c', 'pw');
    await account.refresh();
    await servers.add('ss://own-server');
    const pgId = await account.ensureRegionServer();
    const disconnected: string[] = [];
    await account.signOut(async id => {
      disconnected.push(id);
    });
    expect(disconnected).toEqual([pgId]);
    expect(servers.servers.map(s => s.key)).toEqual(['ss://own-server']);
    expect(session.state.kind).toEqual('signedOut');
  });
});
