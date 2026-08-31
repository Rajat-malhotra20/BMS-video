package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"
)

// flvHub keeps ONE upstream vendor connection per {bus}_{cam} and fans its
// bytes out to every attached viewer.
//
// The old proxy opened a separate vendor session per viewer, which made
// viewers expensive in the one currency these devices actually ration:
// concurrent channel sessions. Chemito's API caps channels, not people
// (PMIDTC_CIPLAPIS.xlsx §4: "A port can configure up to 4 channels",
// "16-way video at most at the same time") — so with one upstream per
// channel, viewer count stops mattering entirely. Ten people on one camera
// is one device session, the same as one person.
//
// It also makes reconnection safe. Per-viewer upstreams meant N players
// retrying in parallel, and since Chemito has no session-close call every
// retry leaked another session — confirmed live 2026-08-24, a 2s retry loop
// across 4 channels took a healthy device to HTTP 408 on every channel.
// With a single upstream per channel there is exactly one reconnect loop,
// so it can be fast (see flvReconnectMin) instead of defensively slow.
type flvHub struct {
	// idleGrace is how long a channel keeps its upstream open after the
	// last viewer leaves, so tab-switching doesn't churn device sessions.
	idleGrace time.Duration

	mu    sync.Mutex
	chans map[string]*flvChannel

	// unwired remembers channels the device answered with an immediate EOF
	// — a channel number it reports but has no camera on. Keyed by
	// {bus}_{cam}, valued at when it was last proven so the memory expires
	// if a camera is ever installed. See flvUnwiredMemory.
	unwired map[string]time.Time
}

func newFLVHub() *flvHub {
	return &flvHub{
		idleGrace: flvIdleGrace,
		chans:     make(map[string]*flvChannel),
		unwired:   make(map[string]time.Time),
	}
}

// Live reports whether key currently has an upstream that has produced
// playable video, and whether the hub has a channel for it at all.
//
// This is what /api/fleet and /api/bus need to stop lying. A direct entry
// is registered when a stream is started and removed only by an explicit
// stop, so on its own it says "this channel was started once", not "this
// channel is streaming" — which is how the fleet view came to list cams 8
// and 9 of DLPD8611 as live when they have no camera at all. The hub is the
// only component that connects, so it is the only one that knows.
func (h *flvHub) Live(key string) (live, known bool) {
	h.mu.Lock()
	c, ok := h.chans[key]
	h.mu.Unlock()
	if !ok {
		return false, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseHdr != nil, true
}

// markUnwired records that key has no camera behind it.
func (h *flvHub) markUnwired(key string) {
	h.mu.Lock()
	h.unwired[key] = time.Now()
	h.mu.Unlock()
}

// Unwired reports whether key was recently proven to have no camera on it.
//
// This exists because these DVRs report more channels than they have
// cameras (9 reported, 7 wired — confirmed live 2026-08-26 on DL1PD8584
// and 2026-08-31 on DLPD8611), and every attempt on an unwired one still
// costs a connection against the device's small concurrent-session budget.
// Measured on DLPD8611: while channels 8 and 9 retried, working channels 6
// and 7 were refused with HTTP 408 "out of concurrent sessions". Not
// offering a channel that cannot work is what keeps the ones that can.
func (h *flvHub) Unwired(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, ok := h.unwired[key]
	if !ok {
		return false
	}
	if time.Since(at) > flvUnwiredMemory {
		delete(h.unwired, key)
		return false
	}
	return true
}

const (
	// flvSubBuffer is how many tags may queue for one viewer before its
	// slow connection starts losing them. Dropping tags is deliberate:
	// a viewer that can't keep up must not stall the channel for everyone,
	// and the picture recovers at the next keyframe. Tags are shared by
	// reference across viewers, so this bounds queue depth, not copies.
	flvSubBuffer = 256

	// flvIdleGrace keeps the upstream alive after the last viewer leaves —
	// reloading a page shouldn't cost a new device session.
	//
	// A minute, not the 10s it was: re-opening is the expensive operation,
	// not staying open. Measured on DLPD8611 2026-08-31, a page reload
	// released channels 6 and 7, and re-opening them 10s later was refused
	// with HTTP 408 "out of concurrent sessions" — the device had not yet
	// let go of the session we just abandoned. Holding the connection we
	// already have costs the same one session and skips that race.
	flvIdleGrace = 60 * time.Second

	// flvUnwiredMemory is how long a channel proven to have no camera is
	// left out of listings before it is offered again. Long enough to stop
	// a wall display retrying it all day, short enough that a camera
	// installed on that channel shows up without a redeploy.
	flvUnwiredMemory = 10 * time.Minute

	// flvReconnectMin/Max bound the single reconnect loop. Fast first
	// (a moving vehicle's signal blip is over in well under a second),
	// backing off while the device stays dark so an offline bus isn't
	// hammered forever.
	flvReconnectMin = 200 * time.Millisecond
	flvReconnectMax = 5 * time.Second

	// flvFirstByteTimeout is how long a viewer waits for a channel that
	// has never produced video before being told it's unavailable. Only
	// applies before the first byte — once a viewer is receiving, it is
	// never disconnected.
	flvFirstByteTimeout = 20 * time.Second

	// flvHeartbeatEvery is how often a silent channel emits a keep-alive
	// tag, so a viewer's connection (and any proxy between) stays open
	// across a vendor outage instead of timing out.
	flvHeartbeatEvery = 2 * time.Second

	// flvGOPCap bounds the replayed group-of-pictures. A late joiner gets
	// the codec headers plus everything since the last keyframe, so it
	// renders immediately rather than waiting for the next one; the cap
	// stops a stream with sparse keyframes from growing without limit.
	//
	// Deliberately modest: this is held per channel, and a fleet can have
	// dozens live at once against a 1Gi pod. Measured 2026-08-26, a main
	// codestream runs ~1MB/s, so 2MB is a couple of seconds of replay —
	// enough to start decoding, small enough that 30 channels cost 60MB.
	flvGOPCap = 2 << 20

	// flvAudioProbe is how long the reader waits for a real audio tag
	// before committing the FLV header's audio bit, when audio was
	// requested. Chemito advertises audio on every stream whether or not
	// the camera has a microphone (measured 2026-08-27: audio=0 still sets
	// 0x05 and sends zero audio tags), and a player that believes the
	// header waits forever for a track that never comes. Observing the
	// stream is the only way to tell; 1.5s is comfortably longer than the
	// ~130ms audio tag interval measured on a camera that does have one.
	flvAudioProbe = 1500 * time.Millisecond

	// flvReconnectGapMS is the nominal frame gap inserted across a
	// reconnect, so the first tag of a new connection lands just after the
	// last tag of the old one rather than on top of it. ~30fps.
	flvReconnectGapMS = 33

	// flvMaxTagSize guards against a desynced parse allocating wildly. FLV
	// carries a 24-bit size, but a real tag is orders of magnitude smaller.
	flvMaxTagSize = 16 << 20
)

// errUnwired is the connect-time EOF that means this channel number exists
// on the device but has no camera on it. Its text is what a viewer sees in
// the 502 body, so a frontend can tell "never going to work" apart from
// "having a bad minute" without parsing status codes.
var errUnwired = errors.New("vendor closed the stream connection (channel may not be wired to a camera)")

// flvSub is one attached viewer.
type flvSub struct {
	ch chan []byte
}

// flvChannel is one {bus}_{cam}: a single upstream reader broadcasting to
// every subscriber.
type flvChannel struct {
	hub      *flvHub
	key      string
	resolve  func(context.Context) (string, error)
	hasAudio bool
	cancel   context.CancelFunc

	// ready closes when the channel has produced a playable init segment;
	// deadOnArrival closes if it failed before ever producing one. A
	// channel that dies *after* going live closes neither again — its
	// viewers stay attached across the outage.
	ready         chan struct{}
	deadOnArrival chan struct{}
	readyOnce     sync.Once
	deadOnce      sync.Once

	// mu guards everything below. RWMutex because viewers read the cached
	// init segment far more often than the single reader mutates it.
	mu       sync.RWMutex
	holders  int // viewers attached or attaching; 0 starts the idle timer
	subs     map[*flvSub]struct{}
	baseHdr  []byte // FLV header + PreviousTagSize0, audio flag corrected
	script   []byte // onMetaData tag, if the vendor sent one
	videoSeq []byte // AVC decoder configuration record tag
	audioSeq []byte // AAC sequence header tag
	gop      []byte // tags since the last keyframe
	lastErr  error

	// Counters for GET /api/hub. Cheap to keep, and the difference between
	// diagnosing a bad camera in seconds and grepping logs for an hour.
	startedAt   time.Time
	reconnects  int
	droppedTags int64
	bytesOut    int64

	// lastOutTS is the highest timestamp handed to viewers so far. Each
	// upstream connection restarts its timestamps at zero, so reconnects
	// continue from here instead of jumping backwards. See rebaseTag.
	lastOutTS uint32
}

// serve attaches one viewer to key's channel, starting the channel if this
// is the first viewer. It returns only when the viewer disconnects.
func (h *flvHub) serve(w http.ResponseWriter, r *http.Request, key string, resolve func(context.Context) (string, error), hasAudio bool) {
	// A channel already proven to have no camera is refused here rather
	// than by trying again: a bookmarked tile retrying every few seconds
	// would otherwise keep spending the device's concurrent sessions on a
	// connection that always EOFs. Same 502 and same text either way, so
	// a client cannot tell this shortcut from the real attempt.
	if h.Unwired(key) {
		http.Error(w, fmt.Sprintf("%s: %v", key, errUnwired), http.StatusBadGateway)
		return
	}

	c := h.channel(key, resolve, hasAudio)

	// Wait for something playable. This is the ONLY point at which a
	// viewer can be refused — past it, the connection is held open no
	// matter what the vendor does.
	//
	// ready is checked on its own first: a channel that failed on its very
	// first attempt and recovered later has BOTH ready and deadOnArrival
	// closed, and a bare select would pick between them at random — handing
	// a 502 to a viewer of a camera that is currently streaming.
	select {
	case <-c.ready:
	default:
		select {
		case <-c.ready:
		case <-c.deadOnArrival:
			h.release(c)
			http.Error(w, fmt.Sprintf("%s: %v", key, c.err()), http.StatusBadGateway)
			return
		case <-r.Context().Done():
			h.release(c)
			return
		case <-time.After(flvFirstByteTimeout):
			h.release(c)
			http.Error(w, key+": vendor sent no video in time", http.StatusGatewayTimeout)
			return
		}
	}

	sub, backlog := c.subscribe()
	defer func() {
		c.unsubscribe(sub)
		h.release(c)
	}()

	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)
	out := flushWriter{w: w, f: flusher}
	if flusher != nil {
		flusher.Flush()
	}

	// Codec headers + the current GOP, so playback starts now instead of
	// at the next keyframe.
	if _, err := out.Write(backlog); err != nil {
		return
	}

	beat := time.NewTicker(flvHeartbeatEvery)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case chunk := <-sub.ch:
			if _, err := out.Write(chunk); err != nil {
				return // viewer went away mid-write
			}
			beat.Reset(flvHeartbeatEvery)
		case <-beat.C:
			// Vendor has gone quiet. Keep the socket warm rather than
			// letting the browser (or an intermediary) time the tile out
			// — the reconnect loop is still working underneath.
			if _, err := out.Write(flvKeepAliveTag); err != nil {
				return
			}
		}
	}
}

// channel returns key's channel, creating and starting it if needed, and
// counts this caller as one holder (released via release).
func (h *flvHub) channel(key string, resolve func(context.Context) (string, error), hasAudio bool) *flvChannel {
	h.mu.Lock()
	defer h.mu.Unlock()

	if c, ok := h.chans[key]; ok {
		c.hold()
		return c
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &flvChannel{
		hub: h, key: key, resolve: resolve, hasAudio: hasAudio, cancel: cancel,
		ready:         make(chan struct{}),
		deadOnArrival: make(chan struct{}),
		subs:          make(map[*flvSub]struct{}),
		startedAt:     time.Now(),
	}
	c.holders = 1
	h.chans[key] = c
	go c.run(ctx)
	return c
}

// release drops one holder and, once nobody is left, stops the upstream
// after idleGrace.
func (h *flvHub) release(c *flvChannel) {
	c.mu.Lock()
	c.holders--
	idle := c.holders == 0
	c.mu.Unlock()
	if !idle {
		return
	}

	time.AfterFunc(h.idleGrace, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		c.mu.RLock()
		stillIdle := c.holders == 0
		c.mu.RUnlock()
		if !stillIdle || h.chans[c.key] != c {
			return
		}
		delete(h.chans, c.key)
		c.cancel()
		log.Printf("flv hub: %s: no viewers, released device session", c.key)
	})
}

func (c *flvChannel) hold() {
	c.mu.Lock()
	c.holders++
	c.mu.Unlock()
}

func (c *flvChannel) err() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastErr
}

func (c *flvChannel) subscribe() (*flvSub, []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &flvSub{ch: make(chan []byte, flvSubBuffer)}
	c.subs[s] = struct{}{}

	// Snapshot under the same lock the reader broadcasts under, so this
	// viewer can't miss a tag or receive one twice.
	backlog := make([]byte, 0, len(c.baseHdr)+len(c.script)+len(c.videoSeq)+len(c.audioSeq)+len(c.gop))
	for _, part := range [][]byte{c.baseHdr, c.script, c.videoSeq, c.audioSeq, c.gop} {
		backlog = append(backlog, part...)
	}
	return s, backlog
}

func (c *flvChannel) unsubscribe(s *flvSub) {
	c.mu.Lock()
	delete(c.subs, s)
	c.mu.Unlock()
}

// run is the single reconnect loop for this channel.
func (c *flvChannel) run(ctx context.Context) {
	backoff := flvReconnectMin
	for ctx.Err() == nil {
		delivered, err := c.pumpOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		c.reconnects++
		c.mu.Unlock()
		if delivered {
			backoff = flvReconnectMin // a working connection earns a fast retry
		}
		c.noteErr(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > flvReconnectMax {
			backoff = flvReconnectMax
		}
	}
}

func (c *flvChannel) noteErr(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.lastErr = err
	live := c.baseHdr != nil
	c.mu.Unlock()

	if live {
		// Already played once: viewers stay attached on heartbeats while
		// the loop reconnects. Not an error the viewer needs to see.
		log.Printf("flv hub: %s: upstream dropped, reconnecting: %v", c.key, err)
		return
	}
	// Never produced a frame and the device EOF'd on connect: this channel
	// has no camera. Remember it so listings stop offering it and nobody
	// spends device sessions retrying it.
	if errors.Is(err, errUnwired) {
		c.hub.markUnwired(c.key)
	}
	c.deadOnce.Do(func() { close(c.deadOnArrival) })
}

// pumpOnce holds one upstream connection open, broadcasting as it reads.
// delivered reports whether any bytes reached subscribers, which decides
// whether the reconnect backoff resets.
func (c *flvChannel) pumpOnce(ctx context.Context) (delivered bool, err error) {
	url, err := c.resolve(ctx)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := flvClient.Do(req)
	if err != nil {
		// Immediate EOF here is the relay accepting the TCP connection and
		// dropping it — in practice a channel with no camera wired to it,
		// distinct from the 408 below. errUnwired is what tells noteErr to
		// stop offering this channel; the wrapped err keeps the detail.
		return false, fmt.Errorf("%w: %w", errUnwired, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail := ""
		if resp.StatusCode == http.StatusRequestTimeout {
			detail = " (device did not deliver video: offline, asleep, or out of concurrent sessions)"
		}
		return false, fmt.Errorf("vendor returned HTTP %d%s", resp.StatusCode, detail)
	}

	body := &stallReader{r: resp.Body, timeout: flvStallTimeout, closer: resp.Body}

	var hdr [9]byte
	n, err := io.ReadFull(body, hdr[:])
	if err != nil {
		return false, fmt.Errorf("short FLV header (%d bytes): %w", n, err)
	}
	base := append([]byte{}, hdr[:]...)
	var prev [4]byte
	pn, perr := io.ReadFull(body, prev[:])
	base = append(base, prev[:pn]...)

	// The header's audio bit is committed from what the stream actually
	// carries, not from what we asked for. Chemito advertises audio on
	// every stream — measured 2026-08-27, audio=0 still yields header 0x05
	// with zero audio tags — and a player that believes it waits forever
	// for a track that never arrives: bytes accumulate, the decoder never
	// initializes, the picture stays frozen. Confirmed live 2026-08-24 on
	// DL1PD8587, where channels advertising 0x05 with no audio pulled
	// 10-29MB each without rendering a frame.
	//
	// So when audio is requested, hold the header (and the tags behind it)
	// until either an audio tag proves the camera has a microphone or the
	// probe window expires, then publish a header that tells the truth.
	// A camera with no mic degrades to video instead of hanging.
	var (
		commitMu  sync.Mutex
		pending   [][]byte
		committed bool
	)
	commitLocked := func(withAudio bool) {
		if committed {
			return
		}
		committed = true
		if !withAudio && len(base) >= 5 && string(base[0:3]) == "FLV" {
			base[4] &^= 0x04
		}
		c.setBase(base)
		for _, t := range pending {
			c.publishTag(t[0], t[11:len(t)-4], t)
		}
		pending = nil
	}
	commit := func(withAudio bool) {
		commitMu.Lock()
		defer commitMu.Unlock()
		commitLocked(withAudio)
	}
	// queue holds tags behind an uncommitted header, so they are replayed
	// in order once the audio bit is decided.
	queue := func(tagType byte, data, tag []byte) {
		commitMu.Lock()
		if !committed {
			pending = append(pending, tag)
			if tagType == 0x08 {
				commitLocked(true) // a real audio tag: the camera has a microphone
			}
			commitMu.Unlock()
			return
		}
		commitMu.Unlock()
		c.publishTag(tagType, data, tag)
	}

	if !c.hasAudio {
		commit(false) // nothing to probe for — publish immediately
	} else {
		// On a timer, not on tag arrival: a camera that sends a header and
		// then goes quiet must still produce a playable stream.
		probe := time.AfterFunc(flvAudioProbe, func() { commit(false) })
		defer probe.Stop()
	}

	// Every connection's timestamps start near zero. Shift this one so it
	// continues the viewer's timeline: without this a reconnect hands the
	// player a timestamp hundreds of seconds in the past, and MSE stalls —
	// the stream keeps flowing while the picture stays frozen.
	outBase := c.resumeTS()
	inBase := int64(-1)
	if perr != nil {
		commit(false)
		return true, perr
	}

	for {
		var th [11]byte
		tn, terr := io.ReadFull(body, th[:])
		if terr != nil {
			commit(false)
			if tn > 0 {
				c.broadcast(append([]byte{}, th[:tn]...))
			}
			return true, terr
		}
		size := int(th[1])<<16 | int(th[2])<<8 | int(th[3])
		if size < 0 || size > flvMaxTagSize {
			commit(false)
			// Parse desync — the stream is no longer tag-aligned. Pass the
			// remainder through raw rather than guessing; the reconnect
			// will resynchronise from a fresh FLV header.
			c.broadcast(append([]byte{}, th[:]...))
			return true, c.drainRaw(body)
		}
		tag := make([]byte, 11+size+4)
		copy(tag, th[:])
		if _, derr := io.ReadFull(body, tag[11:11+size]); derr != nil {
			commit(false)
			c.broadcast(tag[:11])
			return true, derr
		}
		if _, terr := io.ReadFull(body, tag[11+size:]); terr != nil {
			commit(false)
			c.broadcast(tag[:11+size])
			return true, terr
		}
		inTS := int64(tagTimestamp(tag))
		if inBase < 0 {
			inBase = inTS
		}
		outTS := outBase
		if inTS > inBase {
			outTS += uint32(inTS - inBase)
		}
		setTagTimestamp(tag, outTS)
		c.noteOutTS(outTS)

		queue(th[0], tag[11:11+size], tag)
	}
}

// drainRaw forwards whatever is left without interpreting it.
func (c *flvChannel) drainRaw(body io.Reader) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			c.broadcast(append([]byte{}, buf[:n]...))
		}
		if err != nil {
			return err
		}
	}
}

// tagTimestamp reads an FLV tag's 24-bit timestamp plus its extended byte.
func tagTimestamp(tag []byte) uint32 {
	return uint32(tag[4])<<16 | uint32(tag[5])<<8 | uint32(tag[6]) | uint32(tag[7])<<24
}

// setTagTimestamp writes it back in the same split form.
func setTagTimestamp(tag []byte, ts uint32) {
	tag[4] = byte(ts >> 16)
	tag[5] = byte(ts >> 8)
	tag[6] = byte(ts)
	tag[7] = byte(ts >> 24)
}

// resumeTS returns the timestamp a new upstream connection should start
// from, so playback continues rather than jumping back to zero.
func (c *flvChannel) resumeTS() uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.lastOutTS == 0 {
		return 0 // first connection: pass the device's own timeline through
	}
	return c.lastOutTS + flvReconnectGapMS
}

func (c *flvChannel) noteOutTS(ts uint32) {
	c.mu.Lock()
	if ts > c.lastOutTS {
		c.lastOutTS = ts
	}
	c.mu.Unlock()
}

// publishTag caches the tags a late joiner needs, then broadcasts.
func (c *flvChannel) publishTag(tagType byte, data, tag []byte) {
	c.mu.Lock()
	switch {
	case tagType == 0x12: // script data — onMetaData
		c.script = tag
	// Codec 7 is H.264, codec 12 is H.265 — the same tag layout, and both
	// use packet type 0 for the decoder configuration record. Matching only
	// codec 7 (as this did) meant an H.265 camera's configuration fell
	// through to the keyframe case below, where the next keyframe promptly
	// overwrote it. The first viewer, who saw it arrive live, played fine;
	// everyone who joined afterwards got keyframes with nothing to decode
	// them against and a black tile that never errored. Confirmed live
	// 2026-08-31: DLPD8611 cams 6 and 7 are H.265, cams 1-5 H.264, and only
	// 6 and 7 kept going dark.
	case tagType == 0x09 && len(data) >= 2 && (data[0]&0x0F == 7 || data[0]&0x0F == 12) && data[1] == 0:
		c.videoSeq = tag // AVC/HEVC decoder configuration record
	case tagType == 0x08 && len(data) >= 2 && data[1] == 0:
		c.audioSeq = tag // AAC sequence header
	case tagType == 0x09 && len(data) >= 1 && data[0]>>4 == 1:
		c.gop = append(c.gop[:0], tag...) // keyframe starts a new GOP
	default:
		if len(c.gop) < flvGOPCap {
			c.gop = append(c.gop, tag...)
		}
	}
	c.fanout(tag)
	c.mu.Unlock()
	c.markReady()
}

// broadcast sends bytes that aren't a parsed tag (raw passthrough).
func (c *flvChannel) broadcast(b []byte) {
	if len(b) == 0 {
		return
	}
	c.mu.Lock()
	c.fanout(b)
	c.mu.Unlock()
	c.markReady()
}

// fanout delivers to every subscriber without ever blocking on one. Callers
// hold c.mu.
func (c *flvChannel) fanout(b []byte) {
	c.bytesOut += int64(len(b))
	for s := range c.subs {
		select {
		case s.ch <- b:
		default:
			// Viewer is behind. Drop rather than stall the channel — the
			// picture resyncs at the next keyframe.
			c.droppedTags++
		}
	}
}

// setBase records the FLV file header for viewers who join from here on.
//
// Deliberately NOT broadcast to current viewers: this runs again on every
// reconnect, and an FLV file header arriving mid-stream is not a tag — a
// player already parsing the stream reads those 13 bytes as tag data and
// desyncs. Existing viewers just keep receiving tags across the reconnect;
// new ones pick the header up from the subscribe backlog.
func (c *flvChannel) setBase(base []byte) {
	c.mu.Lock()
	c.baseHdr = base

	// The GOP goes: those frames belong to the connection that just ended.
	//
	// The decoder configuration does NOT. It describes the camera, not the
	// TCP connection, and Chemito does not reliably resend it — a reconnect
	// can drop us into the middle of a stream that never repeats its
	// AVCDecoderConfigurationRecord. Clearing it then left the channel
	// permanently unplayable for anyone joining afterwards: measured
	// 2026-08-31 on DLPD8611_6, 150KB/s of video, 13 keyframes in 12s, zero
	// config records, every new tile black while viewers from before the
	// reconnect kept playing. Keeping the last one costs nothing when the
	// vendor does resend (it simply overwrites) and is the difference
	// between a picture and a black tile when it doesn't.
	c.gop = nil

	// Audio is the exception: whether the header advertises audio is
	// decided per connection by the probe, so a stale AAC header would
	// contradict a header that just said this camera has no microphone.
	if len(base) >= 5 && base[4]&0x04 == 0 {
		c.audioSeq = nil
	}
	c.mu.Unlock()
	c.markReady()
}

func (c *flvChannel) markReady() {
	c.readyOnce.Do(func() { close(c.ready) })
}

// flvKeepAliveTag is an FLV script-data tag carrying "onKeepAlive". Players
// look for onMetaData and ignore script tags they don't recognise, so this
// keeps the connection (and any proxy in between) alive during a vendor
// outage without disturbing playback.
var flvKeepAliveTag = buildKeepAliveTag()

func buildKeepAliveTag() []byte {
	const name = "onKeepAlive"
	data := make([]byte, 0, 4+len(name))
	data = append(data, 0x02, byte(len(name)>>8), byte(len(name))) // AMF0 string
	data = append(data, name...)
	data = append(data, 0x05) // AMF0 null

	tag := make([]byte, 0, 11+len(data)+4)
	tag = append(tag, 0x12,
		byte(len(data)>>16), byte(len(data)>>8), byte(len(data)),
		0, 0, 0, 0, // timestamp + extended
		0, 0, 0) // stream id
	tag = append(tag, data...)
	total := 11 + len(data)
	tag = append(tag, byte(total>>24), byte(total>>16), byte(total>>8), byte(total))
	return tag
}

// hubChannelStat is one channel's line in GET /api/hub.
type hubChannelStat struct {
	Key         string `json:"key"`
	Viewers     int    `json:"viewers"`
	Live        bool   `json:"live"`
	UptimeSec   int    `json:"uptimeSec"`
	Reconnects  int    `json:"reconnects"`
	DroppedTags int64  `json:"droppedTags"`
	BytesOut    int64  `json:"bytesOut"`
	LastError   string `json:"lastError,omitempty"`
}

// stats reports what every live channel is doing. Without this the only way
// to answer "is camera X actually streaming, and to how many people" is to
// read logs — which is how a whole day got spent finding that viewers were
// each opening their own vendor session.
func (h *flvHub) stats() []hubChannelStat {
	h.mu.Lock()
	chans := make([]*flvChannel, 0, len(h.chans))
	for _, c := range h.chans {
		chans = append(chans, c)
	}
	h.mu.Unlock()

	out := make([]hubChannelStat, 0, len(chans))
	for _, c := range chans {
		c.mu.RLock()
		stat := hubChannelStat{
			Key:         c.key,
			Viewers:     len(c.subs),
			Live:        c.baseHdr != nil,
			UptimeSec:   int(time.Since(c.startedAt).Seconds()),
			Reconnects:  c.reconnects,
			DroppedTags: c.droppedTags,
			BytesOut:    c.bytesOut,
		}
		if c.lastErr != nil {
			stat.LastError = c.lastErr.Error()
		}
		c.mu.RUnlock()
		out = append(out, stat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
