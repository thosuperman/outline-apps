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
 * Typed client for the ProxyGridVPN API (backend/handlers/vpn.py and console.py in the
 * ProxyGrid repository). Shapes mirror the backend responses exactly.
 */

export const DEFAULT_API_BASE = 'https://api.proxygrid.net';

/** The regions ProxyGrid plans to serve, in display order. */
export const PROXYGRID_REGIONS: ReadonlyArray<{region: string; label: string}> =
  [
    {region: 'ap-northeast-1', label: 'Tokyo'},
    {region: 'ap-northeast-2', label: 'Seoul'},
    {region: 'us-east-1', label: 'Virginia'},
    {region: 'eu-central-1', label: 'Frankfurt'},
  ];

export interface VpnKeySummary {
  key_id: string;
  region: string;
  status: 'active' | 'suspended_quota' | 'suspended_admin' | 'revoked' | string;
  created_at?: string;
}

export interface VpnRegionStatus {
  region: string;
  label: string;
  node_id: string;
  node_status: 'active' | 'down' | string;
  key: VpnKeySummary | null;
}

export interface VpnStatus {
  enabled: boolean;
  suspended: boolean;
  plan: {
    id: string;
    name: string;
    quota_bytes: number;
    period_start: string;
    period_end: string;
  };
  used_bytes: number;
  remaining_bytes: number;
  over_quota: boolean;
  regions: VpnRegionStatus[];
  reactivated: boolean;
  queued_plan: {plan: string; period_start: string; period_end: string} | null;
}

export interface VpnKeyAccess {
  key: VpnKeySummary;
  /** Dynamic key, ssconf://api.proxygrid.net/vpn/c/<token>#name. Preferred: survives failover. */
  ssconf_url: string;
  /** Static key for the current node, when the node has an address. */
  ss_url?: string;
}

export interface VpnPlan {
  id: string;
  name: string;
  quota_gb: number;
  price_usd: number;
  days?: number;
}

export interface PublicPricing {
  currency: string;
  vpn: {
    plans: VpnPlan[];
    regions: Array<{
      region: string;
      label: string;
      status: 'available' | 'degraded' | 'coming_soon';
    }>;
  };
}

export class ProxyGridApiError extends Error {
  constructor(
    readonly status: number,
    message: string
  ) {
    super(message);
    this.name = 'ProxyGridApiError';
  }

  get isAuthError() {
    return this.status === 401 || this.status === 403;
  }
}

type FetchFn = typeof fetch;

export class ProxyGridApi {
  constructor(
    private readonly getIdToken: () => Promise<string | null>,
    private readonly base: string = DEFAULT_API_BASE,
    private readonly fetchFn: FetchFn = (...args) => fetch(...args),
    private readonly timeoutMs = 15000
  ) {}

  private async request<T>(
    method: string,
    path: string,
    {body, auth = true}: {body?: unknown; auth?: boolean} = {}
  ): Promise<T> {
    const headers: Record<string, string> = {Accept: 'application/json'};
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (auth) {
      const token = await this.getIdToken();
      if (!token) throw new ProxyGridApiError(401, 'Not signed in.');
      headers.Authorization = `Bearer ${token}`;
    }
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);
    let response: Response;
    try {
      response = await this.fetchFn(`${this.base}${path}`, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: controller.signal,
      });
    } catch {
      throw new ProxyGridApiError(
        0,
        'Could not reach ProxyGrid. Check your connection.'
      );
    } finally {
      clearTimeout(timer);
    }
    const text = await response.text();
    let json: unknown = undefined;
    try {
      json = text ? JSON.parse(text) : undefined;
    } catch {
      // Non-JSON body; handled below.
    }
    if (!response.ok) {
      const obj = (json ?? {}) as {error?: unknown; message?: unknown};
      const nested = (obj.error ?? {}) as {message?: unknown};
      const msg =
        typeof obj.error === 'string'
          ? obj.error
          : typeof nested.message === 'string'
            ? nested.message
            : typeof obj.message === 'string'
              ? obj.message
              : `Request failed (${response.status}).`;
      throw new ProxyGridApiError(response.status, msg);
    }
    return json as T;
  }

  status(): Promise<VpnStatus> {
    return this.request('GET', '/vpn/status');
  }

  createKey(region: string): Promise<VpnKeyAccess> {
    return this.request('POST', '/vpn/keys', {body: {region}});
  }

  keyAccess(keyId: string): Promise<VpnKeyAccess> {
    return this.request('GET', `/vpn/keys/${encodeURIComponent(keyId)}/access`);
  }

  revokeKey(keyId: string): Promise<{key_id: string; status: string}> {
    return this.request('DELETE', `/vpn/keys/${encodeURIComponent(keyId)}`);
  }

  plans(): Promise<{plans: VpnPlan[]}> {
    return this.request('GET', '/vpn/plans');
  }

  publicPricing(): Promise<PublicPricing> {
    return this.request('GET', '/public/pricing', {auth: false});
  }
}

/**
 * Regions to show: the planned regions in display order, plus any region the backend reports
 * that this build does not know yet, labelled as the backend labels it.
 */
export function regionList(
  status: VpnStatus | null | undefined
): Array<{region: string; label: string}> {
  const out = PROXYGRID_REGIONS.map(r => ({
    region: r.region,
    label: status?.regions.find(s => s.region === r.region)?.label ?? r.label,
  }));
  for (const r of status?.regions ?? []) {
    if (!out.some(o => o.region === r.region)) {
      out.push({region: r.region, label: r.label || r.region});
    }
  }
  return out;
}

export function regionLabel(
  region: string,
  status: VpnStatus | null | undefined
): string {
  return regionList(status).find(r => r.region === region)?.label ?? region;
}

/** True for keys served by the ProxyGrid dynamic-key endpoint. */
export function isProxyGridKey(accessKey: string): boolean {
  try {
    const url = new URL(accessKey.trim().replace(/^ssconf:\/\//i, 'https://'));
    return (
      /^ssconf:/i.test(accessKey.trim()) &&
      url.hostname === 'api.proxygrid.net' &&
      url.pathname.startsWith('/vpn/c/')
    );
  } catch {
    return false;
  }
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = bytes;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}
