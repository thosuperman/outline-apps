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
  CognitoClient,
  CognitoError,
  CognitoTokens,
  SignInResult,
} from './cognito';

/** Storage name of the persisted session (kept in the OS keychain/keystore). */
export const PROXYGRID_SESSION_STORAGE_NAME = 'proxygrid_session';

/** Refresh the id token when it has less than this left. */
const REFRESH_MARGIN_MS = 2 * 60 * 1000;

interface StoredSession {
  email: string;
  tokens: CognitoTokens;
}

export type SessionState =
  | {kind: 'signedOut'}
  | {kind: 'signedIn'; email: string}
  | {kind: 'challenge'; challenge: 'newPassword' | 'totp'; email: string};

/**
 * Owns the ProxyGrid Cognito session: sign-in and its challenges, persistence of the tokens in
 * secret storage, and single-flight refresh of the id token used by the API.
 */
export class ProxyGridSession {
  private stored: StoredSession | null = null;
  private pendingChallenge: {
    kind: 'newPassword' | 'totp';
    session: string;
    username: string;
    email: string;
  } | null = null;
  private refreshing: Promise<string | null> | null = null;
  private listeners = new Set<(s: SessionState) => void>();

  constructor(
    private readonly cognito: CognitoClient,
    private readonly storage: Storage,
    private readonly now: () => number = Date.now
  ) {
    try {
      const raw = storage.getItem(PROXYGRID_SESSION_STORAGE_NAME);
      if (raw) {
        const parsed = JSON.parse(raw) as StoredSession;
        if (parsed?.tokens?.refreshToken && parsed.email) {
          this.stored = parsed;
        }
      }
    } catch {
      this.stored = null;
    }
  }

  get state(): SessionState {
    if (this.pendingChallenge) {
      return {
        kind: 'challenge',
        challenge: this.pendingChallenge.kind,
        email: this.pendingChallenge.email,
      };
    }
    return this.stored
      ? {kind: 'signedIn', email: this.stored.email}
      : {kind: 'signedOut'};
  }

  onChange(listener: (s: SessionState) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private emit() {
    const s = this.state;
    for (const l of this.listeners) l(s);
  }

  private persist(email: string, tokens: CognitoTokens) {
    this.stored = {email, tokens};
    this.storage.setItem(
      PROXYGRID_SESSION_STORAGE_NAME,
      JSON.stringify(this.stored)
    );
  }

  private handle(result: SignInResult, email: string): SessionState {
    if (result.kind === 'signedIn') {
      this.pendingChallenge = null;
      this.persist(email, result.tokens);
    } else {
      this.pendingChallenge = {
        kind: result.kind === 'newPasswordRequired' ? 'newPassword' : 'totp',
        session: result.session,
        username: result.username,
        email,
      };
    }
    this.emit();
    return this.state;
  }

  async signIn(email: string, password: string): Promise<SessionState> {
    email = email.trim().toLowerCase();
    return this.handle(await this.cognito.signIn(email, password), email);
  }

  async answerChallenge(answer: string): Promise<SessionState> {
    const c = this.pendingChallenge;
    if (!c) throw new Error('No sign-in step is pending.');
    const result =
      c.kind === 'newPassword'
        ? await this.cognito.completeNewPassword(c.username, c.session, answer)
        : await this.cognito.submitTotp(c.username, c.session, answer);
    return this.handle(result, c.email);
  }

  cancelChallenge() {
    this.pendingChallenge = null;
    this.emit();
  }

  async signOut(): Promise<void> {
    const refreshToken = this.stored?.tokens.refreshToken;
    this.stored = null;
    this.pendingChallenge = null;
    this.storage.removeItem(PROXYGRID_SESSION_STORAGE_NAME);
    this.emit();
    if (refreshToken) await this.cognito.revoke(refreshToken);
  }

  /**
   * Returns a valid id token, refreshing it if needed. Returns null when signed out. A refresh
   * rejected by Cognito (revoked or expired session) signs the user out; a network failure
   * keeps the session and throws, so the caller can retry later.
   */
  async getIdToken(): Promise<string | null> {
    const s = this.stored;
    if (!s) return null;
    if (s.tokens.expiresAt - this.now() > REFRESH_MARGIN_MS) {
      return s.tokens.idToken;
    }
    if (!this.refreshing) {
      this.refreshing = (async () => {
        try {
          const tokens = await this.cognito.refresh(s.tokens.refreshToken);
          if (this.stored === s) this.persist(s.email, tokens);
          return tokens.idToken;
        } catch (e) {
          if (
            e instanceof CognitoError &&
            e.code !== 'NetworkError' &&
            this.stored === s
          ) {
            this.stored = null;
            this.storage.removeItem(PROXYGRID_SESSION_STORAGE_NAME);
            this.emit();
            return null;
          }
          throw e;
        } finally {
          this.refreshing = null;
        }
      })();
    }
    return this.refreshing;
  }

  /** Forces the next getIdToken to refresh (after the API rejected the token). */
  invalidateIdToken() {
    if (this.stored) {
      this.stored = {
        ...this.stored,
        tokens: {...this.stored.tokens, expiresAt: 0},
      };
    }
  }
}
