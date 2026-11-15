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
  isProxyGridKey,
  PROXYGRID_REGIONS,
  ProxyGridApi,
  regionLabel,
  ProxyGridApiError,
  VpnStatus,
} from './api';
import {ProxyGridSession, SessionState} from './session';
import {ServerAlreadyAdded} from '../../model/errors';

/** What the UI renders for the ProxyGrid tab. */
export interface ProxyGridAccountState {
  session: SessionState;
  status: VpnStatus | null;
  loading: boolean;
  error: string | null;
  selectedRegion: string;
  /** Server id of the ProxyGrid key for the selected region, once provisioned. */
  serverId: string | null;
  /** True while the status is older than one refresh interval (offline, API down). */
  stale: boolean;
}

/** The subset of the server repository the account needs. */
export interface AccountServerStore {
  add(accessKey: string): Promise<void>;
  forget(serverId: string): Promise<void>;
  getAll(): Array<{id: string; name: string}>;
  getAccessKey(serverId: string): string | undefined;
}

export interface AccountPreferences {
  get(name: string): string | null;
  set(name: string, value: string): void;
}

const SELECTED_REGION_PREF = 'proxygrid_region';
export const STATUS_REFRESH_MS = 60 * 1000;

/**
 * Glue between the ProxyGrid session/API and the app's server list. A ProxyGrid region is just
 * an Outline dynamic key (ssconf://api.proxygrid.net/vpn/c/<token>) in the server repository,
 * so connecting, reconnecting and status reporting use the same tunnel code as any other key.
 * The dynamic key also gives server-side failover: if the region's node goes down, the next
 * connect is served by the user's key in another healthy region.
 */
export class ProxyGridAccount {
  private state: ProxyGridAccountState;
  private listeners = new Set<(s: ProxyGridAccountState) => void>();
  private lastStatusAt = 0;
  private refreshTimer: ReturnType<typeof setInterval> | null = null;
  private inflight: Promise<void> | null = null;

  constructor(
    private readonly session: ProxyGridSession,
    private readonly api: ProxyGridApi,
    private readonly servers: AccountServerStore,
    private readonly prefs: AccountPreferences,
    private readonly now: () => number = Date.now
  ) {
    const region =
      prefs.get(SELECTED_REGION_PREF) ?? PROXYGRID_REGIONS[0].region;
    this.state = {
      session: session.state,
      status: null,
      loading: false,
      error: null,
      selectedRegion: region,
      serverId: null,
      stale: false,
    };
    session.onChange(s => {
      this.update({session: s});
      if (s.kind === 'signedIn') void this.refresh();
    });
  }

  get current(): ProxyGridAccountState {
    return this.state;
  }

  onChange(listener: (s: ProxyGridAccountState) => void): () => void {
    this.listeners.add(listener);
    listener(this.state);
    return () => this.listeners.delete(listener);
  }

  private update(patch: Partial<ProxyGridAccountState>) {
    this.state = {...this.state, ...patch};
    for (const l of this.listeners) l(this.state);
  }

  /** Starts periodic status refresh (call when the app is in the foreground). */
  start() {
    if (this.refreshTimer) return;
    void this.refresh();
    this.refreshTimer = setInterval(() => {
      void this.refresh();
    }, STATUS_REFRESH_MS);
  }

  stop() {
    if (this.refreshTimer) clearInterval(this.refreshTimer);
    this.refreshTimer = null;
  }

  private async withAuthRetry<T>(fn: () => Promise<T>): Promise<T> {
    try {
      return await fn();
    } catch (e) {
      if (e instanceof ProxyGridApiError && e.status === 401) {
        // The id token may have been revoked or expired early; refresh once and retry.
        this.session.invalidateIdToken();
        return await fn();
      }
      throw e;
    }
  }

  /** Refreshes plan, usage and region health. Concurrent calls share one request. */
  refresh(): Promise<void> {
    if (this.session.state.kind !== 'signedIn') {
      this.update({status: null, serverId: null, error: null, stale: false});
      return Promise.resolve();
    }
    if (!this.inflight) {
      this.inflight = (async () => {
        this.update({loading: true});
        try {
          const status = await this.withAuthRetry(() => this.api.status());
          this.lastStatusAt = this.now();
          // Only some regions may be live (Tokyo first). If the saved choice is not one of
          // them, move to the first live region so one tap connects.
          const live = status.regions.filter(r => r.node_status === 'active');
          if (
            live.length > 0 &&
            !status.regions.some(r => r.region === this.state.selectedRegion)
          ) {
            this.prefs.set(SELECTED_REGION_PREF, live[0].region);
            this.state = {...this.state, selectedRegion: live[0].region};
          }
          this.update({
            status,
            error: null,
            stale: false,
            serverId: this.findServerForRegion(this.state.selectedRegion),
          });
        } catch (e) {
          const stale =
            this.now() - this.lastStatusAt > STATUS_REFRESH_MS * 2 ||
            !this.state.status;
          this.update({error: errorMessage(e), stale});
        } finally {
          this.update({loading: false});
          this.inflight = null;
        }
      })();
    }
    return this.inflight;
  }

  /** Shows an error (for example a failed sign-in) in the ProxyGrid tab. */
  reportError(message: string | null) {
    this.update({error: message});
  }

  selectRegion(region: string) {
    this.prefs.set(SELECTED_REGION_PREF, region);
    this.update({
      selectedRegion: region,
      serverId: this.findServerForRegion(region),
    });
  }

  private proxyGridServers(): Array<{id: string; name: string; key: string}> {
    const out = [];
    for (const s of this.servers.getAll()) {
      const key = this.servers.getAccessKey(s.id);
      if (key && isProxyGridKey(key)) out.push({id: s.id, name: s.name, key});
    }
    return out;
  }

  private regionKeyName(region: string): string {
    return `ProxyGrid ${regionLabel(region, this.state.status)}`;
  }

  private findServerForRegion(region: string): string | null {
    const name = this.regionKeyName(region);
    // The backend names the dynamic key "ProxyGrid <Region>" in the URL fragment.
    const match = this.proxyGridServers().find(
      s =>
        s.name === name ||
        decodeURIComponent(s.key.split('#')[1] ?? '') === name
    );
    return match?.id ?? null;
  }

  /**
   * Makes sure the selected region has a key in the server list and returns its server id.
   * Reuses the user's existing key for the region; creates one only if there is none.
   */
  async ensureRegionServer(): Promise<string> {
    const region = this.state.selectedRegion;
    const regionStatus = this.state.status?.regions.find(
      r => r.region === region
    );
    // A suspended key fails at the dynamic-key fetch; say why up front instead of retrying.
    if (isQuotaBlocked(this.state.status, region)) {
      throw new Error(QUOTA_MESSAGE);
    }
    if (regionStatus?.key?.status === 'suspended_admin') {
      throw new Error(
        'VPN access for this account is paused. Contact ProxyGrid support.'
      );
    }
    const existing = this.findServerForRegion(region);
    if (existing) return existing;
    if (this.session.state.kind !== 'signedIn') {
      throw new Error('Sign in to ProxyGrid first.');
    }
    // Reuse the existing key (key creation is rate limited per day); create one only when
    // the region has none, or the listed key has gone (revoked between refreshes).
    const keyId =
      regionStatus?.key && regionStatus.key.status !== 'revoked'
        ? regionStatus.key.key_id
        : null;
    const access = await this.withAuthRetry(async () => {
      if (keyId) {
        try {
          return await this.api.keyAccess(keyId);
        } catch (e) {
          if (!(e instanceof ProxyGridApiError && e.status === 404)) throw e;
        }
      }
      return this.api.createKey(region);
    });
    try {
      await this.servers.add(access.ssconf_url);
    } catch (e) {
      // Already in the list (same token): fall through to the lookup.
      if (!(e instanceof ServerAlreadyAdded)) throw e;
    }
    const id = this.findServerForRegion(region);
    if (!id) throw new Error('Could not add the ProxyGrid key.');
    this.update({serverId: id});
    void this.refresh();
    return id;
  }

  /** Signs out and removes every ProxyGrid key from the device. */
  async signOut(disconnect: (serverId: string) => Promise<void>) {
    for (const s of this.proxyGridServers()) {
      try {
        await disconnect(s.id);
      } catch (e) {
        console.warn('disconnect before sign-out failed', e);
      }
      await this.servers.forget(s.id);
    }
    await this.session.signOut();
    this.update({status: null, serverId: null, error: null});
  }

  /** Ids of servers that belong to ProxyGrid, so the own-server list can hide them. */
  proxyGridServerIds(): Set<string> {
    return new Set(this.proxyGridServers().map(s => s.id));
  }
}

export const QUOTA_MESSAGE =
  'Your data allowance for this period is used up. Upgrade your plan or wait for the next period.';

/** True when the plan is over quota or the region's key is suspended for quota. */
export function isQuotaBlocked(
  status: VpnStatus | null | undefined,
  region: string
): boolean {
  if (!status) return false;
  if (status.over_quota) return true;
  const key = status.regions.find(r => r.region === region)?.key;
  return key?.status === 'suspended_quota';
}

function errorMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}
