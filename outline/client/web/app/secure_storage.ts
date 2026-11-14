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

/**
 * Keeps secrets (access keys, the ProxyGrid session) in the OS keychain/keystore instead of the
 * web view's local storage.
 *
 * The server repository expects a synchronous DOM Storage, so SecureStorage loads the managed
 * names into memory once at startup and writes through to the backend asynchronously, in order.
 * On first load, values found in the legacy local storage are moved into the backend and then
 * deleted from local storage.
 */

export interface SecretBackend {
  get(name: string): Promise<string | null>;
  set(name: string, value: string): Promise<void>;
  remove(name: string): Promise<void>;
}

/** Fallback for the browser dev build: no OS keychain available. */
export class LocalStorageSecretBackend implements SecretBackend {
  constructor(
    private readonly storage: Storage = window.localStorage,
    private readonly prefix = 'secret:'
  ) {}
  async get(name: string) {
    return this.storage.getItem(this.prefix + name);
  }
  async set(name: string, value: string) {
    this.storage.setItem(this.prefix + name, value);
  }
  async remove(name: string) {
    this.storage.removeItem(this.prefix + name);
  }
}

export class SecureStorage implements Storage {
  private writeChain: Promise<void> = Promise.resolve();
  private lastError: unknown = null;

  private constructor(
    private readonly backend: SecretBackend,
    private readonly names: ReadonlySet<string>,
    private readonly values: Map<string, string>
  ) {}

  /**
   * Loads `names` from `backend`, migrating any of them still in `legacy` into the backend.
   * A legacy value is only removed after the backend confirmed the write.
   */
  static async load(
    backend: SecretBackend,
    names: string[],
    legacy?: Storage
  ): Promise<SecureStorage> {
    const values = new Map<string, string>();
    for (const name of names) {
      let value: string | null = null;
      try {
        value = await backend.get(name);
      } catch (e) {
        console.error(`secure storage: failed to read ${name}`, e);
      }
      const legacyValue = legacy?.getItem(name) ?? null;
      if (value === null && legacyValue !== null) {
        value = legacyValue;
        try {
          await backend.set(name, legacyValue);
          legacy!.removeItem(name);
        } catch (e) {
          // Keep the legacy copy so nothing is lost; retry on next start.
          console.error(`secure storage: failed to migrate ${name}`, e);
        }
      } else if (value !== null && legacyValue !== null) {
        // Already migrated; drop the stale plaintext copy.
        legacy!.removeItem(name);
      }
      if (value !== null) values.set(name, value);
    }
    return new SecureStorage(backend, new Set(names), values);
  }

  /** Resolves once every write issued so far has reached the backend. */
  flush(): Promise<void> {
    return this.writeChain;
  }

  get lastWriteError(): unknown {
    return this.lastError;
  }

  private enqueue(op: () => Promise<void>) {
    this.writeChain = this.writeChain
      .then(op)
      .then(() => {
        this.lastError = null;
      })
      .catch(e => {
        this.lastError = e;
        console.error('secure storage: write failed', e);
      });
  }

  private check(name: string) {
    if (!this.names.has(name)) {
      throw new Error(`secure storage: ${name} is not a managed name`);
    }
  }

  get length(): number {
    return this.values.size;
  }

  key(index: number): string | null {
    return Array.from(this.values.keys())[index] ?? null;
  }

  getItem(name: string): string | null {
    this.check(name);
    return this.values.get(name) ?? null;
  }

  setItem(name: string, value: string): void {
    this.check(name);
    value = String(value);
    this.values.set(name, value);
    this.enqueue(() => this.backend.set(name, value));
  }

  removeItem(name: string): void {
    this.check(name);
    this.values.delete(name);
    this.enqueue(() => this.backend.remove(name));
  }

  clear(): void {
    for (const name of Array.from(this.values.keys())) {
      this.removeItem(name);
    }
  }
}
