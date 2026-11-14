# Deployment and verification

## Unified GitHub deployment

Production is **Apun Ka Projects → goriber saas → Apun Ka Communication** in Coolify. It deploys the public repository's `main` branch via a signed GitHub push webhook. PocketBase serves the existing website and its own admin/API. LiveKit is the second container in the same Docker Compose application. The production database, media, backups and private environment files remain under `/opt/private-communication` on the Oracle host.

## Web and account update, 15 November 2026 (Dhaka)

The web app first went live at [chat.tmadhu.me](https://chat.tmadhu.me) as a separate Coolify service. The web client starts with a small chat bundle and loads LiveKit when entering a call.

| Check | Result |
|---|---|
| Desktop and mobile browser | Live chat, reconnect catch-up, video in both directions, and chat during a call passed using two Chromium sessions with test media. |
| Device change | Previous browser signed out. Earlier messages stayed in PocketBase and were blocked by the record and message-retry APIs. |
| Gmail | Received branded HTML emails for history unlock, password recovery by code, password recovery by link, and email change. Each code or link completed its workflow on an isolated test account. |
| Firebase | Project `mandy-chat` credentials loaded. FCM HTTP v1 validate-only returned 200. Delivery to a physical Android device awaits an Android client and registration. |

Temporary accounts and records from the final tests were removed.

Cloudflare Email Routing for `tmadhu.me` has a catch-all forwarder. Its destination differs from the Gmail account used for outbound SMTP. Verify which inbox belongs to each user before relying on recovery for the domain aliases.

Deployed 14 November 2026 to Coolify `BACKEND → base → Private Communication`. The service ID is `sx0encsm2ldlcbwsrfzifn2y`. PocketBase is healthy on `https://pb.tmadhu.me`; LiveKit v1.13.11 is available at `wss://lk.tmadhu.me`. The two user accounts are `chat-owner@tmadhu.me` and `chat-guest@tmadhu.me`. A separate restricted local file contains initial passwords.

| Check | Result |
|---|---|
| Login and permissions | Two users signed in. An unrelated account could not read the chat, download protected attachments or obtain call credentials. |
| Realtime chat | Live event received over public HTTPS. Message retry, replies, edits, reactions, read cursor and protected image transfer checked. |
| Call workflow | Simultaneous call, restricted accept, token issuance, signed LiveKit webhook and transition to connected checked. |
| Audio transport | Audio received over direct UDP, forced direct TCP and forced TURN/TLS. |
| Backup | Daily archive produced, archive and database integrity verified, isolated restore instance started. Seven-day retention exercised with disposable archives. |
| Web Push | VAPID keys provisioned. Actual notification display requires the web client. |
| TURN/UDP | UDP 3478 did not respond externally from the test network. The tested TURN/TLS path on TCP 443 is available. |
| Longer benchmark | Stopped on request. This does not change the successful media connectivity checks. |

If TURN/UDP is needed, verify UDP 3478 in the OCI network security group or subnet security list and test from a second network. Docker publishes the port and the existing host rules allow Docker's published traffic. No OCI rule was changed.
