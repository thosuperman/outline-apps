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

import {promises as fs} from 'fs';
import * as path from 'path';

import {app, safeStorage} from 'electron';

// Secrets (access keys, the account refresh token) encrypted with Electron's safeStorage, which
// uses DPAPI on Windows, the Keychain on macOS and libsecret/kwallet on Linux. Only ciphertext is
// written to disk, in the per-user app data directory.

const FILE_NAME = 'secrets.v1.json';

type SecretFile = Record<string, string>; // name -> base64 ciphertext

function filePath(): string {
  return path.join(app.getPath('userData'), FILE_NAME);
}

let writeChain: Promise<unknown> = Promise.resolve();

async function readAll(): Promise<SecretFile> {
  try {
    return JSON.parse(await fs.readFile(filePath(), 'utf8')) as SecretFile;
  } catch {
    return {};
  }
}

async function writeAll(data: SecretFile): Promise<void> {
  const target = filePath();
  const tmp = `${target}.tmp`;
  await fs.writeFile(tmp, JSON.stringify(data), {
    encoding: 'utf8',
    mode: 0o600,
  });
  await fs.rename(tmp, target);
}

function ensureAvailable() {
  if (!safeStorage.isEncryptionAvailable()) {
    // Never fall back to plaintext: the caller keeps the secret in memory only.
    throw new Error('OS secret storage is not available');
  }
}

// Writes are serialized so concurrent updates cannot lose each other.
function serialized<T>(fn: () => Promise<T>): Promise<T> {
  const run = writeChain.then(fn, fn);
  writeChain = run.catch(() => {});
  return run;
}

export async function getSecret(name: string): Promise<string | null> {
  ensureAvailable();
  const data = await readAll();
  const blob = data[name];
  if (!blob) return null;
  try {
    return safeStorage.decryptString(Buffer.from(blob, 'base64'));
  } catch {
    // Encrypted for another user or machine: unrecoverable.
    return null;
  }
}

export function setSecret(name: string, value: string): Promise<void> {
  ensureAvailable();
  return serialized(async () => {
    const data = await readAll();
    data[name] = safeStorage.encryptString(value).toString('base64');
    await writeAll(data);
  });
}

export function removeSecret(name: string): Promise<void> {
  return serialized(async () => {
    const data = await readAll();
    if (name in data) {
      delete data[name];
      await writeAll(data);
    }
  });
}
