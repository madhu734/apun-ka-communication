# Apun Ka Communication client API

PocketBase origin is `https://pb.tmadhu.me`. JSON uses UTF-8. Pass the PocketBase auth token in `Authorization` on authenticated requests. Sign in with `POST /api/collections/users/auth-with-password`, body `{"identity":"email","password":"…"}`, and `X-App-Device` set to a stable random installation ID (24 to 128 characters, such as a UUID). The two newest distinct device sign-ins stay active. A new third device revokes the oldest sign-in. Refresh preserves device ordering. Refresh via `POST /api/collections/users/auth-refresh` and clear local session data and media on revocation. Accounts can be created through web signup or by an administrator.

| Account route | Use |
|---|---|
| GET `/api/app/account/session` | Returns `{active:true}` when the current device session is valid. A revoked device session receives 401. |
| POST `/api/app/account/recovery/request` | Guest route, `{email}`. Send a password recovery code. |
| POST `/api/app/account/recovery/confirm` | Guest route, `{email,code,password,passwordConfirm}`. On success sign in with the new password. |
| POST `/api/app/account/logout` | Signed in, empty body. Revokes the current session. |

Use PocketBase's built-in `request-password-reset` and `confirm-password-reset` for link recovery, and `request-email-change` and `confirm-email-change` for email changes. The respective email links lead to the web app's reset and confirmation pages.

## Read and subscribe

Use `GET /api/collections/{name}/records`, standard PocketBase filters, sort and pagination, plus record-specific `GET /api/collections/{name}/records/{id}`. Subscribe using the PocketBase SDK or implement its [SSE realtime protocol](https://pocketbase.io/docs/api-realtime/). REST history remains authoritative. On connection and reconnection, subscribe and fetch sorted history, merging by record ID so events during the fetch are preserved. Filter by conversation as needed. Use `created,id` for chronological merges and `-created,-id` for the newest page. Page at 30 records and use a composite cursor when loading older history.

| Collection | Client fields |
|---|---|
| `users` | `id,name,avatar` and own account fields. Other participants' public names come from the people route. |
| `conversations` | `id,members,created,updated`. Exactly two members. |
| `message_requests` | `id,sender,recipient,note,created,updated`. Only the sender and recipient receive realtime events. |
| `conversation_members` | `conversation,user,read_through,read_id,presence_until,typing_until`, plus timestamps. |
| `messages` | `id,conversation,sender,text,client_message_id,kind,attachment,attachment_bytes,reply_to,in_call,created,updated`. |
| `message_reactions` | `id,conversation,message,user,emoji,created,updated`. One reaction per user per message. |
| `calls` | `id,conversation,caller,receiver,kind,room,status,ring_expires_at,accepted_at,connected_at,ended_at,created,updated`. |
| `devices` | Own `id,user,platform,created,updated`. |
 
Messages and attachments are visible only to their conversation's members. For a file, call `POST /api/files/token` with session auth and fetch `/api/files/messages/{id}/{attachment}?token={token}`. Add `&thumb=400x0` for image previews. Refresh expired file tokens and keep the resulting URLs out of logs. The read cursor indicates read-through only. A successful POST means server receipt. The API does not report a separate per-device delivered receipt.

## Write operations

All paths below start at `/api/app`. Collection writes for chat, calls, membership state and devices go through these routes.

| Method and path | Input and result |
|---|---|
| GET `/conversations/{id}/people` | Returns `[{id,name,avatar}]`. |
| GET `/inbox` | Returns `{chats,requests}` for the current user. Chats have `{id,peer}` and pending requests have `{id,peer,incoming,note}`. |
| GET `/members/search?q=…` | Searches display names or matches a full email address. Returns up to 20 `{id,name}` results. |
| POST `/message-requests` | `{recipient,note?}`. Returns `{request,direction}` for a pending request or `{conversation}` for an existing chat. |
| POST `/message-requests/{id}/accept` | Recipient only. Returns `{conversation}` and saves any introductory note as a message. |
| DELETE `/message-requests/{id}` | Sender cancels or recipient declines a pending request. Returns 204. |
| POST `/messages` | JSON `{conversation,text,client_message_id,kind,reply_to?,in_call?}` for text. For media, send those fields as multipart with `attachment`; `kind` is `image`, `voice` or `video`. Returns record as 201, or 200 on retry with the same sender and client ID. |
| PATCH `/messages/{id}` | `{text}`. Sender only. Returns record. |
| PUT `/reactions/{id}` | Message ID, `{emoji}`. Empty string removes. 204. |
| PUT `/conversations/{id}/read` | `{message}` with message ID. 204. Cursor only advances. |
| PUT `/conversations/{id}/presence` | `{typing: boolean}`. 204. Presence lasts 45 s, typing 8 s. Refresh only while visible. |
| POST `/calls` | `{conversation,kind}` with `audio` or `video`. Returns call 201. Ring timeout 45 s. |
| POST `/calls/{id}/accept` | `{deviceId}`, a stable installation ID. Receiver only. Returns updated call. |
| POST `/calls/{id}/reject` | Empty JSON, receiver only, returns updated call. |
| POST `/calls/{id}/end` | Empty JSON. Caller cancels ringing, either participant ends an accepted or connected call. |
| POST `/calls/{id}/token` | `{deviceId}`. For accepted/connected calls only. Receiver must use the accepting device ID. Returns `{token,wsUrl,roomName,expiresAt}`. |
| GET `/push` | `{android:boolean,webVapidPublicKey:string}`. |
| POST `/devices` | Android `{platform:"android",token}`. Web `{platform:"web",endpoint,p256dh,auth}`. Returns registration record. |
| DELETE `/devices/{id}` | Removes own registration and pending notifications. 204. |

A 404 means absent or inaccessible, 409 means the operation conflicts with current state, 429 means slow down, and 507 means the attachment quota is exceeded. A call moves from `ringing` to `accepted` to `connected` once both peers join. Terminal states are `ended,rejected,cancelled,missed,failed`. Reconcile with the call record after reconnect or notification tap. Reuse the same device ID for retrying accept. Request a new room token when an expired token prevents reconnecting. Tokens grant media publication and subscription for the call room only.

Push payload is `{event,id,expiresAt}`, with `incoming_call` referring to a call and `new_message` referring to a conversation. Deduplicate notifications and discard expired pushes. Load current state with the user's credentials before accepting a call. Show generic notification content.
