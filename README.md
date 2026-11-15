# Apun Ka Communication

One Git-backed Coolify Docker Compose application, **Apun Ka Communication**, in `Apun Ka Projects → goriber saas`. The backend image builds the web client and serves it from PocketBase. PocketBase also stores accounts, conversations, messages, media, calls and notification jobs. LiveKit runs beside it and forwards encrypted WebRTC media for 1-to-1 calls, with no recording.

- [Health check](https://pb.tmadhu.me/api/app/health) and [PocketBase admin](https://pb.tmadhu.me/_/)
- LiveKit signalling at `wss://lk.tmadhu.me`, authenticated TURN/TLS at `turn.tmadhu.me:443`
- [Open the web app](https://chat.tmadhu.me). The editable client is in `web/`.
- [API contract](API.md), [Web client notes](WEB.md), [Android build prompt](ANDROID.md)

## Accounts and email

Use an email inbox you can access for password recovery. Each account supports two concurrent device sessions. A third device signs out the oldest device without removing messages or media. Password reset and email change links open the web app, and password recovery is also available by six-digit email code.

Set `SMTP_USERNAME` and `SMTP_PASSWORD` in the server environment. Gmail uses TLS on port 465 and the display name is Apun Ka Communication. New-device alerts for app users are disabled. PocketBase encrypts its persisted settings with `PB_SETTINGS_KEY` (32 characters). Keep that value with the server settings outside PocketBase backups. Restored instances need the same key and must receive `--encryptionEnv=PB_SETTINGS_KEY`. After replacing a Gmail app password, update the environment file and recreate the backend.

## Update the web app

In `web/`, run `npm ci` and `npm run build` for a local build. Push the change to `main` to trigger Coolify through the signed GitHub push webhook. Coolify builds the frontend and backend together. The app shell is revalidated and hashed assets are cached. The LiveKit SDK loads on demand when a call starts.

## Deploy and upgrade

Create a public-repository application in Coolify with the Docker Compose build pack, Compose path `/compose.yaml`, and Raw deployment enabled. Use `main` as the production branch and turn preview deployments off because the media ports and application data belong to one stack. Use the GitHub manual push webhook and store the matching webhook secret in Coolify and GitHub.

Host settings and state live in `/opt/private-communication`. The persistent `pb_data` directory belongs to UID/GID 10001. Keep the backend and LiveKit environment files at mode 0600. The Compose file points to them without embedding credentials. For a fresh install, supply `APP_URL`, `LIVEKIT_WS_URL`, `LIVEKIT_INTERNAL_URL`, `LIVEKIT_API_KEY` and `LIVEKIT_API_SECRET` in `backend.env`. Use the same key and secret in LiveKit's `LIVEKIT_KEYS=privatecomms: <secret>` and update the LiveKit webhook API key in the YAML if you change the key name.

Compose owns the Traefik labels for the web, PocketBase, LiveKit and TURN hostnames. Leave the Coolify domain overrides empty. PocketBase uses a TCP TLS route with PROXY version 2 because the shared HTTP upload buffer stalls its realtime stream. The two containers communicate on their own Compose network while Traefik reaches them through the existing `coolify` network. Set `APP_PROXY_CIDR` to the Traefik container's address on that shared network if it changes. TURN DNS stays DNS-only. Keep the media ports `7882/udp`, `7881/tcp` and `3478/udp` consistent across Docker, host policy and Oracle ingress.

For initial provisioning only, `BOOTSTRAP_ADMIN_EMAIL`, `BOOTSTRAP_ADMIN_PASSWORD`, `BOOTSTRAP_USER1_EMAIL`, `BOOTSTRAP_USER1_PASSWORD`, `BOOTSTRAP_USER2_EMAIL` and `BOOTSTRAP_USER2_PASSWORD` create the first accounts and conversation. Remove these variables after first boot. The admin can add an invited user in the dashboard. Create a `conversations` record with exactly two distinct member IDs and a `pair` set to the sorted IDs joined by a colon. Then add one `conversation_members` row for each user. Keep the original memberships immutable to preserve chat visibility.

An authenticated user can manage their own push registrations. Web Push uses the persistent VAPID pair in `backend.env`. Android push uses a project service account JSON at `/opt/private-communication/firebase/service-account.json` readable by UID 10001 and `FCM_CREDENTIALS_FILE=/run/firebase/service-account.json`. The [Firebase Admin instructions](https://firebase.google.com/docs/admin/setup) cover credential setup. Recreate the backend to reload its environment. Regular clients call `GET /api/app/push` to see which providers are configured.

## Backup, restore and rollback

A consistent snapshot is generated daily at **02:00 Dhaka time**. The server and archive names use UTC. After a successful daily snapshot, archives older than the seven-date window are purged. A failed backup leaves earlier archives available. The retention job touches only `daily-YYYY-MM-DD.zip` snapshots. These backups share the main server's disk. To test, use PocketBase Settings → Cron jobs → backup, then inspect Settings → Backups. Live attachment uploads are capped at a total of 2 GiB, 50 MiB per file, and stop when available disk falls below 15%.

Test a restore by starting the same backend version with a separate empty data directory and an isolated, loopback-only admin port. Extract a copy of the archive there with the restore instance stopped, run the instance, and check login, history and protected media. For production restore, stop only this backend, snapshot its current data directory, move or extract the intended backup into a clean data directory owned by UID/GID 10001, and restart. Ensure no other backend is writing to that directory. If a new release fails before changing data, redeploy the previous image tag. If a migration changed storage compatibility, restore the pre-upgrade backup as part of rollback. Keep environment files separately from backups.

Verify health, authenticated realtime message receipt, a protected download and both call paths after upgrading. Watch disk use, backup job errors, and LiveKit CPU, memory and packet loss in Coolify.
