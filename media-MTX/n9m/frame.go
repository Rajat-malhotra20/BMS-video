// Package n9m implements the Chemito ITS "N9M" protocol: a fixed 12-byte
// binary header (version/flags/CSRC-count/payload-type/SSRC/length/reserve),
// optional CSRC address list, and a payload that is either JSON command data
// (PayloadType == PayloadCommand) or opaque media bytes (audio/video/etc, see
// PayloadType constants).
//
// This package only implements framing + JSON command exchange. Media
// payload bytes (PayloadLiveVideo, PayloadRecording, ...) are handed back to
// the caller as opaque []byte for forwarding into a media pipeline (e.g.
// muxed into RTSP/MediaMTX); their internal codec framing is
// vendor/device-specific and out of scope here.
package n9m

import (
	"encoding/binary"
	"fmt"
	"io"
)

// PayloadType identifies what kind of data follows the header, per the N9M
// "Payload_Type_Table".
type PayloadType uint8

const (
	PayloadCommand      PayloadType = 0  // JSON command/status/control data
	PayloadLiveVideo    PayloadType = 2  // real-time audio/video
	PayloadRecordingDL  PayloadType = 3  // recording data for file download
	PayloadPlayback     PayloadType = 4  // video playback data
	PayloadSnapshot     PayloadType = 6  // captured photo
	PayloadParamImport  PayloadType = 10 // parameter import
	PayloadParamExport  PayloadType = 11 // parameter export
	PayloadSubStream    PayloadType = 15 // transmission sub-stream
	PayloadSubStreamRec PayloadType = 16 // recording sub-stream
	PayloadBlackBox     PayloadType = 17 // black box data
	PayloadSpecial      PayloadType = 22 // special command (SSRC-keyed, e.g. heartbeat)
	PayloadStreamaxMnt  PayloadType = 30 // streamax maintenance data
)

// Special-command SSRC values, valid only when PayloadType == PayloadSpecial.
const (
	SpecialHeartbeat         uint16 = 0 // device -> server heartbeat (replaces CERTIFICATE/KEEPALIVE)
	SpecialHeartbeatResponse uint16 = 1 // server -> device heartbeat ack
	SpecialGPSUpload         uint16 = 2 // GPS data upload
)

const headerSize = 12

// Header is the fixed 12-byte N9M frame header, plus any CSRC entries that
// follow it (CSRCCount * 4 bytes, before the payload).
type Header struct {
	Version     uint8 // protocol version, currently 1
	Encrypted   bool
	Compressed  bool
	CSRCCount   uint8 // 0-15
	PayloadType PayloadType
	SSRC        uint16
	PayloadLen  uint32 // length of the payload that follows the CSRC list
	Reserve     uint32
	CSRC        []uint32 // source/target addresses, len == CSRCCount
}

// Encode serializes the header (and any CSRC entries) to w.
func (h Header) Encode(w io.Writer) error {
	if h.CSRCCount > 15 {
		return fmt.Errorf("n9m: CSRCCount %d exceeds max of 15", h.CSRCCount)
	}
	if int(h.CSRCCount) != len(h.CSRC) {
		return fmt.Errorf("n9m: CSRCCount %d does not match %d CSRC entries", h.CSRCCount, len(h.CSRC))
	}

	var first uint32
	first |= uint32(h.Version&0x3) << 30
	if h.Encrypted {
		first |= 1 << 29
	}
	if h.Compressed {
		first |= 1 << 28
	}
	first |= uint32(h.CSRCCount&0xF) << 24
	first |= uint32(h.PayloadType) << 16
	first |= uint32(h.SSRC)

	buf := make([]byte, headerSize)
	binary.BigEndian.PutUint32(buf[0:4], first)
	binary.BigEndian.PutUint32(buf[4:8], h.PayloadLen)
	binary.BigEndian.PutUint32(buf[8:12], h.Reserve)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("n9m: write header: %w", err)
	}

	for _, c := range h.CSRC {
		var cb [4]byte
		binary.BigEndian.PutUint32(cb[:], c)
		if _, err := w.Write(cb[:]); err != nil {
			return fmt.Errorf("n9m: write csrc: %w", err)
		}
	}
	return nil
}

// DecodeHeader reads a fixed header plus its CSRC entries from r.
func DecodeHeader(r io.Reader) (Header, error) {
	var buf [headerSize]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return Header{}, err // may be io.EOF; caller should treat that as "connection closed"
	}

	first := binary.BigEndian.Uint32(buf[0:4])
	h := Header{
		Version:     uint8(first >> 30 & 0x3),
		Encrypted:   first&(1<<29) != 0,
		Compressed:  first&(1<<28) != 0,
		CSRCCount:   uint8(first >> 24 & 0xF),
		PayloadType: PayloadType(first >> 16 & 0xFF),
		SSRC:        uint16(first & 0xFFFF),
		PayloadLen:  binary.BigEndian.Uint32(buf[4:8]),
		Reserve:     binary.BigEndian.Uint32(buf[8:12]),
	}

	if h.CSRCCount > 0 {
		h.CSRC = make([]uint32, h.CSRCCount)
		csrcBuf := make([]byte, int(h.CSRCCount)*4)
		if _, err := io.ReadFull(r, csrcBuf); err != nil {
			return Header{}, fmt.Errorf("n9m: read csrc: %w", err)
		}
		for i := range h.CSRC {
			h.CSRC[i] = binary.BigEndian.Uint32(csrcBuf[i*4 : i*4+4])
		}
	}
	return h, nil
}

// Frame is a fully decoded header + payload.
type Frame struct {
	Header  Header
	Payload []byte
}

// ReadFrame reads one complete frame (header, CSRC list, payload) from r.
func ReadFrame(r io.Reader) (Frame, error) {
	h, err := DecodeHeader(r)
	if err != nil {
		return Frame{}, err
	}
	payload := make([]byte, h.PayloadLen)
	if h.PayloadLen > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, fmt.Errorf("n9m: read payload: %w", err)
		}
	}
	return Frame{Header: h, Payload: payload}, nil
}

// WriteFrame writes header + payload to w, filling in PayloadLen/CSRCCount
// from the payload/CSRC slice so callers don't have to keep them in sync.
func WriteFrame(w io.Writer, h Header, payload []byte) error {
	h.PayloadLen = uint32(len(payload))
	h.CSRCCount = uint8(len(h.CSRC))
	if err := h.Encode(w); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("n9m: write payload: %w", err)
	}
	return nil
}
