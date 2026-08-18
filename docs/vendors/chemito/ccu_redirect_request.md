Subject: Request — bulk redirect of OBU fleet (7000 units) to new server

Hi,

We need to point the OBU fleet currently reporting to your CCU over to our server instead. Per the CCU-SCU Communication Protocol (v1.8) you're running, this doesn't require touching any device physically — it's a command your CCU already has the ability to send.

**What we need you to run, once, across all currently-connected OBU sessions:**

1. `Set Primary IP Address (CCU→OBU)` — packet header `133` (§79)
2. `Set Port Number Primary IP (CCU→OBU)` — packet header `136` (§82)

New destination:
- IP/Host: <PUBLIC_HOST_OR_IP>
- Port: <PORT>

If you want to keep a fallback path live, also set the secondary IP/port the same way:
3. `Set Secondary IP Address (CCU→OBU)` — packet header `135` (§81)
4. `Set Port Number Secondary IP (CCU→OBU)` — packet header `137` (§83)

**For any OBU not currently GPRS-connected** when you run this (so it won't receive the GPRS commands above), please also send the SMS equivalents:
- `SET IP1 SMS` (§105)
- `SET IP2 SMS` (§106)

**One thing we need confirmed from your side:** does this OBU-CCU protocol carry the video/RTMP stream itself, or does video go over a separate channel from the telemetry link (GPS, health, CAN, APC, etc.)? The protocol doc only shows camera *settings* commands (resolution, bitrate, frame rate, etc.) — we don't see a packet for redirecting the actual video/RTMP destination. If video is a separate channel, we'll need its destination-IP/port config too.

Let us know timeline and if you need anything else from us (server cert, auth details, etc.) to make the switch.

Thanks,
Raman
