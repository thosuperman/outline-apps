# GridVPN release notes

## 1.0.0 (preview, 2026-11-14)

GridVPN is the VPN app of ProxyGrid. *Your Network. Your Freedom.*

### New

- **ProxyGrid regions.** Sign in with your ProxyGrid account and connect to Tokyo, Seoul, Virginia
  or Frankfurt with one tap. Each location shows whether it is online. If a location goes down,
  your next connection is served from another healthy one.
- **Plan and usage.** See your plan, data used this period and when it resets. Free accounts get
  3 GB every 30 days; Basic ($2.99, 20 GB), Plus ($4.99, 50 GB) and Pro ($7.99, 100 GB) are
  available at proxygrid.net/pricing. When the allowance runs out, the app says so instead of
  failing silently.
- **My Own Server.** Use any `ss://` or `ssconf://` key, including keys from the Outline Manager
  or your own server, with no ProxyGrid limits.
- **Keys in the system keychain.** Access keys and your session are stored in the iOS/macOS
  Keychain, the Android Keystore or the Windows/Linux credential store instead of app storage.
  Existing keys are moved over automatically.
- **New design** in light and dark themes: a single connect control, clear status, and a new app
  icon.

### Reliability

- DNS: repeated copies of the same lookup share one request instead of each opening a new
  connection to the server, and unanswered lookups are retried a bounded number of times. This
  keeps DNS working during bursts without weakening protection against DNS spoofing.
- DNS no longer gets stuck in the slow TCP-fallback mode after switching networks.
- Windows: the tunnel restarts itself if its traffic relay stops (for example after sleep), DNS
  goes through the tunnel's resolver again, and disconnecting while connecting no longer leaves a
  hidden tunnel running.
- Android: stopping the VPN from the system or the Quick Settings tile while connecting no longer
  crashes or leaves the tile showing "on"; the VPN is restored after a reboot even if the network
  was down at shutdown.
- macOS/iOS: IPv6 traffic now goes through the tunnel instead of around it; reconnecting after a
  stop works reliably; the extension no longer crashes on some network interfaces; DNS mode is
  re-checked after the device wakes.
- All platforms: one malformed packet no longer stops all traffic.

### Credits

GridVPN is built on the open-source [Outline client](https://github.com/OutlineFoundation/outline-apps)
by the Outline Foundation and uses Shadowsocks. It is not affiliated with or endorsed by the
Outline Foundation. Open-source licenses are listed in the app under Licenses.
