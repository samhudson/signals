// Package tray is Signals' tray icon for Windows and Linux: a dot showing the
// most urgent session, and a menu listing every session.
package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"

	"github.com/samhudson/signals/core/engine"
)

var (
	red   = color.RGBA{255, 69, 58, 255}
	amber = color.RGBA{255, 199, 0, 255}
	green = color.RGBA{50, 205, 74, 255}
	gray  = color.RGBA{140, 140, 140, 255}
)

// Urgent is the status the icon shows: needs you, then working, then error, then done.
func Urgent(s engine.State) string {
	for _, st := range []string{"waiting", "working", "error", "done"} {
		if s.Counts[st] > 0 {
			return st
		}
	}
	return ""
}

// IconPNG draws the tray icon: a filled dot in the urgent status's colour, or a
// grey ring when nothing is running.
func IconPNG(status string, size int) []byte {
	c, ring := gray, status == ""
	switch status {
	case "waiting":
		c = amber
	case "working":
		c = red
	case "done":
		c = green
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	r := float64(size)/2 - 1
	cx := float64(size) / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cx
			d := dx*dx + dy*dy
			if d <= r*r && (!ring || d >= (r-2.5)*(r-2.5)) {
				img.Set(x, y, c)
			}
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// ICO wraps a PNG in an .ico container, which the Windows tray needs.
func ICO(pngData []byte, size int) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, []uint16{0, 1, 1}) // reserved, icon type, image count
	dim := byte(size)
	if size >= 256 {
		dim = 0
	}
	b.Write([]byte{dim, dim, 0, 0})
	binary.Write(&b, binary.LittleEndian, []uint16{1, 32})
	binary.Write(&b, binary.LittleEndian, []uint32{uint32(len(pngData)), 22})
	b.Write(pngData)
	return b.Bytes()
}
