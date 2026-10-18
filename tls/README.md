# Local HTTPS certificates

Run `make tls TLS_HOSTS=192.168.31.233,comma-d85bcc94.local,localhost,127.0.0.1,::1`.
Use your own device addresses. Go generates a self-signed local authority and a
365-day server certificate. Generated files stay here and are ignored by Git.

- `ca.crt`: public authority certificate. Install and trust this on each client.
- `ca.key`: private signing key. Keep it on your computer. Never copy it to the device.
- `cert.pem`: server certificate with the requested IP/DNS subject alternative names.
- `key.pem`: private server key, mode `0600`. Copy this only to your own device.

Run the same command to renew the server certificate or add a new LAN address.
The authority is reused, so clients retain trust. Back up these files privately.
Do not publish private keys or put them in release bundles.

See [installation](../docs/installation.md#local-https-and-pwa-installation) for
systemd configuration and macOS/iPhone certificate trust instructions.
