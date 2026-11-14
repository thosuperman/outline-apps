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

import {CognitoClient, CognitoError} from './cognito';
import {newSrpClient} from './srp';

const CONFIG = {
  region: 'ap-northeast-1',
  userPoolId: 'ap-northeast-1_Pool',
  clientId: 'client123',
};

type Call = {target: string; body: Record<string, unknown>};

function fakeFetch(
  responder: (call: Call) => {status?: number; body: unknown}
): {fetch: typeof fetch; calls: Call[]} {
  const calls: Call[] = [];
  const fn = (async (url: string, init: RequestInit) => {
    expect(url).toEqual('https://cognito-idp.ap-northeast-1.amazonaws.com/');
    const headers = init.headers as Record<string, string>;
    const call = {
      target: headers['X-Amz-Target'].split('.').pop()!,
      body: JSON.parse(init.body as string),
    };
    calls.push(call);
    const r = responder(call);
    return new Response(JSON.stringify(r.body), {status: r.status ?? 200});
  }) as unknown as typeof fetch;
  return {fetch: fn, calls};
}

const TOKENS = {
  IdToken: 'id',
  AccessToken: 'access',
  RefreshToken: 'refresh',
  ExpiresIn: 3600,
};

describe('CognitoClient', () => {
  it('signs in with SRP in two steps', async () => {
    const {fetch, calls} = fakeFetch(call => {
      if (call.target === 'InitiateAuth') {
        return {
          body: {
            ChallengeName: 'PASSWORD_VERIFIER',
            ChallengeParameters: {
              SALT: 'abcdef',
              SECRET_BLOCK: btoa('secret'),
              SRP_B: 'f'.repeat(100),
              USERNAME: 'uid',
              USER_ID_FOR_SRP: 'uid',
            },
          },
        };
      }
      return {body: {AuthenticationResult: TOKENS}};
    });
    const client = new CognitoClient(
      CONFIG,
      fetch,
      () => 1000,
      () => newSrpClient(BigInt(12345))
    );
    const result = await client.signIn(' User@Example.com ', 'pw');
    expect(result.kind).toEqual('signedIn');
    if (result.kind === 'signedIn') {
      expect(result.tokens).toEqual({
        idToken: 'id',
        accessToken: 'access',
        refreshToken: 'refresh',
        expiresAt: 1000 + 3600 * 1000,
      });
    }
    expect(calls[0].body.AuthFlow).toEqual('USER_SRP_AUTH');
    expect(
      (calls[0].body.AuthParameters as Record<string, string>).USERNAME
    ).toEqual('user@example.com');
    expect(calls[1].target).toEqual('RespondToAuthChallenge');
    const responses = calls[1].body.ChallengeResponses as Record<
      string,
      string
    >;
    expect(responses.USERNAME).toEqual('uid');
    expect(responses.PASSWORD_CLAIM_SIGNATURE).toMatch(/^[A-Za-z0-9+/]+=*$/);
    expect(responses.TIMESTAMP).toMatch(/ UTC \d{4}$/);
    // The password itself is never sent.
    expect(JSON.stringify(calls)).not.toContain('"pw"');
  });

  it('reports a new-password challenge and completes it', async () => {
    let step = 0;
    const {fetch} = fakeFetch(() => {
      step++;
      if (step === 1) {
        return {
          body: {
            ChallengeName: 'PASSWORD_VERIFIER',
            ChallengeParameters: {
              SALT: '01',
              SECRET_BLOCK: btoa('s'),
              SRP_B: '0f',
              USER_ID_FOR_SRP: 'uid',
            },
          },
        };
      }
      if (step === 2) {
        return {
          body: {ChallengeName: 'NEW_PASSWORD_REQUIRED', Session: 'sess'},
        };
      }
      return {body: {AuthenticationResult: TOKENS}};
    });
    const client = new CognitoClient(CONFIG, fetch);
    const r1 = await client.signIn('a@b.c', 'pw');
    expect(r1).toEqual({
      kind: 'newPasswordRequired',
      session: 'sess',
      username: 'uid',
    });
    const r2 = await client.completeNewPassword('uid', 'sess', 'new-pw');
    expect(r2.kind).toEqual('signedIn');
  });

  it('maps service errors to CognitoError codes', async () => {
    const {fetch} = fakeFetch(() => ({
      status: 400,
      body: {
        __type: 'com.amazonaws#NotAuthorizedException',
        message: 'Incorrect username or password.',
      },
    }));
    const client = new CognitoClient(CONFIG, fetch);
    try {
      await client.signIn('a@b.c', 'pw');
      fail('expected an error');
    } catch (e) {
      expect(e instanceof CognitoError).toBeTrue();
      expect((e as CognitoError).code).toEqual('NotAuthorizedException');
      expect((e as CognitoError).message).toEqual(
        'Incorrect username or password.'
      );
    }
  });

  it('refresh keeps the refresh token when Cognito does not rotate it', async () => {
    const {fetch, calls} = fakeFetch(() => ({
      body: {
        AuthenticationResult: {
          IdToken: 'id2',
          AccessToken: 'a2',
          ExpiresIn: 60,
        },
      },
    }));
    const client = new CognitoClient(CONFIG, fetch, () => 0);
    const tokens = await client.refresh('keep-me');
    expect(tokens.refreshToken).toEqual('keep-me');
    expect(tokens.idToken).toEqual('id2');
    expect(calls[0].body.AuthFlow).toEqual('REFRESH_TOKEN_AUTH');
  });

  it('network failures become NetworkError', async () => {
    const failing = (async () => {
      throw new TypeError('offline');
    }) as unknown as typeof fetch;
    const client = new CognitoClient(CONFIG, failing);
    await expectAsync(client.refresh('r')).toBeRejectedWith(
      jasmine.objectContaining({code: 'NetworkError'})
    );
  });

  it('revoke never throws', async () => {
    const {fetch} = fakeFetch(() => ({status: 500, body: {}}));
    await expectAsync(
      new CognitoClient(CONFIG, fetch).revoke('r')
    ).toBeResolved();
  });
});
