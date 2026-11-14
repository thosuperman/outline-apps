// Copyright 2024 The Outline Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import XCTest
import NetworkExtension
import OutlineError

final class VpnExtensionTest: XCTestCase {

  func testSelectVpnAddress() throws {
    XCTAssertEqual("10.111.222.0", selectVpnAddress(interfaceAddresses:["172.16.9.2", "192.168.20.2", "169.254.19.1"]))
    XCTAssertEqual("172.16.9.1", selectVpnAddress(interfaceAddresses:["10.111.222.1", "192.168.20.2", "169.254.19.1"]))
    XCTAssertEqual("192.168.20.1", selectVpnAddress(interfaceAddresses:["10.111.222.1", "172.16.9.2", "169.254.19.1"]))
    XCTAssertEqual("169.254.19.0", selectVpnAddress(interfaceAddresses:["10.111.222.1", "172.16.9.2", "192.168.20.2"]))
    XCTAssertTrue(kVpnSubnetCandidates.values.contains(selectVpnAddress(interfaceAddresses: getNetworkInterfaceAddresses())))
  }

  func testGetTunnelNetworkSettings() throws {
    let settings = SwiftBridge.getTunnelNetworkSettings()
    
    XCTAssertEqual("::", settings.tunnelRemoteAddress)
    
    XCTAssertEqual(1, settings.ipv4Settings?.addresses.count)
    XCTAssertTrue(kVpnSubnetCandidates.values.contains(settings.ipv4Settings?.addresses[0] ?? ""))
    XCTAssertEqual(["255.255.255.0"], settings.ipv4Settings?.subnetMasks)
    
    // Default route plus the fake DNS /32, which sits inside the excluded 169.254.0.0/16.
    let included = settings.ipv4Settings?.includedRoutes ?? []
    XCTAssertEqual(2, included.count)
    XCTAssertEqual(NEIPv4Route.default(), included.first)
    XCTAssertEqual("169.254.113.53", included.last?.destinationAddress)
    XCTAssertEqual("255.255.255.255", included.last?.destinationSubnetMask)
    XCTAssertEqual(15, settings.ipv4Settings?.excludedRoutes?.count ?? 0)

    // IPv6 is captured by the tunnel instead of leaking around it.
    XCTAssertEqual(1, settings.ipv6Settings?.addresses.count)
    XCTAssertEqual([NEIPv6Route.default()], settings.ipv6Settings?.includedRoutes)

    XCTAssertEqual(["169.254.113.53"], settings.dnsSettings?.servers)
  }


  /// This type should be aligned with the one defined in OutlineVpn.Swift
  private struct LastErrorIPCData: Decodable {
    let errorCode: String
    let errorJson: String
  }

  func testSaveLastDisconnectError() throws {
    // Nil error should be retrieved as nil data
    SwiftBridge.saveLastError(nsError: nil)
    XCTAssertNil(SwiftBridge.loadLastErrorToIPCResponse())

    // Error should be serialized to disk, and deserialized using the default decoder
    let lastErr = OutlineError.detailedJsonError(code: "ERR_TEST", json: "A test error json str")
    SwiftBridge.saveLastError(nsError: lastErr as NSError)
    let errData = SwiftBridge.loadLastErrorToIPCResponse()
    XCTAssertNotNil(errData)
    let errIPCResponse = try PropertyListDecoder().decode(LastErrorIPCData.self, from: errData! as Data)
    XCTAssertEqual(errIPCResponse.errorCode, "ERR_TEST")
    XCTAssertEqual(errIPCResponse.errorJson, "A test error json str")
  }
}
