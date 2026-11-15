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

import {Localizer} from '@outline/infrastructure/i18n';
import {css, html, LitElement, nothing, svg} from 'lit';
import {customElement, property, state} from 'lit/decorators.js';
import {classMap} from 'lit/directives/class-map.js';

import {BRAND} from '../../app/brand';
import {
  isQuotaBlocked,
  type ProxyGridAccountState,
} from '../../app/proxygrid/account';
import {formatBytes, regionLabel, regionList} from '../../app/proxygrid/api';
import {ServerConnectionState} from '../servers_view/server_connection_indicator';
import type {ServerListItem} from '../servers_view/server_list_item';

export type HomeMode = 'proxygrid' | 'own';

/** Dispatches a bubbling, composed event that App listens for on app-root. */
function emit(el: HTMLElement, name: string, detail: object = {}) {
  el.dispatchEvent(
    new CustomEvent(name, {detail, bubbles: true, composed: true})
  );
}

// The ProxyGridVPN mark, identical to the website's VpnMark (proxygrid frontend/src/ui).
export const APP_MARK = svg`
  <svg viewBox="0 0 48 48" aria-hidden="true">
    <rect x="2" y="2" width="44" height="44" rx="12" fill="#5b5bd6"/>
    <g stroke="#fff" stroke-width="2.6" stroke-linecap="round" opacity="0.92">
      <path d="M17 12v24M31 12v24M12 17h24M12 31h24"/>
    </g>
    <circle cx="31" cy="31" r="5" fill="#0e9f6e" stroke="#fff" stroke-width="2.2"/>
  </svg>`;

/**
 * The ProxyGridVPN home screen: one large connect control, a region picker with live health and the
 * plan's data usage for ProxyGrid, and the user's own Outline-compatible servers in a second
 * tab (the existing server list is slotted in as "own-servers").
 */
@customElement('home-view')
export class HomeView extends LitElement {
  @property({type: Object}) localize: Localizer = (k: string) => k;
  @property({type: Array}) servers: ServerListItem[] = [];
  @property({type: Object}) account: ProxyGridAccountState | null = null;
  @property({type: String}) mode: HomeMode = 'proxygrid';
  @property({type: Boolean}) busy = false;

  @state() private email = '';
  @state() private password = '';
  @state() private challengeAnswer = '';

  static styles = css`
    :host {
      display: flex;
      flex-direction: column;
      align-items: center;
      width: 100%;
      box-sizing: border-box;
      padding: 12px 16px calc(24px + env(safe-area-inset-bottom, 0px));
      font-family: var(--outline-font-family);
      color: var(--outline-text-color);
      background: var(--outline-background);
      -webkit-font-smoothing: antialiased;
    }
    .column {
      width: 100%;
      max-width: 420px;
      display: flex;
      flex-direction: column;
      gap: 16px;
    }
    .tabs {
      display: grid;
      grid-template-columns: 1fr 1fr;
      padding: 4px;
      gap: 4px;
      border-radius: var(--grid-radius-lg);
      background: var(--grid-surface-inset);
    }
    .tabs button {
      font: inherit;
      font-size: 0.875rem;
      font-weight: 600;
      border: 0;
      border-radius: var(--grid-radius-md);
      padding: 9px 8px;
      background: transparent;
      color: var(--outline-label-color);
      cursor: pointer;
    }
    .tabs button[aria-selected='true'] {
      background: var(--outline-card-background);
      color: var(--outline-text-color);
      box-shadow: var(--outline-elevation);
    }
    .card {
      background: var(--outline-card-background);
      border: var(--outline-hairline);
      border-radius: var(--grid-radius-lg);
      padding: 16px;
    }
    .hero {
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: 14px;
      padding: 20px 16px 22px;
      text-align: center;
    }
    .power {
      --ring: var(--grid-ink-300);
      position: relative;
      width: 168px;
      height: 168px;
      border-radius: 50%;
      border: 0;
      cursor: pointer;
      background: radial-gradient(
        circle at 50% 40%,
        var(--outline-card-background) 0 60%,
        var(--grid-surface-subtle) 61%
      );
      box-shadow:
        0 0 0 10px var(--grid-surface-inset),
        0 0 0 12px var(--ring),
        var(--outline-elevation);
      color: var(--outline-label-color);
      display: grid;
      place-items: center;
      transition:
        box-shadow 240ms ease,
        color 240ms ease,
        transform 120ms ease;
    }
    .power:active {
      transform: scale(0.97);
    }
    .power:focus-visible {
      outline: 3px solid var(--grid-vpn);
      outline-offset: 16px;
    }
    .power svg {
      width: 56px;
      height: 56px;
    }
    .power.connected {
      --ring: var(--grid-connected);
      color: var(--grid-connected);
      box-shadow:
        0 0 0 10px var(--grid-connected-soft),
        0 0 0 12px var(--ring),
        0 10px 40px rgb(14 159 110 / 25%);
    }
    .power.pending {
      --ring: var(--grid-vpn);
      color: var(--grid-vpn);
      animation: pulse 1.4s ease-in-out infinite;
    }
    .power[disabled] {
      cursor: not-allowed;
      opacity: 0.55;
    }
    @keyframes pulse {
      50% {
        box-shadow:
          0 0 0 16px var(--grid-vpn-soft),
          0 0 0 18px var(--ring),
          var(--outline-elevation);
      }
    }
    @media (prefers-reduced-motion: reduce) {
      .power.pending {
        animation: none;
      }
    }
    .status {
      font-size: 1.25rem;
      font-weight: 650;
      letter-spacing: -0.01em;
      margin: 4px 0 0;
    }
    .status.connected {
      color: var(--grid-connected);
    }
    .sub {
      color: var(--outline-label-color);
      font-size: 0.875rem;
      margin: 0;
    }
    .section-title {
      font-size: 0.6875rem;
      font-weight: 600;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: var(--outline-label-color);
      margin: 0 0 10px;
    }
    .regions {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 8px;
    }
    .region {
      font: inherit;
      text-align: start;
      display: flex;
      align-items: center;
      gap: 10px;
      padding: 12px;
      border-radius: var(--grid-radius-md);
      border: var(--outline-hairline);
      background: var(--outline-card-background);
      color: var(--outline-text-color);
      cursor: pointer;
    }
    .region[aria-pressed='true'] {
      border-color: var(--grid-vpn);
      box-shadow: inset 0 0 0 1px var(--grid-vpn);
      background: var(--grid-vpn-soft);
    }
    .region[disabled] {
      cursor: not-allowed;
      opacity: 0.5;
    }
    .region .name {
      font-weight: 600;
      font-size: 0.9375rem;
    }
    .region .meta {
      font-size: 0.75rem;
      color: var(--outline-label-color);
    }
    .dot {
      width: 8px;
      height: 8px;
      border-radius: 50%;
      flex: none;
      background: var(--grid-ink-400);
    }
    .dot.up {
      background: var(--grid-connected);
      box-shadow: 0 0 0 3px var(--grid-connected-soft);
    }
    .dot.down {
      background: var(--grid-warning);
    }
    .usage-row {
      display: flex;
      justify-content: space-between;
      align-items: baseline;
      gap: 8px;
    }
    .plan {
      font-weight: 650;
    }
    .numbers {
      font-family: var(--grid-font-mono);
      font-size: 0.8125rem;
      color: var(--outline-label-color);
    }
    .bar {
      height: 8px;
      border-radius: 999px;
      background: var(--grid-surface-inset);
      overflow: hidden;
      margin: 10px 0 8px;
    }
    .bar > span {
      display: block;
      height: 100%;
      border-radius: inherit;
      background: var(--grid-vpn);
      transition: width 300ms ease;
    }
    .bar.warn > span {
      background: var(--grid-warning);
    }
    .bar.full > span {
      background: var(--outline-error);
    }
    .row-actions {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 8px;
      margin-top: 4px;
    }
    a,
    .link {
      color: var(--grid-vpn);
      font-weight: 600;
      text-decoration: none;
      background: none;
      border: 0;
      padding: 0;
      font: inherit;
      cursor: pointer;
    }
    .btn {
      font: inherit;
      font-weight: 600;
      font-size: 0.9375rem;
      width: 100%;
      padding: 12px 16px;
      border: 0;
      border-radius: var(--grid-radius-md);
      background: var(--grid-vpn);
      color: #fff;
      cursor: pointer;
    }
    .btn[disabled] {
      opacity: 0.6;
      cursor: progress;
    }
    .btn.secondary {
      background: transparent;
      color: var(--outline-text-color);
      border: var(--outline-hairline);
    }
    form {
      display: flex;
      flex-direction: column;
      gap: 10px;
    }
    label {
      font-size: 0.8125rem;
      font-weight: 600;
      display: flex;
      flex-direction: column;
      gap: 6px;
    }
    input {
      font: inherit;
      font-size: 1rem;
      padding: 11px 12px;
      border-radius: var(--grid-radius-md);
      border: 1px solid var(--outline-input-border);
      background: var(--outline-input-bg);
      color: var(--outline-input-text);
    }
    input:focus {
      outline: 2px solid var(--grid-vpn);
      outline-offset: 1px;
    }
    .brand {
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: 8px;
      text-align: center;
      padding: 8px 0 4px;
    }
    .brand svg {
      width: 64px;
      height: 64px;
      filter: drop-shadow(0 8px 24px rgb(91 91 214 / 35%));
    }
    .brand h2 {
      margin: 6px 0 0;
      font-size: 1.5rem;
      letter-spacing: -0.03em;
    }
    .error {
      color: var(--outline-error);
      font-size: 0.875rem;
      margin: 0;
    }
    .notice {
      font-size: 0.8125rem;
      color: var(--outline-label-color);
      text-align: center;
      margin: 0;
    }
    .remaining {
      font-weight: 600;
      font-size: 0.875rem;
      margin: 0 0 6px;
    }
    .links {
      display: flex;
      gap: 14px;
    }
    .mode-note {
      display: flex;
      flex-direction: column;
      gap: 4px;
      font-size: 0.8125rem;
      color: var(--outline-label-color);
      padding: 12px 14px;
      border-radius: var(--grid-radius-md);
      background: var(--grid-surface-inset);
    }
    .mode-note strong {
      color: var(--outline-text-color);
      font-size: 0.875rem;
    }
    .alert {
      border-radius: var(--grid-radius-md);
      padding: 10px 12px;
      font-size: 0.875rem;
      background: rgb(217 130 43 / 12%);
      color: var(--outline-text-color);
    }
  `;

  private t(key: string, fallback: string, ...args: string[]): string {
    const v = this.localize(key, ...args);
    return v && v !== key ? v : fallback;
  }

  private get proxyGridServer(): ServerListItem | undefined {
    const id = this.account?.serverId;
    return id ? this.servers.find(s => s.id === id) : undefined;
  }

  render() {
    return html`<div class="column">
      <div class="tabs" role="tablist">
        ${this.tab('proxygrid', BRAND.platformName)}
        ${this.tab(
          'own',
          this.t('proxygridvpn-my-own-server', 'My Own Server')
        )}
      </div>
      ${this.mode === 'proxygrid'
        ? this.renderProxyGrid()
        : this.renderOwnServers()}
    </div>`;
  }

  /** Mode B: the user's own Outline/Shadowsocks servers, independent of ProxyGrid. */
  private renderOwnServers() {
    return html`<div class="mode-note" role="note">
        <strong>${this.t('proxygridvpn-own-title', 'Your own servers')}</strong>
        <span
          >${this.t(
            'proxygridvpn-own-note',
            'Use any Outline or ss:// key. Keys are stored only on this device, never sent to ProxyGrid, and need no subscription.'
          )}</span
        >
      </div>
      <slot name="own-servers"></slot>`;
  }

  private tab(mode: HomeMode, label: string) {
    return html`<button
      role="tab"
      aria-selected=${this.mode === mode ? 'true' : 'false'}
      @click=${() => emit(this, 'HomeModeChanged', {mode})}
    >
      ${label}
    </button>`;
  }

  private renderProxyGrid() {
    const session = this.account?.session ?? {kind: 'signedOut'};
    if (session.kind === 'signedOut') return this.renderSignIn();
    if (session.kind === 'challenge')
      return this.renderChallenge(session.challenge);
    return html`${this.renderConnect()} ${this.renderRegions()}
    ${this.renderUsage()} ${this.renderAccountRow(session.email)}`;
  }

  private renderSignIn() {
    return html`<div class="brand">
        ${APP_MARK}
        <h2>${BRAND.productName}</h2>
        <p class="sub">${BRAND.tagline}</p>
      </div>
      <form
        class="card"
        @submit=${(e: Event) => {
          e.preventDefault();
          emit(this, 'ProxyGridSignIn', {
            email: this.email,
            password: this.password,
          });
          this.password = '';
        }}
      >
        <label
          >${this.t('proxygridvpn-email', 'Email')}
          <input
            type="email"
            autocomplete="username"
            inputmode="email"
            required
            .value=${this.email}
            @input=${(e: InputEvent) =>
              (this.email = (e.target as HTMLInputElement).value)}
        /></label>
        <label
          >${this.t('proxygridvpn-password', 'Password')}
          <input
            type="password"
            autocomplete="current-password"
            required
            .value=${this.password}
            @input=${(e: InputEvent) =>
              (this.password = (e.target as HTMLInputElement).value)}
        /></label>
        ${this.account?.error
          ? html`<p class="error" role="alert">${this.account.error}</p>`
          : nothing}
        <button class="btn" type="submit" ?disabled=${this.busy}>
          ${this.busy
            ? this.t('proxygridvpn-signing-in', 'Signing in…')
            : this.t('proxygridvpn-sign-in', 'Sign in to ProxyGrid')}
        </button>
        <div class="row-actions">
          <a href=${BRAND.forgotPasswordUrl} target="_blank" rel="noopener"
            >${this.t('proxygridvpn-forgot-password', 'Forgot password?')}</a
          >
          <a href=${BRAND.signUpUrl} target="_blank" rel="noopener"
            >${this.t('proxygridvpn-create-account', 'Create free account')}</a
          >
        </div>
      </form>
      <p class="notice">
        ${this.t(
          'proxygridvpn-free-plan-notice',
          'Free plan: 3 GB of VPN data every 30 days.'
        )}
        <button
          class="link"
          @click=${() => emit(this, 'HomeModeChanged', {mode: 'own'})}
        >
          ${this.t('proxygridvpn-use-own-server', 'Have your own server?')}
        </button>
      </p>`;
  }

  private renderChallenge(kind: 'newPassword' | 'totp') {
    const isTotp = kind === 'totp';
    return html`<form
      class="card"
      @submit=${(e: Event) => {
        e.preventDefault();
        emit(this, 'ProxyGridChallenge', {answer: this.challengeAnswer});
        this.challengeAnswer = '';
      }}
    >
      <p class="section-title">
        ${isTotp
          ? this.t('proxygridvpn-totp-title', 'Two-step verification')
          : this.t('proxygridvpn-new-password-title', 'Choose a new password')}
      </p>
      <label
        >${isTotp
          ? this.t(
              'proxygridvpn-totp-label',
              '6-digit code from your authenticator'
            )
          : this.t('proxygridvpn-new-password-label', 'New password')}
        <input
          type=${isTotp ? 'text' : 'password'}
          inputmode=${isTotp ? 'numeric' : 'text'}
          autocomplete=${isTotp ? 'one-time-code' : 'new-password'}
          required
          .value=${this.challengeAnswer}
          @input=${(e: InputEvent) =>
            (this.challengeAnswer = (e.target as HTMLInputElement).value)}
      /></label>
      ${this.account?.error
        ? html`<p class="error" role="alert">${this.account.error}</p>`
        : nothing}
      <button class="btn" type="submit" ?disabled=${this.busy}>
        ${this.t('proxygridvpn-continue', 'Continue')}
      </button>
      <button
        class="btn secondary"
        type="button"
        @click=${() => emit(this, 'ProxyGridCancelChallenge')}
      >
        ${this.t('proxygridvpn-cancel', 'Cancel')}
      </button>
    </form>`;
  }

  private renderConnect() {
    const server = this.proxyGridServer;
    const st = server?.connectionState ?? ServerConnectionState.DISCONNECTED;
    const connected = st === ServerConnectionState.CONNECTED;
    const pending =
      this.busy ||
      st === ServerConnectionState.CONNECTING ||
      st === ServerConnectionState.RECONNECTING ||
      st === ServerConnectionState.DISCONNECTING;
    const overQuota = isQuotaBlocked(
      this.account?.status,
      this.account?.selectedRegion ?? ''
    );
    const region = this.account?.selectedRegion
      ? regionLabel(this.account.selectedRegion, this.account.status)
      : '';
    const statusText = {
      [ServerConnectionState.CONNECTED]: this.t(
        'proxygridvpn-protected',
        'Protected'
      ),
      [ServerConnectionState.CONNECTING]: this.t(
        'proxygridvpn-connecting',
        'Connecting…'
      ),
      [ServerConnectionState.RECONNECTING]: this.t(
        'proxygridvpn-reconnecting',
        'Reconnecting…'
      ),
      [ServerConnectionState.DISCONNECTING]: this.t(
        'proxygridvpn-disconnecting',
        'Disconnecting…'
      ),
      [ServerConnectionState.DISCONNECTED]: this.t(
        'proxygridvpn-not-protected',
        'Not protected'
      ),
    }[st];
    return html`<div class="card hero">
      <button
        class=${classMap({power: true, connected, pending})}
        aria-label=${connected
          ? this.t('proxygridvpn-disconnect', 'Disconnect')
          : this.t('proxygridvpn-connect', 'Connect')}
        ?disabled=${(overQuota && !connected) || pending}
        @click=${() =>
          emit(this, connected ? 'ProxyGridDisconnect' : 'ProxyGridConnect')}
      >
        <svg
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2.2"
          stroke-linecap="round"
          aria-hidden="true"
        >
          <path d="M12 3v8" />
          <path d="M6.3 6.6a8 8 0 1 0 11.4 0" />
        </svg>
      </button>
      <div>
        <p class=${classMap({status: true, connected})} role="status">
          ${statusText}
        </p>
        <p class="sub">
          ${connected
            ? this.t(
                'proxygridvpn-connected-via',
                `via ${region}`,
                'region',
                region
              )
            : region
              ? this.t(
                  'proxygridvpn-tap-to-connect',
                  `Tap to connect · ${region}`,
                  'region',
                  region
                )
              : nothing}
        </p>
      </div>
      ${overQuota && !connected
        ? html`<div class="alert">
            ${this.t(
              'proxygridvpn-over-quota',
              'Your data allowance for this period is used up.'
            )}
            <a href=${BRAND.pricingUrl} target="_blank" rel="noopener"
              >${this.t('proxygridvpn-upgrade', 'Upgrade')}</a
            >
          </div>`
        : nothing}
    </div>`;
  }

  private renderRegions() {
    const statusByRegion = new Map(
      (this.account?.status?.regions ?? []).map(r => [r.region, r])
    );
    return html`<div>
      <p class="section-title">
        ${this.t('proxygridvpn-location', 'Location')}
      </p>
      <div class="regions">
        ${regionList(this.account?.status).map(({region, label}) => {
          const rs = statusByRegion.get(region);
          const up = rs?.node_status === 'active';
          const down = rs?.node_status === 'down';
          const available = !!rs;
          const meta = up
            ? this.t('proxygridvpn-region-online', 'Online')
            : down
              ? this.t('proxygridvpn-region-degraded', 'Degraded')
              : this.t('proxygridvpn-region-soon', 'Coming soon');
          return html`<button
            class="region"
            aria-pressed=${this.account?.selectedRegion === region
              ? 'true'
              : 'false'}
            ?disabled=${!available && !!this.account?.status}
            @click=${() => emit(this, 'ProxyGridSelectRegion', {region})}
          >
            <span class=${classMap({dot: true, up, down})}></span>
            <span>
              <div class="name">${label}</div>
              <div class="meta">${meta}</div>
            </span>
          </button>`;
        })}
      </div>
    </div>`;
  }

  private renderUsage() {
    const s = this.account?.status;
    if (!s) {
      return this.account?.error
        ? html`<p class="error" role="alert">${this.account.error}</p>`
        : nothing;
    }
    const pct = s.plan.quota_bytes
      ? Math.min(100, (s.used_bytes / s.plan.quota_bytes) * 100)
      : 0;
    const renews = new Date(s.plan.period_end);
    const queued = s.queued_plan;
    const queuedName = queued
      ? queued.plan.charAt(0).toUpperCase() + queued.plan.slice(1)
      : '';
    const queuedStart = queued ? new Date(queued.period_start) : null;
    return html`<div class="card">
      ${s.suspended
        ? html`<p class="alert" role="alert">
            ${this.t(
              'proxygridvpn-suspended',
              'This account is suspended. Contact ProxyGrid support.'
            )}
          </p>`
        : nothing}
      <div class="usage-row">
        <span class="plan">${s.plan.name}</span>
        <span class="numbers"
          >${formatBytes(s.used_bytes)} /
          ${formatBytes(s.plan.quota_bytes)}</span
        >
      </div>
      <div
        class=${classMap({
          bar: true,
          warn: pct >= 80 && pct < 100,
          full: pct >= 100,
        })}
        role="progressbar"
        aria-valuemin="0"
        aria-valuemax="100"
        aria-valuenow=${Math.round(pct)}
      >
        <span style="width:${pct}%"></span>
      </div>
      <p class="remaining">
        ${this.t(
          'proxygridvpn-remaining',
          `${formatBytes(s.remaining_bytes)} left this period, shared by all locations`,
          'amount',
          formatBytes(s.remaining_bytes)
        )}
      </p>
      ${queued && queuedStart && !isNaN(queuedStart.getTime())
        ? html`<p class="sub">
            ${this.t(
              'proxygridvpn-queued-plan',
              `Next plan: ${queuedName} from ${queuedStart.toLocaleDateString()}`,
              'plan',
              queuedName,
              'date',
              queuedStart.toLocaleDateString()
            )}
          </p>`
        : nothing}
      <div class="usage-row">
        <span class="sub">
          ${isNaN(renews.getTime())
            ? nothing
            : this.t(
                'proxygridvpn-renews',
                `Resets ${renews.toLocaleDateString()}`,
                'date',
                renews.toLocaleDateString()
              )}
        </span>
        <span class="links">
          <a href=${BRAND.dashboardUrl} target="_blank" rel="noopener"
            >${this.t('proxygridvpn-billing', 'Billing')}</a
          >
          <a href=${BRAND.pricingUrl} target="_blank" rel="noopener"
            >${s.plan.id === 'free'
              ? this.t('proxygridvpn-upgrade', 'Upgrade')
              : this.t('proxygridvpn-manage-plan', 'Manage plan')}</a
          >
        </span>
      </div>
      ${this.account?.stale && this.account.error
        ? html`<p class="error" role="alert">${this.account.error}</p>`
        : nothing}
    </div>`;
  }

  private renderAccountRow(email: string) {
    return html`<div class="row-actions">
      <span class="sub">${email}</span>
      <button class="link" @click=${() => emit(this, 'ProxyGridSignOut')}>
        ${this.t('proxygridvpn-sign-out', 'Sign out')}
      </button>
    </div>`;
  }
}
