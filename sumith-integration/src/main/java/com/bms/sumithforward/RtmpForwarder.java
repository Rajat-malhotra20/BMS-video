package com.bms.sumithforward;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Reads a list of cameras from a CSV file and keeps one ffmpeg process per
 * camera running, each pushing that camera's feed onward as RTMP to our
 * ingest server under its own stream key.
 *
 * This does the muxing/pushing itself via ffmpeg (must be on PATH) rather
 * than reimplementing RTMP in Java — ffmpeg already handles the RTMP
 * handshake/FLV muxing correctly and is what most CCU/DVR stacks already
 * have available.
 *
 * CSV format, one camera per line, no header:
 *   streamKey,inputUrl
 * e.g.:
 *   bus123_cam1,rtsp://127.0.0.1:8554/cam1
 *   bus123_cam2,rtsp://127.0.0.1:8554/cam2
 *
 * inputUrl is whatever ffmpeg can already read on your side today — a
 * local RTSP restream, an SDK-provided URL, a file device, etc. This tool
 * only handles getting that feed onward to us; it doesn't change how your
 * CCU pulls video off the OBU in the first place.
 *
 * Usage:
 *   java com.bms.sumithforward.RtmpForwarder cameras.csv
 */
public final class RtmpForwarder {

    private static final Logger LOG = Logger.getLogger(RtmpForwarder.class.getName());
    private static final long RESTART_BACKOFF_MS = 5000;

    // Our RTMP ingest — see prototype/k8s/portainer-stack.yaml's bms-ingest
    // Service (nodePort 31935) and DEPLOY.md's bms-media.gna.energy DNS
    // record. Update these two if that deployment ever changes.
    private static final String DEST_HOST = "bms-media.gna.energy";
    private static final int DEST_PORT = 31935;

    public static void main(String[] args) throws Exception {
        if (args.length < 1) {
            System.err.println("Usage: java com.bms.sumithforward.RtmpForwarder <cameras.csv>");
            System.exit(1);
        }
        List<Camera> cameras = loadCameras(args[0]);

        if (cameras.isEmpty()) {
            LOG.severe("No cameras loaded from " + args[0] + " — nothing to do.");
            return;
        }

        LOG.info("Starting " + cameras.size() + " camera push(es) to rtmp://" + DEST_HOST + ":" + DEST_PORT + "/<streamKey>");

        ExecutorService pool = Executors.newFixedThreadPool(cameras.size());
        for (Camera camera : cameras) {
            pool.submit(() -> superviseForever(camera));
        }

        // Runs until killed (Ctrl+C / service stop). Each camera's push
        // loop restarts itself independently on failure.
        pool.shutdown();
        pool.awaitTermination(Long.MAX_VALUE, java.util.concurrent.TimeUnit.DAYS);
    }

    /** Runs one camera's ffmpeg push, restarting it with a backoff delay every time it exits. */
    private static void superviseForever(Camera camera) {
        String rtmpUrl = "rtmp://" + DEST_HOST + ":" + DEST_PORT + "/" + camera.streamKey;
        while (true) {
            LOG.info("[" + camera.streamKey + "] starting push: " + camera.inputUrl + " -> " + rtmpUrl);
            try {
                int exitCode = runFfmpeg(camera, rtmpUrl);
                LOG.warning("[" + camera.streamKey + "] ffmpeg exited with code " + exitCode + ", restarting in "
                        + (RESTART_BACKOFF_MS / 1000) + "s");
            } catch (IOException e) {
                LOG.log(Level.SEVERE, "[" + camera.streamKey + "] failed to start ffmpeg: " + e.getMessage(), e);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return;
            }
            sleepQuietly(RESTART_BACKOFF_MS);
        }
    }

    /** Launches ffmpeg for one camera and blocks until it exits, streaming its stderr to our log. */
    private static int runFfmpeg(Camera camera, String rtmpUrl) throws IOException, InterruptedException {
        // -c copy: remux only, no re-encode (cheap). Drop this flag and add
        // encode settings instead if your inputUrl's codec isn't RTMP/FLV
        // compatible (e.g. needs H.264 baseline).
        ProcessBuilder pb = new ProcessBuilder(
                "ffmpeg",
                "-nostdin",
                "-re",
                "-i", camera.inputUrl,
                "-c", "copy",
                "-f", "flv",
                rtmpUrl
        );
        pb.redirectErrorStream(true);
        Process process = pb.start();

        try (BufferedReader reader = new BufferedReader(
                new InputStreamReader(process.getInputStream(), StandardCharsets.UTF_8))) {
            String line;
            while ((line = reader.readLine()) != null) {
                LOG.fine("[" + camera.streamKey + "] ffmpeg: " + line);
            }
        }
        return process.waitFor();
    }

    private static List<Camera> loadCameras(String csvPath) throws IOException {
        List<Camera> cameras = new ArrayList<>();
        for (String line : Files.readAllLines(Paths.get(csvPath), StandardCharsets.UTF_8)) {
            String trimmed = line.trim();
            if (trimmed.isEmpty() || trimmed.startsWith("#")) {
                continue;
            }
            String[] parts = trimmed.split(",", 2);
            if (parts.length != 2) {
                LOG.warning("Skipping malformed line in " + csvPath + ": " + line);
                continue;
            }
            cameras.add(new Camera(parts[0].trim(), parts[1].trim()));
        }
        return cameras;
    }

    private static void sleepQuietly(long millis) {
        try {
            Thread.sleep(millis);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    private static final class Camera {
        final String streamKey;
        final String inputUrl;

        Camera(String streamKey, String inputUrl) {
            this.streamKey = streamKey;
            this.inputUrl = inputUrl;
        }
    }
}
