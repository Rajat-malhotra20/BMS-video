# Camera/CCTV stream flow — request path and the "only some cams work" issue

## End-to-end path

```
BMS-Frontend (CctvMonitoring.tsx)
  -> mediaClient.getStreams(busId)              GET https://bms-media.gna.energy/api/stream/{id}?token=...
       -> backend: handleStreamLive (api.go)
            -> camsForBus() / ensureAllCams()   fans out to one channel per camera
                 -> a.unwired(key)?             skip silently if cached "no camera" (10 min memory)
                 -> a.ensureStream(bus, cam)
                      -> services.StreamService
                           -> vendors registry -> chemitoapi.Adapter.ResolveLiveSource
                                -> acquireSlot(channelKey)      in-process cap: maxActiveChannels = 16
                                -> resolveURL()
                                     -> session()                shared login, one key per process
                                     -> client.LivePorts()        account's *actual* entitled ports
                                     -> client.LiveVideoURL(...)  vendor's playable FLV URL
  <- directUrl: "/api/flv/{bus}_{cam}"     (relative, proxied through our own backend)
  -> VideoPlayer (mpegts.js) opens         GET /api/flv/{bus}_{cam}?token=...
       -> flvHub holds one real upstream device connection per {bus}_{cam},
          shared by every viewer of that camera (flvhub.go)
```

Frontend never talks to the vendor (Chemito) directly — everything goes through
this backend, which owns the vendor login/session and does the real device
connection.

## The "2 of 9 cams work" symptom

Confirmed from backend logs (2026-09-14): for bus `DL1PD8659`, only cam 5 and
cam 6 ever got a `GET /api/flv/...` session opened in a ~2 minute window; cams
1-4, 7-9 never even appeared in the log as start attempts.

Root cause chain:

1. `ensureAllCams` (api.go) skips any channel already marked **unwired**
   without logging anything (api.go, the `if a.unwired != nil && a.unwired(key)`
   check) — so a channel that failed once stays silently absent from every
   subsequent `/api/stream/{id}` response for up to `flvUnwiredMemory` (10 min).
2. A channel gets marked unwired when the vendor's connection attempt ends in
   an **immediate EOF at connect** (`errUnwired`, flvhub.go) *and* a sibling
   channel on the same bus is already confirmed live (`siblingLive`) — the
   sibling check exists so a whole-device outage isn't mistaken for "no
   camera," but it does **not** check whether the account is simply out of
   concurrent-session capacity.
3. The vendor's own dashboard (a separate login, e.g. Ceiba2) showed all 9
   cameras streaming live at the same time — proving the hardware is wired for
   all 9.

**Update (confirmed 2026-09-14): the "low concurrent-capacity" theory below is
ruled out.** The account really does have 4 ports x 4 channels = 16
concurrent-channel capacity, confirmed directly (not just assumed from the
vendor's doc). `maxActiveChannels` has been raised to 16 to match (see
"Bus-switch fix" section below) — and even before that change, 9 channels for
one bus was never close to exhausting a 12-channel software cap, let alone a
real 16-channel ceiling. So capacity was never the bottleneck for this
specific symptom.

~~That rules out "no camera" and points at the account (`media-live`) having a
lower real concurrent-channel entitlement than we throttle to in software.~~

**Current hypothesis: per-channel authorization, not concurrency.** Chemito
may scope an account to specific channel *numbers* on a device, independent of
how many concurrent slots it's allowed — e.g. `media-live` might only be
authorized for channels 5 and 6 on this `terid`, while Ceiba2's account is
authorized for all 9, regardless of either account's concurrent-session
ceiling. That would explain the exact pattern seen: same hardware (proven
wired via Ceiba2), same vendor API, only 2 of 9 channels ever answer for this
account. This needs the vendor (or whoever manages the Chemito account) to
confirm which channel numbers `media-live` is actually authorized to view on
that device — a permissions question, not a code fix.

The mechanical bug is still real regardless of root cause, though: an
account-capacity rejection and a genuinely unwired channel currently look
identical to this code (both show up as an immediate EOF on connect), so
*whichever* the real cause turns out to be, it gets mislabeled as "no camera"
and cached for 10 minutes (`flvUnwiredMemory`) — which is why the same 2
cameras "win" every time and the rest look permanently broken even when they
might just be an authorization gap.

## What's been added to confirm capacity (still useful diagnostically)

`vendors/chemitoapi/adapter.go`'s `resolveURL` logs, on every resolve attempt:

```
chemitoapi: {terid}_{channel}: account offers N live port(s); M channel(s) currently held by this process
```

Now that the port count is confirmed (4 ports), this is mostly useful for
watching `M` — how many channels this process itself believes are active —
to sanity-check the bus-switch fix below rather than to hunt for a low
entitlement.

## Bus-switch fix (added after this doc was first written)

Switching buses in the UI used to leave the old bus's channels holding their
vendor capacity slots for up to `flvIdleGrace` (60s) after the last viewer
left, because nothing told the backend the switch was deliberate — see
`CctvMonitoring.tsx`'s old comment: *"the device session frees itself on the
server ~60s later on its own."* With 9 cameras per bus and a cap of 12 (now
16), a fast bus switch could starve the new bus of slots for up to a minute.

Fixed with:
- `flvHub.ExpediteBus(bus)` (`flvhub.go`) — immediately releases any channel
  of `bus` that already has zero viewers, skipping `idleGrace`. Never touches
  a channel someone else is still watching.
- `POST /api/bridge/stop-bus?bus={id}` (`bridge_unified.go`, `main.go`) —
  the HTTP entry point for the above.
- Frontend: `mediaClient.stopBus(id)` fires this (fire-and-forget) from
  `CctvMonitoring.tsx`'s bus-switch effect cleanup, right when the operator
  navigates to a different bus.
- `maxActiveChannels` raised from 12 to 16 (`vendors/chemitoapi/adapter.go`)
  since the 4-channel buffer that number used to reserve was there
  specifically for slow bus-switch releases, which are now fast.

## Frontend behavior worth knowing about

`BMS-Frontend/src/api/mediaClient.ts` hardcodes the API base
(`https://bms-media.gna.energy/api`) and a static bearer token as a `?token=`
query param — it does not use the app's usual `VITE_*_URL` env-var pattern
that `fleetOpsClient`/`reportsClient`/`paymentClient` follow.

`CctvMonitoring.tsx`'s retry loop regex-matches the error message
`"may not be wired"` (the text of `errUnwired` in `flvhub.go`) to back off to a
5-minute retry instead of 4s/8s — so that string is effectively a cross-repo
contract. If the backend's unwired-vs-capacity distinction is fixed later,
make sure the message/error code the frontend keys off still means what the
frontend assumes it means.

## Next steps

- Confirm with the vendor which channel numbers the `media-live` account is
  actually authorized to view on `terid` `00C4004064` (bus `DL1PD8659`) — the
  live hypothesis is a per-channel authorization gap, not a concurrency limit
  (concurrency is now confirmed at 16, and was never the bottleneck for this
  specific symptom).
- If confirmed as an authorization gap, that's a vendor-account change
  request, not a code fix — this backend can only relay what the vendor's API
  reports for the channels it's allowed to see.
- Separately, still worth doing regardless of root cause: have the backend
  distinguish a genuine vendor-capacity rejection from a truly unwired
  channel (it already has a `capacity_limit` domain error for the in-process
  cap — see `acquireSlot`) instead of letting any connect-time EOF get folded
  into `errUnwired` and cached as "no camera" for 10 minutes.
