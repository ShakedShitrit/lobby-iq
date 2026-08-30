// Command mkico builds a multi-size Windows .ico from a square PNG.
//
// It exists because the icon has to carry several sizes: Windows picks one per
// context, and a single large image scaled down by the shell looks noticeably
// worse in a taskbar than one rendered at the right size. Keeping the
// generator in the repo means the icon can be rebuilt from the source art
// rather than being a binary nobody can reproduce.
//
// Usage:
//
//	go run ./tools/mkico -in assets/brand/emblem.png -out assets/brand/lobbyiq.ico
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"log"
	"os"

	xdraw "golang.org/x/image/draw"
)

// sizes are the icon sizes Windows asks for: 16 and 32 in lists and the
// taskbar, 48 in Explorer's medium view, 256 for the large views and for
// installers.
var sizes = []int{16, 24, 32, 48, 64, 128, 256}

// pngThreshold is the size at or above which entries are stored as PNG rather
// than as a device-independent bitmap.
//
// Windows has read PNG-compressed entries since Vista, but only for the large
// ones is it worth it: a 256x256 BGRA bitmap is 256 KB where the PNG is a few,
// while at 16x16 the saving is nothing and some shell surfaces are still
// happier with a bitmap.
const pngThreshold = 128

func main() {
	in := flag.String("in", "", "source PNG (square)")
	out := flag.String("out", "", "destination .ico")
	flag.Parse()

	if *in == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*in, *out); err != nil {
		log.Fatal(err)
	}
}

func run(in, out string) error {
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()

	src, err := png.Decode(f)
	if err != nil {
		return fmt.Errorf("decoding %s: %w", in, err)
	}
	b := src.Bounds()
	if b.Dx() != b.Dy() {
		return fmt.Errorf("%s is %dx%d; an icon source must be square", in, b.Dx(), b.Dy())
	}

	var (
		entries []entry
		blobs   [][]byte
	)
	for _, size := range sizes {
		img := scale(src, size)

		var data []byte
		if size >= pngThreshold {
			data, err = encodePNG(img)
		} else {
			data, err = encodeDIB(img)
		}
		if err != nil {
			return fmt.Errorf("encoding %dpx: %w", size, err)
		}
		entries = append(entries, entry{size: size, length: len(data)})
		blobs = append(blobs, data)
	}

	ico := assemble(entries, blobs)
	if err := os.WriteFile(out, ico, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d sizes, %d bytes)\n", out, len(sizes), len(ico))
	return nil
}

type entry struct {
	size   int
	length int
}

// assemble writes the ICONDIR, one ICONDIRENTRY per image, then the images.
func assemble(entries []entry, blobs [][]byte) []byte {
	const dirSize = 6
	const entrySize = 16

	buf := &bytes.Buffer{}
	// ICONDIR: reserved, type 1 (icon), count.
	binary.Write(buf, binary.LittleEndian, uint16(0))
	binary.Write(buf, binary.LittleEndian, uint16(1))
	binary.Write(buf, binary.LittleEndian, uint16(len(entries)))

	offset := dirSize + entrySize*len(entries)
	for _, e := range entries {
		// 256 is stored as 0: the field is a single byte, so 256 does not fit
		// and zero is the format's agreed way of saying it.
		dim := byte(e.size)
		if e.size == 256 {
			dim = 0
		}
		buf.WriteByte(dim)                                       // width
		buf.WriteByte(dim)                                       // height
		buf.WriteByte(0)                                         // palette size, 0 for truecolour
		buf.WriteByte(0)                                         // reserved
		binary.Write(buf, binary.LittleEndian, uint16(1))        // colour planes
		binary.Write(buf, binary.LittleEndian, uint16(32))       // bits per pixel
		binary.Write(buf, binary.LittleEndian, uint32(e.length)) // bytes of data
		binary.Write(buf, binary.LittleEndian, uint32(offset))   // offset to data
		offset += e.length
	}
	for _, b := range blobs {
		buf.Write(b)
	}
	return buf.Bytes()
}

// scale resamples to size x size.
func scale(src image.Image, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	// CatmullRom rather than the cheaper kernels: this runs once, offline, and
	// the difference is visible at 16px where every pixel counts.
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

func encodePNG(img image.Image) ([]byte, error) {
	buf := &bytes.Buffer{}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeDIB writes the BITMAPINFOHEADER form an icon entry uses: a 32-bit
// bottom-up BGRA image, followed by a 1-bit AND mask.
//
// The mask is redundant when the pixels carry alpha, but the format still
// requires it and some Windows surfaces still consult it. It is written as all
// zero - "every pixel opaque" - and the alpha channel does the real work.
func encodeDIB(img *image.NRGBA) ([]byte, error) {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()

	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, img.Bounds(), img, img.Bounds().Min, draw.Src)

	buf := &bytes.Buffer{}

	// BITMAPINFOHEADER. The declared height is doubled: the structure counts
	// the colour image and the mask as one bitmap.
	binary.Write(buf, binary.LittleEndian, uint32(40))    // header size
	binary.Write(buf, binary.LittleEndian, int32(w))      // width
	binary.Write(buf, binary.LittleEndian, int32(h*2))    // height, image + mask
	binary.Write(buf, binary.LittleEndian, uint16(1))     // planes
	binary.Write(buf, binary.LittleEndian, uint16(32))    // bits per pixel
	binary.Write(buf, binary.LittleEndian, uint32(0))     // BI_RGB, no compression
	binary.Write(buf, binary.LittleEndian, uint32(w*h*4)) // image size
	binary.Write(buf, binary.LittleEndian, int32(0))      // x pixels per metre
	binary.Write(buf, binary.LittleEndian, int32(0))      // y pixels per metre
	binary.Write(buf, binary.LittleEndian, uint32(0))     // palette entries used
	binary.Write(buf, binary.LittleEndian, uint32(0))     // important colours

	// Colour data, bottom row first, BGRA per pixel.
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			i := rgba.PixOffset(x, y)
			buf.WriteByte(rgba.Pix[i+2]) // B
			buf.WriteByte(rgba.Pix[i+1]) // G
			buf.WriteByte(rgba.Pix[i+0]) // R
			buf.WriteByte(rgba.Pix[i+3]) // A
		}
	}

	// AND mask: one bit per pixel, each row padded to a 4-byte boundary.
	maskRow := ((w + 31) / 32) * 4
	buf.Write(make([]byte, maskRow*h))

	return buf.Bytes(), nil
}
