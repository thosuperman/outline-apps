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
  bytesToHex,
  cognitoTimestamp,
  computeX,
  g,
  hexToBytes,
  hmacSha256,
  modPow,
  multiplier,
  N,
  newSrpClient,
  padHex,
  passwordVerifier,
  randomBigInt,
  sha256,
  TEST_ONLY,
} from './srp';

// A minimal Cognito-side SRP implementation, used to check that the client's proof matches
// what the server derives from the stored verifier.
async function serverExpectedSignature(opts: {
  poolId: string;
  userId: string;
  password: string;
  salt: bigint;
  A: bigint;
  b: bigint;
  secretBlock: string;
  timestamp: string;
}): Promise<{B: bigint; signature: string}> {
  const poolName = opts.poolId.split('_')[1];
  const x = await computeX(poolName, opts.userId, opts.password, opts.salt);
  const v = modPow(g, x, N);
  const k = await multiplier();
  const B = (k * v + modPow(g, opts.b, N)) % N;
  const u = BigInt(
    '0x' + (await TEST_ONLY.hexHash(padHex(opts.A) + padHex(B)))
  );
  const S = modPow((opts.A * modPow(v, u, N)) % N, opts.b, N);
  const prk = await hmacSha256(hexToBytes(padHex(u)), hexToBytes(padHex(S)));
  const hkdf = (
    await hmacSha256(prk, TEST_ONLY.concat(TEST_ONLY.INFO, new Uint8Array([1])))
  ).slice(0, 16);
  const enc = new TextEncoder();
  const msg = TEST_ONLY.concat(
    enc.encode(poolName),
    enc.encode(opts.userId),
    TEST_ONLY.b64decode(opts.secretBlock),
    enc.encode(opts.timestamp)
  );
  return {B, signature: TEST_ONLY.b64encode(await hmacSha256(hkdf, msg))};
}

describe('Cognito SRP', () => {
  it('padHex follows Cognito rules', () => {
    expect(padHex(BigInt(0xf))).toEqual('0f');
    expect(padHex(BigInt(0x7f))).toEqual('7f');
    expect(padHex(BigInt(0x80))).toEqual('0080');
    expect(padHex(BigInt(0x1ff))).toEqual('01ff');
  });

  it('hex round-trips', () => {
    expect(bytesToHex(hexToBytes('00ff10'))).toEqual('00ff10');
    expect(bytesToHex(hexToBytes('abc'))).toEqual('0abc');
  });

  it('sha256 matches a known vector', async () => {
    expect(bytesToHex(await sha256(new TextEncoder().encode('abc')))).toEqual(
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad'
    );
  });

  it('formats timestamps like Cognito (unpadded day)', () => {
    expect(cognitoTimestamp(new Date(Date.UTC(2026, 10, 4, 9, 5, 3)))).toEqual(
      'Wed Nov 4 09:05:03 UTC 2026'
    );
    expect(
      cognitoTimestamp(new Date(Date.UTC(2026, 0, 15, 23, 59, 0)))
    ).toEqual('Thu Jan 15 23:59:00 UTC 2026');
  });

  it('produces the signature the server expects', async () => {
    const poolId = 'ap-northeast-1_TestPool1';
    const userId = 'user-123';
    const password = 'correct horse battery staple';
    const salt = randomBigInt(16);
    const client = newSrpClient();
    const secretBlock = TEST_ONLY.b64encode(new Uint8Array([1, 2, 3, 4, 5]));
    const timestamp = 'Wed Nov 4 09:05:03 UTC 2026';
    const server = await serverExpectedSignature({
      poolId,
      userId,
      password,
      salt,
      A: client.A,
      b: randomBigInt(32),
      secretBlock,
      timestamp,
    });
    const proof = await passwordVerifier(client, {
      userPoolId: poolId,
      userIdForSrp: userId,
      password,
      saltHex: salt.toString(16),
      srpBHex: server.B.toString(16),
      secretBlockB64: secretBlock,
      timestamp,
    });
    expect(proof.signature).toEqual(server.signature);
    expect(proof.timestamp).toEqual(timestamp);
  });

  it('a wrong password gives a different signature', async () => {
    const poolId = 'ap-northeast-1_TestPool1';
    const salt = randomBigInt(16);
    const client = newSrpClient();
    const secretBlock = TEST_ONLY.b64encode(new Uint8Array([9]));
    const timestamp = 'Wed Nov 4 09:05:03 UTC 2026';
    const server = await serverExpectedSignature({
      poolId,
      userId: 'u',
      password: 'right',
      salt,
      A: client.A,
      b: randomBigInt(32),
      secretBlock,
      timestamp,
    });
    const proof = await passwordVerifier(client, {
      userPoolId: poolId,
      userIdForSrp: 'u',
      password: 'wrong',
      saltHex: salt.toString(16),
      srpBHex: server.B.toString(16),
      secretBlockB64: secretBlock,
      timestamp,
    });
    expect(proof.signature).not.toEqual(server.signature);
  });

  it('rejects B = 0 mod N', async () => {
    await expectAsync(
      passwordVerifier(newSrpClient(), {
        userPoolId: 'r_p',
        userIdForSrp: 'u',
        password: 'p',
        saltHex: '01',
        srpBHex: N.toString(16),
        secretBlockB64: 'AA==',
      })
    ).toBeRejectedWithError(/illegal B/);
  });
});
