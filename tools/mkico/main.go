// mkico converts the Guardian Android launcher icon (WebP) into a Windows .ico
// with the usual set of sizes.
//
// Small sizes are written as BMP/DIB entries and 256x256 as PNG: that is the
// layout Windows has always understood, whereas PNG entries at small sizes are
// only reliable on newer shells.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

var sizes = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: mkico <source image> <out.ico>")
		os.Exit(2)
	}
	src, dst := os.Args[1], os.Args[2]

	f, err := os.Open(src)
	check(err)
	defer f.Close()

	img, format, err := image.Decode(f)
	check(err)

	b := img.Bounds()
	fmt.Printf("source: %s  format=%s  %dx%d\n", src, format, b.Dx(), b.Dy())

	square := toSquareRGBA(img)

	var entries [][]byte
	for _, s := range sizes {
		scaled := scaleTo(square, s)
		if s >= 256 {
			entries = append(entries, encodePNG(scaled))
		} else {
			entries = append(entries, encodeDIB(scaled))
		}
	}

	out, err := os.Create(dst)
	check(err)
	defer out.Close()

	check(writeICO(out, sizes, entries))

	info, err := os.Stat(dst)
	check(err)
	fmt.Printf("wrote:  %s  %d bytes  sizes=%v\n", dst, info.Size(), sizes)
}

// toSquareRGBA centres the image on a transparent square canvas, so a
// non-square source is not distorted by scaling.
func toSquareRGBA(img image.Image) *image.RGBA {
	b := img.Bounds()
	side := b.Dx()
	if b.Dy() > side {
		side = b.Dy()
	}

	canvas := image.NewRGBA(image.Rect(0, 0, side, side))
	offset := image.Pt((side-b.Dx())/2, (side-b.Dy())/2)
	draw.Draw(canvas, image.Rectangle{Min: offset, Max: offset.Add(b.Size())}, img, b.Min, draw.Src)
	return canvas
}

func scaleTo(src *image.RGBA, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

func encodePNG(img *image.RGBA) []byte {
	var buf bytes.Buffer
	check(png.Encode(&buf, img))
	return buf.Bytes()
}

// encodeDIB writes a BITMAPINFOHEADER icon image: a 32-bit bottom-up BGRA
// bitmap of double the declared height, followed by the 1-bit AND mask.
func encodeDIB(img *image.RGBA) []byte {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()

	var buf bytes.Buffer
	hdr := struct {
		Size          uint32
		Width         int32
		Height        int32
		Planes        uint16
		BitCount      uint16
		Compression   uint32
		SizeImage     uint32
		XPelsPerMeter int32
		YPelsPerMeter int32
		ClrUsed       uint32
		ClrImportant  uint32
	}{
		Size:     40,
		Width:    int32(w),
		Height:   int32(h * 2), // XOR bitmap plus AND mask
		Planes:   1,
		BitCount: 32,
	}
	check(binary.Write(&buf, binary.LittleEndian, hdr))

	// Pixel rows run bottom-up, in BGRA order.
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			buf.WriteByte(img.Pix[i+2]) // B
			buf.WriteByte(img.Pix[i+1]) // G
			buf.WriteByte(img.Pix[i+0]) // R
			buf.WriteByte(img.Pix[i+3]) // A
		}
	}

	// The AND mask is redundant with the alpha channel, but the format requires
	// it. All zeroes means "use the alpha".
	maskRow := ((w + 31) / 32) * 4
	buf.Write(make([]byte, maskRow*h))

	return buf.Bytes()
}

func writeICO(out *os.File, sizes []int, entries [][]byte) error {
	var buf bytes.Buffer

	// ICONDIR
	check(binary.Write(&buf, binary.LittleEndian, uint16(0))) // reserved
	check(binary.Write(&buf, binary.LittleEndian, uint16(1))) // type: icon
	check(binary.Write(&buf, binary.LittleEndian, uint16(len(entries))))

	offset := 6 + 16*len(entries)
	for i, data := range entries {
		s := sizes[i]
		dim := byte(s)
		if s >= 256 {
			dim = 0 // 0 means 256 in the ICO header
		}

		buf.WriteByte(dim)                                                // width
		buf.WriteByte(dim)                                                // height
		buf.WriteByte(0)                                                  // palette size
		buf.WriteByte(0)                                                  // reserved
		check(binary.Write(&buf, binary.LittleEndian, uint16(1)))         // planes
		check(binary.Write(&buf, binary.LittleEndian, uint16(32)))        // bpp
		check(binary.Write(&buf, binary.LittleEndian, uint32(len(data)))) // bytes
		check(binary.Write(&buf, binary.LittleEndian, uint32(offset)))    // offset
		offset += len(data)
	}

	for _, data := range entries {
		buf.Write(data)
	}

	_, err := out.Write(buf.Bytes())
	return err
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
