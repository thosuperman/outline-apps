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

// Renders the ProxyGrid VPN SVG sources to PNGs: node render.mjs <svg> <size> <out.png> [<size> <out.png> ...]
// Uses the headless Chromium that the web tests already depend on, so output is deterministic
// for a given puppeteer version.
import {readFile} from 'node:fs/promises';
import puppeteer from 'puppeteer';

const [svgPath, ...pairs] = process.argv.slice(2);
if (!svgPath || pairs.length % 2) {
  console.error('usage: node render.mjs <svg> <size> <out.png> [...]');
  process.exit(2);
}
const svg = await readFile(svgPath, 'utf8');
const browser = await puppeteer.launch({headless: true});
try {
  const page = await browser.newPage();
  for (let i = 0; i < pairs.length; i += 2) {
    const size = Number(pairs[i]);
    const out = pairs[i + 1];
    await page.setViewport({width: size, height: size, deviceScaleFactor: 1});
    await page.setContent(
      `<html><body style="margin:0;background:transparent">` +
        svg.replace('<svg ', `<svg width="${size}" height="${size}" `) +
        `</body></html>`
    );
    await page.screenshot({path: out, omitBackground: true, clip: {x: 0, y: 0, width: size, height: size}});
  }
} finally {
  await browser.close();
}
