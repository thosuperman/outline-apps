# Copyright 2026 ProxyGrid
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Replaces the app name in the client's message catalogues.

usage: python3 rebrand_messages.py <messages-dir> <ProductName> [<OldName>]

Only the *app* name is replaced. References that mean the open-source Outline project or
Outline-compatible servers stay as they are, because GridVPN's "My Own Server" mode accepts
standard Outline keys and the Outline Manager: "Outline Manager", "Outline Foundation",
"Outline server(s)", "Outline VPN server", "Outline key", "Outline access key" and URLs.
Running it again with a new name moves from <OldName> (default "Outline") to the new name.
"""
import json
import re
import sys
from collections import OrderedDict
from pathlib import Path

# Followers that mean "the Outline project/servers", not the app.
KEEP_FOLLOWERS = [
    'Manager', 'Foundation', 'server', 'servers', 'Server', 'Servers', 'VPN server',
    'VPN servers', 'VPN access key', 'access key', 'key', 'keys', 'team',
]
# Messages that are about the Outline project itself and are no longer shown by GridVPN.
SKIP_KEYS = {'about-outline'}


def rebrand(text: str, old: str, new: str) -> str:
    keep = '|'.join(re.escape(f) for f in sorted(KEEP_FOLLOWERS, key=len, reverse=True))
    pattern = re.compile(
        rf'(?<![\w./-]){re.escape(old)}(?![\w-])(?!\.\w)(?!\s+(?:{keep})\b)'
    )
    return pattern.sub(new, text)


def main(directory: str, new: str, old: str = 'Outline') -> None:
    changed = 0
    for path in sorted(Path(directory).glob('*.json')):
        data = json.loads(path.read_text(encoding='utf-8'), object_pairs_hook=OrderedDict)
        out = OrderedDict()
        for key, value in data.items():
            if key in SKIP_KEYS or not isinstance(value, str):
                out[key] = value
                continue
            replaced = rebrand(value, old, new)
            changed += replaced != value
            out[key] = replaced
        path.write_text(json.dumps(out, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(f'updated {changed} messages')


if __name__ == '__main__':
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    main(*sys.argv[1:4])
