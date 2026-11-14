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
 * Product identity shown in the UI. Kept in one place so the product name can change without
 * touching components. Bundle IDs, package names and URL schemes live in the platform build
 * configuration (see docs/proxygridvpn/IDENTITY.md), not here.
 */
export const BRAND = {
  productName: 'ProxyGridVPN',
  platformName: 'ProxyGrid',
  tagline: 'Your Network. Your Freedom.',
  websiteUrl: 'https://proxygrid.net/vpn',
  pricingUrl: 'https://proxygrid.net/pricing',
  signUpUrl: 'https://proxygrid.net/register',
  forgotPasswordUrl: 'https://proxygrid.net/forgot-password',
  dashboardUrl: 'https://proxygrid.net/dashboard/vpn',
  supportEmail: 'contact@proxygrid.net',
  privacyUrl: 'https://proxygrid.net/privacy',
  termsUrl: 'https://proxygrid.net/terms',
} as const;
