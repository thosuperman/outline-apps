// Copyright 2018 The Outline Authors
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

import '../ui_components/app-root.js';

import {Localizer} from '@outline/infrastructure/i18n';
import {makeConfig, SIP002_URI} from 'ShadowsocksConfig';

import {App} from './app';
import {BRAND} from './brand';
import {EnvironmentVariables, onceEnvVars} from './environment';
import {newOutlineServerRepository} from './outline_server_repository';
import {
  FAKE_BROKEN_HOSTNAME,
  FAKE_UNREACHABLE_HOSTNAME,
  FakeVpnApi,
} from './outline_server_repository/vpn.fake';
import {OutlinePlatform} from './platform';
import {ProxyGridAccount} from './proxygrid/account';
import {ProxyGridApi} from './proxygrid/api';
import {CognitoClient, CognitoError} from './proxygrid/cognito';
import {
  PROXYGRID_SESSION_STORAGE_NAME,
  ProxyGridSession,
} from './proxygrid/session';
import {SecureStorage} from './secure_storage';
import {Settings} from './settings';
import {EventQueue} from '../model/events';
import {ServerRepository} from '../model/server.js';

// Used to determine whether to use Polymer functionality on app initialization failure.
let webComponentsAreReady = false;
document.addEventListener('WebComponentsReady', () => {
  console.debug('received WebComponentsReady event');
  webComponentsAreReady = true;
});

// Used to delay loading the app until (translation) resources have been loaded. This can happen a
// little later than WebComponentsReady.
const oncePolymerIsReady = new Promise<void>(resolve => {
  document.addEventListener('app-localize-resources-loaded', () => {
    console.debug('received app-localize-resources-loaded event');
    resolve();
  });
});

// Helpers

// Do not call until WebComponentsReady has fired!
function getRootEl() {
  return document.querySelector('app-root') as {} as polymer.Base;
}

// Storage names that hold secrets: access keys (server list, v1 and legacy v0) and the
// ProxyGrid session. They live in the OS keychain/keystore when the platform has one.
const SECRET_STORAGE_NAMES = [
  'servers_v1',
  'servers',
  PROXYGRID_SESSION_STORAGE_NAME,
];

/**
 * Returns the Storage for secrets: the OS keychain/keystore when available and working, with a
 * one-time migration out of local storage; otherwise local storage, as before.
 */
export async function createSecretStorage(
  platform: OutlinePlatform
): Promise<Storage> {
  const backend = platform.getSecretBackend?.();
  if (!backend) {
    return window.localStorage;
  }
  try {
    // Probe: fails when the OS store is unavailable (e.g. Linux without a keyring).
    await backend.get('__probe__');
  } catch (e) {
    console.warn('OS secret storage unavailable, using local storage', e);
    return window.localStorage;
  }
  return SecureStorage.load(backend, SECRET_STORAGE_NAMES, window.localStorage);
}

async function createServerRepo(
  platform: OutlinePlatform,
  eventQueue: EventQueue,
  secretStorage: Storage
): Promise<ServerRepository> {
  const localize = getLocalizationFunction();
  const vpnApi = platform.getVpnApi();
  if (vpnApi) {
    return await newOutlineServerRepository(
      vpnApi,
      eventQueue,
      secretStorage,
      localize
    );
  }

  console.debug('Platform not supported, using fake servers.');
  const repo = await newOutlineServerRepository(
    new FakeVpnApi(),
    eventQueue,
    window.localStorage,
    localize
  );

  if (repo.getAll().length === 0) {
    repo.add(
      SIP002_URI.stringify(
        makeConfig({
          host: '127.0.0.1',
          port: 123,
          method: 'chacha20-ietf-poly1305',
          tag: 'Fake Working Server',
        })
      )
    );
    repo.add(
      SIP002_URI.stringify(
        makeConfig({
          host: FAKE_BROKEN_HOSTNAME,
          port: 123,
          method: 'chacha20-ietf-poly1305',
          tag: 'Fake Broken Server',
        })
      )
    );
    repo.add(
      SIP002_URI.stringify(
        makeConfig({
          host: FAKE_UNREACHABLE_HOSTNAME,
          port: 123,
          method: 'chacha20-ietf-poly1305',
          tag: 'Fake Unreachable Server',
        })
      )
    );
  }
  return repo;
}

export function main(platform: OutlinePlatform) {
  return Promise.all([onceEnvVars, oncePolymerIsReady]).then(
    async ([environmentVars]) => {
      console.debug('running main() function');

      const queryParams = new URL(document.URL).searchParams;
      const debugMode = queryParams.get('debug') === 'true';

      const eventQueue = new EventQueue();
      const secretStorage = await createSecretStorage(platform);
      const serverRepo = await createServerRepo(
        platform,
        eventQueue,
        secretStorage
      );
      const settings = new Settings();
      const app = new App(
        eventQueue,
        serverRepo,
        getRootEl(),
        debugMode,
        platform.getUrlInterceptor(),
        platform.getClipboard(),
        platform.getErrorReporter(environmentVars),
        settings,
        environmentVars,
        platform.getUpdater(),
        platform.getVpnServiceInstaller(),
        platform.quitApplication
      );
      const {session, account} = createProxyGrid(
        environmentVars,
        secretStorage,
        serverRepo
      );
      app.attachProxyGrid(account, session);
    },
    e => {
      onUnexpectedError(e);
    }
  );
}

function createProxyGrid(
  env: EnvironmentVariables,
  secretStorage: Storage,
  serverRepo: ServerRepository
) {
  const configured = !!(
    env.PROXYGRID_COGNITO_REGION &&
    env.PROXYGRID_USER_POOL_ID &&
    env.PROXYGRID_USER_POOL_CLIENT_ID
  );
  const cognito = new CognitoClient(
    {
      region: env.PROXYGRID_COGNITO_REGION || 'invalid',
      userPoolId: env.PROXYGRID_USER_POOL_ID || 'invalid_invalid',
      clientId: env.PROXYGRID_USER_POOL_CLIENT_ID || '',
    },
    configured
      ? undefined
      : async () => {
          throw new CognitoError(
            'NotConfigured',
            'ProxyGrid sign-in is not configured in this build.'
          );
        }
  );
  const session = new ProxyGridSession(cognito, secretStorage);
  const api = new ProxyGridApi(
    () => session.getIdToken(),
    env.PROXYGRID_API_URL || undefined
  );
  const account = new ProxyGridAccount(session, api, serverRepo, {
    get: name => window.localStorage.getItem(name),
    set: (name, value) => window.localStorage.setItem(name, value),
  });
  return {session, account};
}

function onUnexpectedError(error: Error) {
  const rootEl = getRootEl();
  if (webComponentsAreReady && rootEl && rootEl.localize) {
    const localize = rootEl.localize.bind(rootEl);
    rootEl.showToast(localize('error-unexpected'), 120000);
  } else {
    // Something went terribly wrong (i.e. Polymer failed to initialize). Provide some messaging to
    // the user, even if we are not able to display it in a toast or localize it.
    alert(
      `An unexpected error occurred. Please contact ${BRAND.supportEmail} for assistance.`
    );
  }
  console.error(error);
}

// Returns Polymer's localization function. Must be called after WebComponentsReady has fired.
export function getLocalizationFunction(): Localizer {
  const rootEl = getRootEl();
  if (!rootEl) {
    return null;
  }
  return rootEl.localize;
}
