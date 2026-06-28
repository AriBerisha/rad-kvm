# controlplane — q6a-kvm web UI + input bridge (`qkvm`)

Our own KVM control plane: a single Go service (**zero external deps**) that
serves the web UI, proxies the live video, bridges browser keyboard/mouse to the
USB HID gadget, and gates it all behind a single user.

```
Browser ──<img> /stream──◄ ustreamer :8080 (proxied, single origin, gated)
        ──WS /ws/input──► qkvm ──► /dev/hidg0 (keyboard), /dev/hidg1 (abs mouse)
        ──/api/{status,setup,login,logout}──► single-user auth (signed cookie)
```

## Why our own (not kvmd)

Single-user appliance with a custom UI + features (named devices, WoL inventory,
paste rules). kvmd's value is its integrated UI/auth; replacing those makes it a
fight. We reuse only the hard primitives — **ustreamer** (video) and the **gadget**
`/dev/hidg*` (input) — and own the rest. kvmd remains a reference (esp. HID keymaps).

## Files

| file | role |
|------|------|
| `main.go` | HTTP routes, flags, MJPEG reverse-proxy, auth API |
| `auth.go` | single-user auth: PBKDF2-HMAC-SHA256 (600k iters) + HMAC-signed session cookie + optional TOTP |
| `totp.go` | RFC 6238 TOTP (stdlib): per-device secret, code verify with replay guard, otpauth URI |
| `hid.go` | input bridge: `event.code`→HID usage keymap, report assembly, `/dev/hidg*` writers |
| `ws.go` | minimal hand-rolled RFC-6455 WebSocket (read text frames; no dependency) |
| `web/index.html` | embedded SPA: login/setup gate, then video + input capture |
| `q6a-kvm-controlplane.service` | systemd unit → `/usr/local/bin/qkvm` |

## Auth

First visit with no account → **setup** (username + password ≥ 8). Afterwards →
**login**. Password is PBKDF2-HMAC-SHA256 (stdlib, 600k iterations, random salt);
the session is an **HMAC-signed cookie** (`HttpOnly`, `SameSite=Strict`) — stateless,
survives restarts via a persisted signing secret, no server-side session store.
`/stream`, `/snapshot`, `/ws/input` require a valid session; the static page is
public (it only renders the form). Setup can't be re-run once configured; failed
logins are throttled. Creds: `/var/lib/q6a-kvm/auth.json` (0600).
**Not `Secure`-flagged yet** — plain HTTP, LAN only. Add the flag + a cert with TLS.

**Two-factor (TOTP), optional but recommended.** At first-run setup you can enable
an authenticator app (Google Authenticator, 1Password, Authy — anything RFC 6238).
The secret is **generated per device** with `crypto/rand` at setup time — never
baked into the image, so two boards never share a seed. Setup shows a **QR code**
(rendered offline by a vendored `qrcode.js`, so the secret never leaves the board)
and the **manual key**, and only enables 2FA after you confirm a valid code — a
mis-scanned secret can't lock you out. Login then needs the 6-digit code too. The
last accepted 30-second step is persisted, so a code can't be **replayed**. Lost
your phone? Wipe `auth.json` over SSH to re-run setup (the appliance's recovery path).

## Build & deploy

No Go needed on the board — cross-compile and copy the static binary:

```sh
cd controlplane
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o /tmp/qkvm .
scp /tmp/qkvm <user>@<board>:/tmp/qkvm.new      # staging name avoids "text file busy"
# then on the board: run tmp/board-qkvm-install.sh  (stops service, swaps binary, restarts)
```

Runs as `q6a-kvm-controlplane.service` (root: writes `/dev/hidg*` + the auth file).
Flags: `-addr :8000`, `-stream http://127.0.0.1:8080`, `-kbd /dev/hidg0`,
`-mouse /dev/hidg1`, `-auth-file /var/lib/q6a-kvm/auth.json`.

## Input protocol (browser → server, WebSocket `/ws/input`, JSON)

- key:    `{"t":"k","code":"KeyA","down":true}`  (`code` = JS `KeyboardEvent.code`)
- mouse:  `{"t":"m","x":0.5,"y":0.3,"buttons":1}`  (x/y = 0..1 of the video rect; **absolute** → hidg1)
- mouse:  `{"t":"mr","dx":4,"dy":-2,"buttons":0}`  (**relative** deltas → hidg2; for multi-monitor / BIOS)
- wheel:  `{"t":"w","dy":1}`
- chord: `{"t":"chord","codes":["ControlLeft","AltLeft","Delete"]}`  (shortcuts: press together, release)
- type:  `{"t":"type","text":"hello"}`  (typed as keystrokes, US layout)

## REST API (all gated; JSON)

- `GET /api/devices` · `POST /api/devices {name,mac,broadcast?}` — Wake-on-LAN targets
- `POST /api/devices/wake {id}` — send magic packet · `POST /api/devices/delete {id}`
- `GET /api/macros` · `POST /api/macros {name,script}` (validated) — saved macros
- `POST /api/macros/run {id}` (runs on the HID, async) · `POST /api/macros/delete {id}`
- `GET /api/status` → `{configured, authed, totp}` · `POST /api/{setup,login,logout}`
- `GET /api/2fa/new?user=…` → `{secret, grouped, uri}` — mint a TOTP secret for the
  setup QR/manual key (stateless; nothing saved until setup confirms a code). Served
  only pre-account or to a signed-in user.
- `POST /api/setup {username,password,totp_secret?,code?}` — `code` confirms the
  secret before 2FA is enabled. `POST /api/login {username,password,code?}` — `code`
  required when 2FA is on.

Macro script: one step per line — `text:…`, `key:Enter`, `chord:A+B+C`, `delay:500`.
Stores: `/var/lib/q6a-kvm/devices.json`, `macros.json` (0600).

## Status / next

- ✅ Live video (proxied MJPEG) + absolute mouse + full keyboard over a hand-rolled
  WebSocket; releases all keys on connect/disconnect (no stuck keys).
- ✅ Single-user auth (setup/login, gated endpoints, signed cookie).
- ✅ Optional **TOTP 2FA** (RFC 6238, per-device secret, QR + manual, replay-guarded).
- ✅ Boot-persistent systemd service.
- ✅ **RAD-KVM** UI: sidebar with Shortcuts (key combos + type/paste), Devices
  (**Wake-on-LAN** by MAC), and Macros (save/run scripted sequences).
- ✅ **Mouse modes**: Absolute (1:1) *and* **Relative** (`/dev/hidg2`, deltas via
  pointer lock) — toggle in the top bar. Relative fixes the multi-monitor cursor
  jump (absolute maps across the whole virtual desktop) and works in BIOS/UEFI.
- ⏭ Mass-storage image mounting from the UI.
- ⏭ TLS + safe remote access (VPN/relay) — never a direct port-forward.

(Product/UI name is **RAD-KVM**; binary/service/paths stay `q6a-kvm`/`qkvm` for
now to avoid breaking the deploy — a full rename is a separate pass.)

## Security note

A KVM is hardware-level control of the target. Today this is **LAN-only, no TLS** —
keep it on a trusted network. Public access comes later via VPN/Tailscale or an
outbound relay, after TLS lands.

Auth notes (from an adversarial review of the 2FA work):
- The credential store fails **closed** — a corrupt/empty `auth.json` makes the
  service refuse to start rather than silently revert to first-run setup, and the
  file is written atomically (temp + fsync + rename) so a crash never truncates it.
- Online code/password guessing is throttled by the PBKDF2 cost (~600k iters per
  attempt) plus a 500 ms delay on failure; a dedicated failed-attempt rate-limiter
  is **future hardening** (kept out for now to avoid a single-user self-lockout DoS).
- Without TLS the second factor only adds defense-in-depth against a passive LAN
  sniffer to the extent the session cookie isn't also captured — TLS is the real fix.
