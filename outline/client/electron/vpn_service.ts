// Copyright 2024 The Outline Authors
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

import {invokeGoMethod, registerCallback} from './go_plugin';
import {
  StartRequestJson,
  TunnelStatus,
} from '../web/app/outline_server_repository/vpn';

// TODO: Separate this config into LinuxVpnConfig and WindowsVpnConfig. Some fields may share.
interface VpnConfig {
  id: string;
  interfaceName: string;
  connectionName: string;
  ipAddress: string;
  dnsLinkLocalAddress: string;
  routingTableId: number;
  routingPriority: number;
  protectionMark: number;
}

interface EstablishVpnRequestJson {
  vpn: VpnConfig;
  client: string;
}

export async function establishVpn(tsRequest: StartRequestJson) {
  const goRequest: EstablishVpnRequestJson = {
    vpn: {
      id: tsRequest.id,

      // TUN device name. Distinct from the official Outline app ('outline-tun1', and the old
      // 'outline-tun0' left behind by its AppImage build) so both apps can be installed:
      // https://github.com/OutlineFoundation/outline-apps/blob/client/linux/v1.14.0/client/electron/linux_proxy_controller/outline_proxy_controller.h#L203
      interfaceName: 'proxygrid-tun1',

      // Network Manager connection name, Use "TUN Connection" instead of "VPN Connection"
      // because Network Manager has a dedicated "VPN Connection" concept that we did not implement
      connectionName: 'ProxyGridVPN TUN Connection',

      // TUN IP. Outline uses 10.0.85.x ('10.0.85.5', and the old AppImage's '10.0.85.1'):
      // https://github.com/OutlineFoundation/outline-apps/blob/client/linux/v1.14.0/client/electron/linux_proxy_controller/outline_proxy_controller.h#L204
      ipAddress: '10.0.86.5',

      // A "fake" local DNS resolver. Outline will intercept the real resolver at this address.
      // Must align with: client/go/outline/configregistry/outline_dns_intercept.go
      dnsLinkLocalAddress: '169.254.113.53',

      // Distinct from the official Outline app's routing table (7113) and mark (0x711E), so the
      // two apps' policy routes cannot clash. 0x7C6D: "PG" + "VPN"-ish, outside Outline's range.
      routingTableId: 7124,
      routingPriority: 0x7c6d,
      protectionMark: 0x7c6d,
    },

    // The actual client config
    client: tsRequest.client,
  };

  // The request looks like:
  // {"vpn": {...}, "firstHop": "...", "client": "..."}
  await invokeGoMethod('EstablishVPN', JSON.stringify(goRequest));
}

export async function closeVpn(): Promise<void> {
  await invokeGoMethod('CloseVPN', '');
}

export type VpnStateChangeCallback = (status: TunnelStatus, id: string) => void;

/**
 * Registers a callback function to be invoked when the VPN state changes.
 *
 * @param cb - The callback function to be invoked when the VPN state changes.
 *             The callback will receive the VPN connection ID as well as the new status.
 *
 * @remarks The caller should subscribe to this event **only once**.
 *          Use the `id` parameter in the callback to identify the firing VPN connection.
 */
export async function onVpnStateChanged(
  cb: VpnStateChangeCallback
): Promise<void> {
  if (!cb) {
    return;
  }

  const cbToken = await registerCallback(data => {
    const conn = JSON.parse(data) as VPNConnectionState;
    console.debug('VPN connection state changed', conn);
    switch (conn?.status) {
      case VPNConnConnected:
        cb(TunnelStatus.CONNECTED, conn.id);
        break;
      case VPNConnConnecting:
        cb(TunnelStatus.RECONNECTING, conn.id);
        break;
      case VPNConnDisconnecting:
        cb(TunnelStatus.DISCONNECTING, conn.id);
        break;
      case VPNConnDisconnected:
        cb(TunnelStatus.DISCONNECTED, conn.id);
        break;
    }
    return '';
  });

  await invokeGoMethod('SetVPNStateChangeListener', cbToken.toString());
}

//#region type definitions of VPNConnection in Go

// The following constants and types should be aligned with the corresponding definitions
// in `./client/go/outline/vpn/vpn.go`.

type VPNConnStatus = string;
const VPNConnConnecting: VPNConnStatus = 'Connecting';
const VPNConnConnected: VPNConnStatus = 'Connected';
const VPNConnDisconnecting: VPNConnStatus = 'Disconnecting';
const VPNConnDisconnected: VPNConnStatus = 'Disconnected';

interface VPNConnectionState {
  readonly id: string;
  readonly status: VPNConnStatus;
}

//#endregion type definitions of VPNConnection in Go
