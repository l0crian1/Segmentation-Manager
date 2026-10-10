# Administration

[Documentation home](../README.md)

## Initial setup

1. Start the container with persistent storage and read its startup log: `docker logs segman`, `docker compose logs segmentation-manager`, or `show container log segman` on VyOS.
2. Open the application's HTTPS address on a trusted management network. The first-start certificate is self-signed; verify that you are connecting to your own host before proceeding with the browser warning.
3. Enter the one-time installation claim code from the log. Create an administrator account with a 2–64 character username and a 12–256 character password, and set the workspace name.
4. Sign in and [add your routers](user-guide.md#connect-a-vyos-router).
5. Replace the application certificate in **Settings → Security** with a certificate trusted by your users' browsers.

There is no default administrator password. Setup is available only for a fresh data directory. Treat the installation claim code as a secret; it grants the ability to initialize that installation. If restoring an existing workspace, choose the restore option instead of creating a new administrator.

## Accounts and permissions

Open **Users** to view accounts. Administrators can create accounts, change their roles or passwords, and delete accounts.

- **administrator:** full access, including account management, credential-bearing backup exports, and application TLS certificate replacement.
- **network-admin:** manages sites, devices, router certificate trust, VRFs, MPLS/EVPN policies, and general application preferences. Can view accounts and activity, but cannot create, modify, or delete accounts, export backups, or replace application TLS credentials.
- **view-only:** can view all application pages, configuration details, device status, settings, users, and activity. Cannot make configuration or administrative changes. Signing out and choosing a local theme remain available.

These permissions are enforced by the API as well as the interface. Account changes revoke the affected user's sessions. The last administrator cannot be removed or demoted. For password changes, ask an administrator to edit the account in **Users**; leave the password field blank when editing other account properties to keep the existing password.

## Settings and appearance

**Settings → General** controls:

- Workspace name.
- Matrix refresh interval: default 30 seconds; range 10–3600 seconds.
- Device read timeout: default 15 seconds; range 3–120 seconds. This does not shorten the separate allowance for configuration commits.
- Maximum session duration: default 8 hours; range 1–24 hours. Sessions also expire after 30 minutes of API inactivity. A polling page counts as activity.
- Audit retention: default 90 days; range 7–3650 days. Cleanup runs when the settings are saved.

The appearance selector on the sign-in screen and application header supports **Light**, **Dark**, and **System**. System follows the operating system preference. The selection is stored in the current browser between sessions.

**Settings → Activity** shows configuration operations and audit events. Device availability and observation timestamps describe the last check or refresh; they are not continuous monitoring or an uptime report.

## HTTPS certificates

The application serves HTTPS directly. An administrator can upload a PEM certificate chain and matching PEM private key in **Settings → Security**, then restart the container to load them. Use a certificate valid for the hostname your users enter. Keep the data volume when restarting; it stores the certificate and key.

Router API certificate trust is configured separately for each device. A pinned router certificate must still be within its validity period. When a router certificate changes, verify the replacement through a trusted channel and update its pin. Do not disable certificate validation to resolve a trust error.

### Obtaining a VyOS certificate fingerprint

These steps apply regardless of where Segmentation Manager is deployed. After enabling the router's HTTPS API, run this command from a trusted SSH session or console **on the VyOS router**:

```sh
sudo echo | openssl s_client -connect 127.0.0.1:443 2>/dev/null | openssl x509 -noout -fingerprint -sha256
```

The command prints the API certificate's SHA-256 fingerprint. It assumes the API listens on loopback port 443; if the HTTPS service is bound only to a specific address or uses a different port, substitute that listener. Use the router API endpoint rather than Segmentation Manager's web port.

In **Devices → Add device**, select **Trust an exact certificate fingerprint** and paste only the hexadecimal value after `=` into **SHA-256 fingerprint**. Colons and either letter case are accepted. If you inspect the certificate through the application, compare that result with the value obtained on the router before accepting it. Verify and update the saved fingerprint whenever the router certificate is replaced or renewed.

## Backup and restore

### Online backup

As an administrator, open **Settings → Backup**, choose a strong backup password, and download the encrypted file. Keep that password separately; it is needed to restore the backup.

The backup contains a consistent database snapshot and the key used to decrypt device credentials. It includes accounts, sites, devices, settings, and recorded activity. It does **not** include the application's HTTPS certificate or back up the managed routers' full configurations. The application backup facility supports databases up to 48 MiB.

### Restore into a fresh installation

1. Start the desired application version with a new, empty persistent volume or data directory.
2. Read that installation's claim code from its container log.
3. On the setup page, choose **Restore from a backup** and supply the claim code, backup file, and backup password.
4. Sign in with an account from the restored workspace and check router connections.

Restoring revokes old sessions and retains the destination installation's TLS certificate. Replace that certificate separately if necessary. Restoration is available during initial setup, so it cannot overwrite an initialized workspace through the normal setup endpoint.

### Full offline backup

For a complete filesystem backup, wait for operations to finish, stop the application container, and back up its entire persistent data directory or volume. Include the database sidecars, encryption key, and TLS material. Do not copy only a live database file. Restart the container after the copy completes.

On VyOS, an offline copy requires keeping the service stopped while copying `/config/containers/segman`; a maintenance window with `set container name segman disable` followed by commit is one way to do this. Remove that disable setting and commit after the backup. Keep your saved router configuration consistent with whether the service should start at boot.

## Upgrades and recovery

Use the update procedure for your [Docker host](deployment-docker.md#updates) or [VyOS container](deployment-vyos.md#updates-and-restarts). Preserve the existing volume, accounts, and settings. Back up before upgrades because a newer release may migrate the database; older releases cannot necessarily read the migrated data.

For rollback, pair the previous image with a restored pre-upgrade backup or offline volume copy. Retain the upgraded volume separately until recovery has been verified. Starting against a new empty volume intentionally creates a separate installation and does not recover existing accounts or device keys.
