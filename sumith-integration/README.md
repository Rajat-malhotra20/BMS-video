# Sumith CCU RTMP push automation

`RtmpForwarder.java` keeps one supervised `ffmpeg` process per camera
running, each pushing that camera's feed to our RTMP ingest
(`rtmp://bms-media.gna.energy:31935`, baked into the file as `DEST_HOST`/
`DEST_PORT`) under its own stream key. It doesn't reimplement RTMP — it
drives `ffmpeg`, which already handles the RTMP handshake/muxing correctly.

## Requirements

- Java 8+
- `ffmpeg` on `PATH` on the machine this runs on (your CCU host)

## Set up your camera list

For a fleet this size, don't hand-type the CSV — generate it from whatever
inventory your CCU already has.

`GenerateCameraCsv.java` does that: point it at your database and it
writes `cameras.csv` for the whole fleet in one run. Fill in the three
spots marked `FILL IN` in that file (your JDBC connection, your query, and
the column names your schema actually uses), then:

```bash
javac -d out src/main/java/com/bms/sumithforward/GenerateCameraCsv.java
# add your DB's JDBC driver jar to the classpath, e.g. mysql-connector-j-*.jar
java -cp "out:path/to/your-jdbc-driver.jar" com.bms.sumithforward.GenerateCameraCsv cameras.csv
```

If your fleet inventory lives behind an HTTP API instead of a direct DB
connection, swap the JDBC call in `fetchRows()`/`main()` for an HTTP
request + JSON parse — the CSV-writing part doesn't change.

Each output line is `streamKey,inputUrl`:

```
bus123_cam1,rtsp://127.0.0.1:8554/cam1
bus123_cam2,rtsp://127.0.0.1:8554/cam2
```

(`cameras.csv.example` shows the same format by hand, useful for testing
with 1-2 cameras before running the full generator.)

- `streamKey` — must be unique across your whole fleet (becomes the path on
  our server).
- `inputUrl` — whatever `ffmpeg` can already read (a local RTSP restream,
  an SDK-provided URL, etc). This tool only handles getting that feed
  onward to us — it doesn't change how you pull video off the OBU in the
  first place.

## Compile and run

```bash
javac -d out src/main/java/com/bms/sumithforward/RtmpForwarder.java
java -cp out com.bms.sumithforward.RtmpForwarder cameras.csv
```

Each camera gets pushed to:

```
rtmp://bms-media.gna.energy:31935/<streamKey>
```

Every camera's push runs independently and restarts itself (5s backoff) if
`ffmpeg` exits for any reason (network blip, source glitch, etc) — one
camera failing doesn't affect the others.

## Note on encoding

The default command uses `-c copy` (remux only, no re-encode — cheap on
CPU). If your `inputUrl`'s codec isn't RTMP/FLV-compatible, replace `-c
copy` in `runFfmpeg()` with real encode settings (e.g. `-c:v libx264
-preset veryfast`).

## Running many cameras at once

For a large fleet, run this as a long-lived service (systemd unit, etc.)
rather than a foreground process — it's designed to run forever, spawning
and re-spawning one `ffmpeg` per camera for as long as the JVM is alive.
