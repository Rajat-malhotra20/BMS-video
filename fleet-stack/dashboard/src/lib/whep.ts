// Minimal WHEP (WebRTC-HTTP Egress Protocol) client for MediaMTX.
export interface WhepSession {
  pc: RTCPeerConnection
  close: () => void
}

export async function startWhep(
  path: string,
  video: HTMLVideoElement,
  signal: AbortSignal,
): Promise<WhepSession> {
  const pc = new RTCPeerConnection()
  pc.addTransceiver('video', { direction: 'recvonly' })
  pc.addTransceiver('audio', { direction: 'recvonly' })

  pc.ontrack = ev => {
    if (video.srcObject !== ev.streams[0]) {
      video.srcObject = ev.streams[0]
    }
  }

  const offer = await pc.createOffer()
  await pc.setLocalDescription(offer)

  // Wait for ICE gathering (MediaMTX expects a complete offer).
  await new Promise<void>(resolve => {
    if (pc.iceGatheringState === 'complete') return resolve()
    const check = () => {
      if (pc.iceGatheringState === 'complete') {
        pc.removeEventListener('icegatheringstatechange', check)
        resolve()
      }
    }
    pc.addEventListener('icegatheringstatechange', check)
    setTimeout(resolve, 1000) // cap gathering wait
  })

  const res = await fetch(`/whep/${path}/whep`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/sdp' },
    body: pc.localDescription!.sdp,
    signal,
  })
  if (!res.ok) {
    pc.close()
    throw new Error(`WHEP ${path}: HTTP ${res.status}`)
  }
  await pc.setRemoteDescription({ type: 'answer', sdp: await res.text() })

  return {
    pc,
    close: () => {
      video.srcObject = null
      pc.close()
    },
  }
}
