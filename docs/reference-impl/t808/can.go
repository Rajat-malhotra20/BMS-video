package t808

const MsgIDCANUpload = 0x0705

// CANItem is one CAN bus data item (Table 2): a 4-byte CAN ID plus 8 bytes
// of frame data.
type CANItem struct {
	ID   uint32
	Data [8]byte
}

// CANUpload is message 0x0705 (OBU -> Server) (Table 1).
type CANUpload struct {
	ReceivingTime BCDTime5
	Items         []CANItem
}

func BuildCANUpload(m CANUpload) ([]byte, error) {
	var w byteWriter
	w.u16(uint16(len(m.Items)))
	tb, err := encodeBCDTime5(m.ReceivingTime)
	if err != nil {
		return nil, err
	}
	w.raw(tb[:])
	for _, item := range m.Items {
		w.u32(item.ID)
		w.raw(item.Data[:])
	}
	return w.bytesOut()
}

func DecodeCANUpload(body []byte) (CANUpload, error) {
	r := newByteReader(body)
	count := int(r.u16())
	var m CANUpload
	var tb [5]byte
	copy(tb[:], r.bytesN(5))
	m.ReceivingTime = decodeBCDTime5(tb)
	for i := 0; i < count && r.err == nil; i++ {
		item := CANItem{ID: r.u32()}
		copy(item.Data[:], r.bytesN(8))
		m.Items = append(m.Items, item)
	}
	return m, r.err
}
