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
 * Amazon Cognito USER_SRP_AUTH client math (SRP-6a, RFC 5054 3072-bit group, SHA-256).
 *
 * The password never leaves the device: we send A, Cognito returns B and a salt, and we prove
 * knowledge of the password with an HMAC over Cognito's secret block. Implemented with BigInt
 * and WebCrypto so the client needs no Amplify dependency.
 */

// RFC 5054 3072-bit group, as used by Cognito.
const N_HEX =
  'FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74' +
  '020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F1437' +
  '4FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED' +
  'EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3DC2007CB8A163BF05' +
  '98DA48361C55D39A69163FA8FD24CF5F83655D23DCA3AD961C62F356208552BB' +
  '9ED529077096966D670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B' +
  'E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF695581718' +
  '3995497CEA956AE515D2261898FA051015728E5A8AAAC42DAD33170D04507A33' +
  'A85521ABDF1CBA64ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7' +
  'ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6BF12FFA06D98A0864' +
  'D87602733EC86A64521F2B18177B200CBBE117577A615D6C770988C0BAD946E2' +
  '08E24FA074E5AB3143DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF';

// BigInt literals (0n) need an ES2020 target; the web build targets lower.
const ZERO = BigInt(0);
const ONE = BigInt(1);
const TWO = BigInt(2);

export const N = BigInt('0x' + N_HEX);
export const g = TWO;

const INFO = new TextEncoder().encode('Caldera Derived Key');

export function hexToBytes(hex: string): Uint8Array {
  if (hex.length % 2) hex = '0' + hex;
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.substr(i * 2, 2), 16);
  }
  return out;
}

export function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
}

/**
 * Cognito's padHex: even length, and a leading 00 when the high bit is set, so the value is
 * read as positive. Cognito hashes these exact bytes, so this must match bit for bit.
 */
export function padHex(value: bigint): string {
  if (value < ZERO) throw new Error('padHex: negative value');
  let hex = value.toString(16);
  if (hex.length % 2) hex = '0' + hex;
  if (/^[89a-f]/i.test(hex)) hex = '00' + hex;
  return hex;
}

export function modPow(base: bigint, exp: bigint, mod: bigint): bigint {
  let result = ONE;
  base = ((base % mod) + mod) % mod;
  while (exp > ZERO) {
    if (exp & ONE) result = (result * base) % mod;
    base = (base * base) % mod;
    exp >>= ONE;
  }
  return result;
}

function subtle(): SubtleCrypto {
  const s = globalThis.crypto?.subtle;
  if (!s) throw new Error('WebCrypto is not available');
  return s;
}

export async function sha256(data: Uint8Array): Promise<Uint8Array> {
  return new Uint8Array(await subtle().digest('SHA-256', data));
}

export async function hmacSha256(
  key: Uint8Array,
  data: Uint8Array
): Promise<Uint8Array> {
  const k = await subtle().importKey(
    'raw',
    key,
    {name: 'HMAC', hash: 'SHA-256'},
    false,
    ['sign']
  );
  return new Uint8Array(await subtle().sign('HMAC', k, data));
}

async function hexHash(hex: string): Promise<string> {
  return bytesToHex(await sha256(hexToBytes(hex)));
}

function concat(...parts: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

let kCache: bigint | undefined;
export async function multiplier(): Promise<bigint> {
  if (kCache === undefined) {
    kCache = BigInt('0x' + (await hexHash(padHex(N) + padHex(g))));
  }
  return kCache;
}

export function randomBigInt(bytes: number): bigint {
  const buf = new Uint8Array(bytes);
  globalThis.crypto.getRandomValues(buf);
  return BigInt('0x' + bytesToHex(buf));
}

export interface SrpClient {
  readonly a: bigint;
  readonly A: bigint;
}

/** Creates the ephemeral client key pair. `a` may be injected for tests. */
export function newSrpClient(a: bigint = randomBigInt(128)): SrpClient {
  const A = modPow(g, a, N);
  if (A % N === ZERO) throw new Error('SRP: illegal A');
  return {a, A};
}

/** Cognito's timestamp format, e.g. "Tue Nov 4 09:05:03 UTC 2026" (day of month unpadded). */
export function cognitoTimestamp(now: Date = new Date()): string {
  const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
  const months = [
    'Jan',
    'Feb',
    'Mar',
    'Apr',
    'May',
    'Jun',
    'Jul',
    'Aug',
    'Sep',
    'Oct',
    'Nov',
    'Dec',
  ];
  const p = (n: number) => String(n).padStart(2, '0');
  return (
    `${days[now.getUTCDay()]} ${months[now.getUTCMonth()]} ${now.getUTCDate()} ` +
    `${p(now.getUTCHours())}:${p(now.getUTCMinutes())}:${p(now.getUTCSeconds())} UTC ` +
    `${now.getUTCFullYear()}`
  );
}

export interface PasswordVerifierInput {
  userPoolId: string; // e.g. "ap-northeast-1_AbCdEf"
  userIdForSrp: string;
  password: string;
  saltHex: string;
  srpBHex: string;
  secretBlockB64: string;
  timestamp?: string;
}

function b64decode(s: string): Uint8Array {
  const bin = atob(s);
  return Uint8Array.from(bin, c => c.charCodeAt(0));
}

function b64encode(b: Uint8Array): string {
  let s = '';
  for (const x of b) s += String.fromCharCode(x);
  return btoa(s);
}

/** x = H(salt | H(poolName | userId | ":" | password)) — Cognito's private key. */
export async function computeX(
  poolName: string,
  userIdForSrp: string,
  password: string,
  salt: bigint
): Promise<bigint> {
  const userHash = bytesToHex(
    await sha256(
      new TextEncoder().encode(`${poolName}${userIdForSrp}:${password}`)
    )
  );
  return BigInt('0x' + (await hexHash(padHex(salt) + userHash)));
}

/** Computes the PASSWORD_VERIFIER challenge responses. */
export async function passwordVerifier(
  client: SrpClient,
  input: PasswordVerifierInput
): Promise<{timestamp: string; signature: string}> {
  const poolName = input.userPoolId.split('_')[1];
  if (!poolName) throw new Error('SRP: bad user pool id');
  const B = BigInt('0x' + input.srpBHex);
  if (B % N === ZERO) throw new Error('SRP: illegal B');
  const u = BigInt('0x' + (await hexHash(padHex(client.A) + padHex(B))));
  if (u === ZERO) throw new Error('SRP: illegal u');
  const salt = BigInt('0x' + input.saltHex);
  const x = await computeX(poolName, input.userIdForSrp, input.password, salt);
  const k = await multiplier();
  const base = (((B - k * modPow(g, x, N)) % N) + N) % N;
  const S = modPow(base, client.a + u * x, N);

  const prk = await hmacSha256(hexToBytes(padHex(u)), hexToBytes(padHex(S)));
  const hkdf = (await hmacSha256(prk, concat(INFO, new Uint8Array([1])))).slice(
    0,
    16
  );

  const timestamp = input.timestamp ?? cognitoTimestamp();
  const msg = concat(
    new TextEncoder().encode(poolName),
    new TextEncoder().encode(input.userIdForSrp),
    b64decode(input.secretBlockB64),
    new TextEncoder().encode(timestamp)
  );
  const signature = b64encode(await hmacSha256(hkdf, msg));
  return {timestamp, signature};
}

export const TEST_ONLY = {b64encode, b64decode, concat, hexHash, INFO};
