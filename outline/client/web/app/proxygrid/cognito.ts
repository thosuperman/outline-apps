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
 * Minimal Amazon Cognito user-pool client: SRP sign-in, the challenges the ProxyGrid pool can
 * raise (new password, TOTP), and token refresh. Talks to the public Cognito JSON API with
 * fetch; the app client is public (no secret), as configured in the ProxyGrid backend.
 */

import {newSrpClient, passwordVerifier, SrpClient} from './srp';

export interface CognitoConfig {
  region: string;
  userPoolId: string;
  clientId: string;
}

export interface CognitoTokens {
  idToken: string;
  accessToken: string;
  refreshToken: string;
  /** Epoch milliseconds at which the id token expires. */
  expiresAt: number;
}

export type SignInResult =
  | {kind: 'signedIn'; tokens: CognitoTokens}
  | {kind: 'newPasswordRequired'; session: string; username: string}
  | {kind: 'totpRequired'; session: string; username: string};

export class CognitoError extends Error {
  constructor(
    readonly code: string,
    message: string
  ) {
    super(message);
    this.name = 'CognitoError';
  }
}

type FetchFn = typeof fetch;

export class CognitoClient {
  constructor(
    private readonly config: CognitoConfig,
    private readonly fetchFn: FetchFn = (...args) => fetch(...args),
    private readonly now: () => number = Date.now,
    private readonly srpFactory: () => SrpClient = newSrpClient
  ) {}

  private async call<T>(target: string, body: object): Promise<T> {
    let response: Response;
    try {
      response = await this.fetchFn(
        `https://cognito-idp.${this.config.region}.amazonaws.com/`,
        {
          method: 'POST',
          headers: {
            'Content-Type': 'application/x-amz-json-1.1',
            'X-Amz-Target': `AWSCognitoIdentityProviderService.${target}`,
          },
          body: JSON.stringify(body),
        }
      );
    } catch {
      throw new CognitoError(
        'NetworkError',
        'Could not reach the sign-in service.'
      );
    }
    const text = await response.text();
    let json: Record<string, unknown> = {};
    try {
      json = text ? JSON.parse(text) : {};
    } catch {
      // Fall through to the generic error below.
    }
    if (!response.ok) {
      const rawType = String(json.__type ?? 'UnknownError');
      const code = rawType.includes('#') ? rawType.split('#').pop()! : rawType;
      throw new CognitoError(
        code,
        String(json.message ?? json.Message ?? code)
      );
    }
    return json as T;
  }

  private tokensFrom(
    result: {
      IdToken: string;
      AccessToken: string;
      RefreshToken?: string;
      ExpiresIn: number;
    },
    refreshToken?: string
  ): CognitoTokens {
    return {
      idToken: result.IdToken,
      accessToken: result.AccessToken,
      refreshToken: result.RefreshToken ?? refreshToken ?? '',
      expiresAt: this.now() + result.ExpiresIn * 1000,
    };
  }

  private handleAuthResponse(
    res: AuthResponse,
    username: string
  ): SignInResult {
    if (res.AuthenticationResult) {
      return {
        kind: 'signedIn',
        tokens: this.tokensFrom(res.AuthenticationResult),
      };
    }
    switch (res.ChallengeName) {
      case 'NEW_PASSWORD_REQUIRED':
        return {kind: 'newPasswordRequired', session: res.Session!, username};
      case 'SOFTWARE_TOKEN_MFA':
        return {kind: 'totpRequired', session: res.Session!, username};
      default:
        throw new CognitoError(
          'UnsupportedChallenge',
          `Sign-in step ${res.ChallengeName} is not supported in the app yet. Please sign in on the website.`
        );
    }
  }

  async signIn(email: string, password: string): Promise<SignInResult> {
    const username = email.trim().toLowerCase();
    const srp = this.srpFactory();
    const init = await this.call<AuthResponse>('InitiateAuth', {
      AuthFlow: 'USER_SRP_AUTH',
      ClientId: this.config.clientId,
      AuthParameters: {USERNAME: username, SRP_A: srp.A.toString(16)},
    });
    if (
      init.ChallengeName !== 'PASSWORD_VERIFIER' ||
      !init.ChallengeParameters
    ) {
      return this.handleAuthResponse(init, username);
    }
    const p = init.ChallengeParameters;
    const {timestamp, signature} = await passwordVerifier(srp, {
      userPoolId: this.config.userPoolId,
      userIdForSrp: p.USER_ID_FOR_SRP,
      password,
      saltHex: p.SALT,
      srpBHex: p.SRP_B,
      secretBlockB64: p.SECRET_BLOCK,
    });
    const res = await this.call<AuthResponse>('RespondToAuthChallenge', {
      ChallengeName: 'PASSWORD_VERIFIER',
      ClientId: this.config.clientId,
      ChallengeResponses: {
        USERNAME: p.USER_ID_FOR_SRP,
        PASSWORD_CLAIM_SECRET_BLOCK: p.SECRET_BLOCK,
        PASSWORD_CLAIM_SIGNATURE: signature,
        TIMESTAMP: timestamp,
      },
      ...(init.Session ? {Session: init.Session} : {}),
    });
    return this.handleAuthResponse(res, p.USER_ID_FOR_SRP);
  }

  async completeNewPassword(
    username: string,
    session: string,
    newPassword: string
  ): Promise<SignInResult> {
    const res = await this.call<AuthResponse>('RespondToAuthChallenge', {
      ChallengeName: 'NEW_PASSWORD_REQUIRED',
      ClientId: this.config.clientId,
      Session: session,
      ChallengeResponses: {USERNAME: username, NEW_PASSWORD: newPassword},
    });
    return this.handleAuthResponse(res, username);
  }

  async submitTotp(
    username: string,
    session: string,
    code: string
  ): Promise<SignInResult> {
    const res = await this.call<AuthResponse>('RespondToAuthChallenge', {
      ChallengeName: 'SOFTWARE_TOKEN_MFA',
      ClientId: this.config.clientId,
      Session: session,
      ChallengeResponses: {
        USERNAME: username,
        SOFTWARE_TOKEN_MFA_CODE: code.trim(),
      },
    });
    return this.handleAuthResponse(res, username);
  }

  async refresh(refreshToken: string): Promise<CognitoTokens> {
    const res = await this.call<AuthResponse>('InitiateAuth', {
      AuthFlow: 'REFRESH_TOKEN_AUTH',
      ClientId: this.config.clientId,
      AuthParameters: {REFRESH_TOKEN: refreshToken},
    });
    if (!res.AuthenticationResult) {
      throw new CognitoError(
        'NotAuthorizedException',
        'Session expired. Please sign in again.'
      );
    }
    return this.tokensFrom(res.AuthenticationResult, refreshToken);
  }

  /** Revokes the refresh token server-side. Errors are ignored: sign-out must always succeed. */
  async revoke(refreshToken: string): Promise<void> {
    try {
      await this.call('RevokeToken', {
        Token: refreshToken,
        ClientId: this.config.clientId,
      });
    } catch {
      // Best effort.
    }
  }
}

interface AuthResponse {
  AuthenticationResult?: {
    IdToken: string;
    AccessToken: string;
    RefreshToken?: string;
    ExpiresIn: number;
  };
  ChallengeName?: string;
  ChallengeParameters?: Record<string, string>;
  Session?: string;
}
