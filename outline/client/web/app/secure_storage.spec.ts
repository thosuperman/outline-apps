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

import {SecretBackend, SecureStorage} from './secure_storage';

class FakeBackend implements SecretBackend {
  data = new Map<string, string>();
  failWrites = false;
  ops: string[] = [];
  async get(name: string) {
    return this.data.get(name) ?? null;
  }
  async set(name: string, value: string) {
    this.ops.push(`set ${name}`);
    if (this.failWrites) throw new Error('keychain locked');
    this.data.set(name, value);
  }
  async remove(name: string) {
    this.ops.push(`remove ${name}`);
    this.data.delete(name);
  }
}

describe('SecureStorage', () => {
  it('migrates legacy values into the backend and deletes the plaintext copy', async () => {
    const backend = new FakeBackend();
    const legacy = new InMemoryStorage();
    legacy.setItem('servers_v1', '[{"id":"a"}]');
    legacy.setItem('unrelated', 'x');
    const storage = await SecureStorage.load(backend, ['servers_v1'], legacy);
    expect(storage.getItem('servers_v1')).toEqual('[{"id":"a"}]');
    expect(backend.data.get('servers_v1')).toEqual('[{"id":"a"}]');
    expect(legacy.getItem('servers_v1')).toBeNull();
    expect(legacy.getItem('unrelated')).toEqual('x');
  });

  it('keeps the legacy copy when the backend cannot store it', async () => {
    const backend = new FakeBackend();
    backend.failWrites = true;
    const legacy = new InMemoryStorage();
    legacy.setItem('servers_v1', 'v');
    const storage = await SecureStorage.load(backend, ['servers_v1'], legacy);
    expect(storage.getItem('servers_v1')).toEqual('v');
    expect(legacy.getItem('servers_v1')).toEqual('v');
  });

  it('prefers the backend and removes a stale legacy copy', async () => {
    const backend = new FakeBackend();
    backend.data.set('servers_v1', 'new');
    const legacy = new InMemoryStorage();
    legacy.setItem('servers_v1', 'old');
    const storage = await SecureStorage.load(backend, ['servers_v1'], legacy);
    expect(storage.getItem('servers_v1')).toEqual('new');
    expect(legacy.getItem('servers_v1')).toBeNull();
  });

  it('writes through in order', async () => {
    const backend = new FakeBackend();
    const storage = await SecureStorage.load(backend, ['a', 'b']);
    storage.setItem('a', '1');
    storage.setItem('b', '2');
    storage.removeItem('a');
    expect(storage.getItem('a')).toBeNull();
    await storage.flush();
    expect(backend.ops).toEqual(['set a', 'set b', 'remove a']);
    expect(backend.data.get('b')).toEqual('2');
    expect(backend.data.has('a')).toBeFalse();
  });

  it('records write failures without throwing', async () => {
    const backend = new FakeBackend();
    const storage = await SecureStorage.load(backend, ['a']);
    backend.failWrites = true;
    storage.setItem('a', '1');
    await storage.flush();
    expect(storage.getItem('a')).toEqual('1');
    expect(storage.lastWriteError).toBeTruthy();
  });

  it('rejects names it does not manage', async () => {
    const storage = await SecureStorage.load(new FakeBackend(), ['a']);
    expect(() => storage.setItem('b', 'x')).toThrowError(/not a managed name/);
  });
});
