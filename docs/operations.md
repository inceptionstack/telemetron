# Operations

These notes cover routine Linux service operation for `lokiotel`.

## Service files and drop-ins

- Unit file: `/etc/systemd/system/lokiotel.service`
- Config file: `/etc/lokiotel/config.yaml`
- Token file: `/etc/lokiotel/token`
- State directory: `/var/lib/lokiotel`
- Systemd drop-ins: `/etc/systemd/system/lokiotel.service.d/*.conf`

Example drop-in workflow:

```bash
sudo install -d -m 0755 /etc/systemd/system/lokiotel.service.d
sudoedit /etc/systemd/system/lokiotel.service.d/override.conf
sudo systemctl daemon-reload
sudo systemctl restart lokiotel
```

## Journald retention

`lokiotel` logs to journald by default. On hosts with long retention windows, consider a dedicated journald drop-in to cap disk usage.

Example:

```ini
[Journal]
SystemMaxUse=500M
MaxRetentionSec=7day
```

Apply with:

```bash
sudo install -d -m 0755 /etc/systemd/journald.conf.d
sudoedit /etc/systemd/journald.conf.d/lokiotel.conf
sudo systemctl restart systemd-journald
```

## Rolling tokens

1. Write the new token to `/etc/lokiotel/token`.
2. Set mode `0400`.
3. Ensure ownership matches the `lokiotel` service user on Linux.
4. Restart the service.

Example:

```bash
printf '%s\n' 'new-token' | sudo tee /etc/lokiotel/token >/dev/null
sudo chown lokiotel:lokiotel /etc/lokiotel/token
sudo chmod 0400 /etc/lokiotel/token
sudo systemctl restart lokiotel
```

## Observing status

Useful commands:

```bash
./lokiotel status
sudo systemctl status lokiotel
sudo journalctl -u lokiotel -n 100 --no-pager
sudo journalctl -u lokiotel -f
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

`lokiotel` backs off after authorization failures, so repeated auth errors should be noisy but not request-storming.
