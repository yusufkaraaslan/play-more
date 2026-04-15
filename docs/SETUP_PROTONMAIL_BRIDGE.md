# ProtonMail Bridge Setup

Use your ProtonMail account as the SMTP server for PlayMore email verification and password reset.

## Prerequisites

- **Paid Proton plan** — Mail Plus, Unlimited, Business, or Visionary. Free accounts cannot use Bridge.
- The server running PlayMore must also run Bridge (they talk via `127.0.0.1:1025`).
- Linux with systemd (for auto-start). Works on macOS/Windows but these instructions are Linux-focused.

## 1. Install Bridge

### Debian / Ubuntu
```bash
wget https://proton.me/download/bridge/protonmail-bridge_3.15.0-1_amd64.deb
sudo apt install ./protonmail-bridge_3.15.0-1_amd64.deb
```

### Other Linux
Download the latest `.rpm`, `.deb`, or AppImage from <https://proton.me/mail/bridge> or [GitHub releases](https://github.com/ProtonMail/proton-bridge/releases).

## 2. First-time login (CLI)

```bash
protonmail-bridge --cli
```

In the Bridge CLI:
```
>>> login
Username: you@proton.me
Password: ********
# If 2FA enabled:
2FA code: 123456
# Wait for sync to complete

>>> info
# Copy the SMTP credentials shown — the "Bridge password" is AUTO-GENERATED,
# it is NOT your Proton account password.

>>> exit
```

You'll see something like:
```
SMTP Settings:
  Host: 127.0.0.1
  Port: 1025
  Username: you@proton.me
  Password: xyz-generated-password-xyz
  Security: STARTTLS
```

## 3. Run Bridge as a systemd service

Create `/etc/systemd/system/protonmail-bridge.service`:

```ini
[Unit]
Description=ProtonMail Bridge
After=network.target

[Service]
Type=simple
User=YOUR_USER
Group=YOUR_GROUP
Environment="PASSWORD_STORE_DIR=/home/YOUR_USER/.password-store"
ExecStart=/usr/bin/protonmail-bridge --noninteractive
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Replace `YOUR_USER` / `YOUR_GROUP` with the user that logged into Bridge.

Enable and start:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now protonmail-bridge
sudo systemctl status protonmail-bridge
```

**Test the SMTP port is listening:**
```bash
nc -zv 127.0.0.1 1025
# Connection to 127.0.0.1 1025 port [tcp/*] succeeded!
```

## 4. Configure PlayMore

Add these to your `.env`:
```ini
PLAYMORE_SMTP_HOST=127.0.0.1
PLAYMORE_SMTP_PORT=1025
PLAYMORE_SMTP_USER=you@proton.me
PLAYMORE_SMTP_PASS=xyz-generated-bridge-password
PLAYMORE_SMTP_FROM=PlayMore <you@proton.me>
PLAYMORE_BASE_URL=https://playmore.example.com
```

Restart PlayMore. You should see:
```
✓  SMTP reachable at 127.0.0.1:1025
```

If Bridge isn't running, PlayMore will try to start it automatically via `systemctl start protonmail-bridge`.

## 5. Test

Register a new user in PlayMore — the verification email should arrive in their inbox within a few seconds.

## Troubleshooting

### "Connection refused" or `⚠  SMTP health check failed`
Bridge isn't running.
```bash
sudo systemctl status protonmail-bridge
sudo journalctl -fu protonmail-bridge
```

### "Authentication failed"
You're using your Proton password instead of the Bridge-generated password. Run `protonmail-bridge --cli` → `info` again to get the correct one.

### Bridge keeps crashing
Bridge stores state in `~/.config/protonmail/bridge-v3/` and `~/.cache/protonmail/bridge-v3/`. Make sure the systemd `User=` can read/write there.

### Bridge needs a GUI keyring
On headless servers, set `PASSWORD_STORE_DIR` to a `pass`-managed directory, or use the `--keychain` flag to pick a keyring backend that works without a desktop session.

### 2FA or password changed on Proton account
Re-run `protonmail-bridge --cli` → `login`. The Bridge password regenerates; update `.env` accordingly.

## Caveats

- **Same machine only.** Bridge listens on localhost. If PlayMore is on a different host, you'll need an SSH tunnel or a VPN — Bridge does not expose itself on the network by design.
- **Proton sending limits** apply: 150/day for personal, 10k/day for Unlimited+. Exceeding these will silently fail.
- **Bridge updates** occasionally require re-authentication. Check `journalctl -u protonmail-bridge` if emails stop sending.
