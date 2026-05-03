# Operations

These notes cover routine Linux service operation for `clawtello`.

## Service files and drop-ins

- Unit file: `/etc/systemd/system/clawtello.service`
- Config file: `/etc/clawtello/config.yaml`
- Token file: `/etc/clawtello/token`
- State directory: `/var/lib/clawtello`
- Systemd drop-ins: `/etc/systemd/system/clawtello.service.d/*.conf`

Example drop-in workflow:

```bash
sudo install -d -m 0755 /etc/systemd/system/clawtello.service.d
sudoedit /etc/systemd/system/clawtello.service.d/override.conf
sudo systemctl daemon-reload
sudo systemctl restart clawtello
```

## Journald retention

`clawtello` logs to journald by default. On hosts with long retention windows, consider a dedicated journald drop-in to cap disk usage.

Example:

```ini
[Journal]
SystemMaxUse=500M
MaxRetentionSec=7day
```

Apply with:

```bash
sudo install -d -m 0755 /etc/systemd/journald.conf.d
sudoedit /etc/systemd/journald.conf.d/clawtello.conf
sudo systemctl restart systemd-journald
```

## Rolling tokens

1. Write the new token to `/etc/clawtello/token`.
2. Set mode `0400`.
3. Ensure ownership matches the `clawtello` service user on Linux.
4. Restart the service.

Example:

```bash
printf '%s\n' 'new-token' | sudo tee /etc/clawtello/token >/dev/null
sudo chown clawtello:clawtello /etc/clawtello/token
sudo chmod 0400 /etc/clawtello/token
sudo systemctl restart clawtello
```

## Observing status

Useful commands:

```bash
./clawtello status
sudo systemctl status clawtello
sudo journalctl -u clawtello -n 100 --no-pager
sudo journalctl -u clawtello -f
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

`clawtello` backs off after authorization failures, so repeated auth errors should be noisy but not request-storming.

## Disabling telemetry

Set `DO_NOT_TRACK=1` or `CLAWTELLO_DISABLE=1` in the service unit’s environment (drop-in override) to have `clawtello start` exit cleanly without loading config, reading the token, or opening any sockets. Truthy values are `1`, `true`, `yes`, `on` (case-insensitive).

Example drop-in:

```ini
[Service]
Environment=DO_NOT_TRACK=1
```

Apply with `sudo systemctl daemon-reload && sudo systemctl restart clawtello`.
