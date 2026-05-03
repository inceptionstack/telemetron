# Operations

These notes cover routine Linux service operation for `telemetron`.

## Service files and drop-ins

- Unit file: `/etc/systemd/system/telemetron.service`
- Config file: `/etc/telemetron/config.yaml`
- Token file: `/etc/telemetron/token`
- State directory: `/var/lib/telemetron`
- Systemd drop-ins: `/etc/systemd/system/telemetron.service.d/*.conf`

Example drop-in workflow:

```bash
sudo install -d -m 0755 /etc/systemd/system/telemetron.service.d
sudoedit /etc/systemd/system/telemetron.service.d/override.conf
sudo systemctl daemon-reload
sudo systemctl restart telemetron
```

## Journald retention

`telemetron` logs to journald by default. On hosts with long retention windows, consider a dedicated journald drop-in to cap disk usage.

Example:

```ini
[Journal]
SystemMaxUse=500M
MaxRetentionSec=7day
```

Apply with:

```bash
sudo install -d -m 0755 /etc/systemd/journald.conf.d
sudoedit /etc/systemd/journald.conf.d/telemetron.conf
sudo systemctl restart systemd-journald
```

## Rolling tokens

1. Write the new token to `/etc/telemetron/token`.
2. Set mode `0400`.
3. Ensure ownership matches the `telemetron` service user on Linux.
4. Restart the service.

Example:

```bash
printf '%s\n' 'new-token' | sudo tee /etc/telemetron/token >/dev/null
sudo chown telemetron:telemetron /etc/telemetron/token
sudo chmod 0400 /etc/telemetron/token
sudo systemctl restart telemetron
```

## Observing status

Useful commands:

```bash
./telemetron status
sudo systemctl status telemetron
sudo journalctl -u telemetron -n 100 --no-pager
sudo journalctl -u telemetron -f
```

Look for:

- recent heartbeat timestamps
- recent flush timestamps
- HTTP status codes
- dropped batch count

## Troubleshooting auth failures

Symptoms:

- `401` or `403` in logs
- rising dropped batch count
- stale flush timestamps

Checklist:

1. Confirm the token file contents and permissions.
2. Confirm the endpoint is correct and still expects bearer auth.
3. Confirm the endpoint uses HTTPS unless `insecure_endpoint` is intentionally enabled for local testing.
4. Restart the service after token rotation.
5. Check whether the server is rejecting caller metadata or tenant routing.

`telemetron` backs off after authorization failures, so repeated auth errors should be noisy but not request-storming.

## Disabling telemetry

Five opt-out signals honored, covering both standalone and lowkey-family deployments:

**Shared**
- `DO_NOT_TRACK=1` — community standard. Truthy: `1|true|yes|on`.

**telemetron-specific**
- `TELEMETRON_TELEMETRY=0` — falsy: `0|false|no|off`. Unset = enabled.
- `~/.telemetron/telemetry-off` — marker file under the service user’s home.

**Lowkey-inherited**
- `LOWKEY_TELEMETRY=0`
- `~/.lowkey/telemetry-off`

When any signal is present, `telemetron start` exits cleanly without loading config, reading the token, or opening any sockets.

### Env-var drop-in

```ini
[Service]
Environment=DO_NOT_TRACK=1
```

Apply with `sudo systemctl daemon-reload && sudo systemctl restart telemetron`.

### Marker-file (preferred for lowkey deployments)

Env vars set by the lowkey installer in the interactive shell do not propagate into systemd units. Use the marker file so the opt-out sticks across restarts:

```bash
# Drop into the telemetron service user's home:
sudo -u telemetron install -d -m 0700 ~telemetron/.telemetron
sudo -u telemetron touch ~telemetron/.telemetron/telemetry-off
sudo systemctl restart telemetron
```

Or honor lowkey’s marker directly:

```bash
sudo -u telemetron install -d -m 0700 ~telemetron/.lowkey
sudo -u telemetron touch ~telemetron/.lowkey/telemetry-off
sudo systemctl restart telemetron
```
