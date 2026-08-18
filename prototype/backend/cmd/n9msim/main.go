// Command n9msim simulates a Chemito N9M device dialing into this project's
// n9mserver, so the full connect -> register -> REQUESTALIVEVIDEO -> media
// pipeline can be exercised end-to-end without real hardware. It is a test
// tool only, not a vendor client — real devices implement this same protocol
// themselves.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"mediamtx-console/vendorclients/n9m"
)

func main() {
	signalAddr := flag.String("signal", "localhost:9500", "n9mserver signaling address")
	mediaAddr := flag.String("media", "localhost:9501", "n9mserver media address")
	dsno := flag.String("dsno", "SIM000001", "simulated device serial number")
	h264Path := flag.String("h264", "", "path to a raw H.264 Annex-B elementary stream file to loop as fake live video")
	flag.Parse()

	if *h264Path == "" {
		log.Fatal("n9msim: -h264 is required (path to a raw .h264 Annex-B file)")
	}
	videoData, err := os.ReadFile(*h264Path)
	if err != nil {
		log.Fatalf("n9msim: read -h264 file: %v", err)
	}
	log.Printf("n9msim: loaded %d bytes of test video from %s", len(videoData), *h264Path)

	nc, err := net.Dial("tcp", *signalAddr)
	if err != nil {
		log.Fatalf("n9msim: dial signaling %s: %v", *signalAddr, err)
	}
	conn := n9m.NewConn(nc)
	conn.Start()
	defer conn.Close()

	session := n9m.NewSessionID()
	log.Printf("n9msim: connecting as DSNO=%s session=%s", *dsno, session)

	resp, err := conn.Connect(session, n9m.ConnectParams{
		NET: 2, DevName: "n9msim", DSNO: *dsno, CPN: "n9msim-test",
	}, 10*time.Second)
	if err != nil {
		log.Fatalf("n9msim: CONNECT failed: %v", err)
	}
	log.Printf("n9msim: connected, server PRO=%s MASKCMD=%d", resp.PRO, resp.MaskCmd)

	stopHeartbeat := conn.StartHeartbeatLoop(session, 30*time.Second, func(err error) {
		log.Printf("n9msim: heartbeat error: %v", err)
	})
	defer stopHeartbeat()

	log.Printf("n9msim: waiting for REQUESTALIVEVIDEO ... (call POST /api/bridge/n9m/start now)")

	for env := range conn.Notifications {
		if env.Module != n9m.ModuleMediaStream || env.Operation != n9m.OpRequestAliveVideo {
			log.Printf("n9msim: ignoring unexpected %s/%s", env.Module, env.Operation)
			continue
		}

		var params n9m.RequestAliveVideoParams
		if err := json.Unmarshal(env.Parameter, &params); err != nil {
			log.Printf("n9msim: decode REQUESTALIVEVIDEO parameter: %v", err)
			continue
		}
		log.Printf("n9msim: got REQUESTALIVEVIDEO stream=%s type=%d channel=%#x", params.StreamName, params.StreamType, params.Channel)

		if err := conn.SendResponse(n9m.ModuleMediaStream, n9m.OpRequestAliveVideo, env.Session, n9m.RequestAliveVideoResponse{
			SSRC: params.SSRC, StreamName: params.StreamName, StreamType: params.StreamType,
		}); err != nil {
			log.Printf("n9msim: send REQUESTALIVEVIDEO response: %v", err)
			continue
		}

		go streamMedia(*mediaAddr, session, *dsno, params, videoData)
	}
	log.Printf("n9msim: signaling connection closed: %v", conn.Err())
}

// streamMedia dials a fresh media connection, binds it via CREATESTREAM, and
// loops the loaded H.264 data as PayloadLiveVideo frames until the connection
// is closed (e.g. by the server issuing CONTROLSTREAM stop, which closes the
// media connection on the server side per StopLiveVideo).
func streamMedia(mediaAddr, session, dsno string, params n9m.RequestAliveVideoParams, videoData []byte) {
	nc, err := net.Dial("tcp", mediaAddr)
	if err != nil {
		log.Printf("n9msim: dial media %s: %v", mediaAddr, err)
		return
	}
	conn := n9m.NewConn(nc)
	conn.Start()
	defer conn.Close()

	if err := conn.CreateStream(session, n9m.CreateStreamParams{
		StreamName: params.StreamName, DSNO: dsno,
	}, 10*time.Second); err != nil {
		log.Printf("n9msim: CREATESTREAM failed: %v", err)
		return
	}
	log.Printf("n9msim: media channel bound for stream %s, streaming test video...", params.StreamName)

	if err := conn.NotifyMediaTaskStart(session, n9m.MediaTaskStartParams{
		StreamName: params.StreamName, PT: int(n9m.PayloadLiveVideo), SSRC: params.SSRC,
	}); err != nil {
		log.Printf("n9msim: MEDIATASKSTART notify failed (continuing anyway): %v", err)
	}

	const chunkSize = 4096
	ssrc := params.SSRC
	if ssrc == 0 {
		ssrc = 3
	}

	for {
		for off := 0; off < len(videoData); off += chunkSize {
			end := off + chunkSize
			if end > len(videoData) {
				end = len(videoData)
			}
			err := conn.SendMediaFrame(n9m.Header{Version: 1, PayloadType: n9m.PayloadLiveVideo, SSRC: uint16(ssrc)}, videoData[off:end])
			if err != nil {
				if err == io.EOF {
					return
				}
				log.Printf("n9msim: media send stopped: %v", err)
				return
			}
			time.Sleep(20 * time.Millisecond) // pace roughly like a real-time stream
		}
		fmt.Fprintln(os.Stderr, "n9msim: looped test video")
	}
}
