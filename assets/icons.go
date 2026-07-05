package assets

import "encoding/binary"

// Tray icons in Windows ICO format (BMP-in-ICO, 32×32, 32-bit BGRA).
// systray on Windows writes icon bytes to a temp file and loads it with
// LoadImage(LR_LOADFROMFILE), which requires the ICO container format.
var (
	IconGrey  = makeICO(128, 128, 128)
	IconGreen = makeICO(0, 200, 0)
	IconRed   = makeICO(200, 0, 0)
)

// makeICO produces a minimal single-image 32×32 ICO file with a solid color.
func makeICO(r, g, b byte) []byte {
	const (
		w, h        = 32, 32
		rowStride   = w * 4                       // bytes per pixel row (BGRA)
		pixDataSize = h * rowStride               // XOR (color) data
		maskStride  = (w + 31) / 32 * 4           // AND mask row, DWORD-aligned
		maskSize    = h * maskStride              // AND mask (all 0 = opaque)
		bmpSize     = 40 + pixDataSize + maskSize // BITMAPINFOHEADER + data
		dataOffset  = 6 + 16                      // after ICONDIR + ICONDIRENTRY
	)

	buf := make([]byte, dataOffset+bmpSize)

	// ICONDIR (6 bytes)
	binary.LittleEndian.PutUint16(buf[0:], 0) // reserved
	binary.LittleEndian.PutUint16(buf[2:], 1) // type = ICO
	binary.LittleEndian.PutUint16(buf[4:], 1) // image count

	// ICONDIRENTRY (16 bytes at offset 6)
	e := buf[6:]
	e[0] = w                                 // width
	e[1] = h                                 // height
	e[2] = 0                                 // color count (0 = true color)
	e[3] = 0                                 // reserved
	binary.LittleEndian.PutUint16(e[4:], 1)  // planes
	binary.LittleEndian.PutUint16(e[6:], 32) // bits per pixel
	binary.LittleEndian.PutUint32(e[8:], uint32(bmpSize))
	binary.LittleEndian.PutUint32(e[12:], uint32(dataOffset))

	// BITMAPINFOHEADER (40 bytes at dataOffset)
	bh := buf[dataOffset:]
	binary.LittleEndian.PutUint32(bh[0:], 40)  // biSize
	binary.LittleEndian.PutUint32(bh[4:], w)   // biWidth
	binary.LittleEndian.PutUint32(bh[8:], h*2) // biHeight = 2×h (includes mask)
	binary.LittleEndian.PutUint16(bh[12:], 1)  // biPlanes
	binary.LittleEndian.PutUint16(bh[14:], 32) // biBitCount
	binary.LittleEndian.PutUint32(bh[16:], 0)  // biCompression = BI_RGB
	binary.LittleEndian.PutUint32(bh[20:], uint32(pixDataSize))

	// XOR mask (BGRA pixel data, bottom-up)
	pix := bh[40:]
	for row := h - 1; row >= 0; row-- {
		for col := 0; col < w; col++ {
			off := row*rowStride + col*4
			pix[off+0] = b   // blue
			pix[off+1] = g   // green
			pix[off+2] = r   // red
			pix[off+3] = 255 // alpha (fully opaque)
		}
	}
	// AND mask stays all zero (= fully opaque)

	return buf
}
