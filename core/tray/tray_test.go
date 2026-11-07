package tray

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/samhudson/signals/core/engine"
)

func TestUrgentAndSummary(t *testing.T) {
	s := engine.State{Counts: map[string]int{"done": 2, "working": 1, "waiting": 1}}
	if Urgent(s) != "waiting" {
		t.Errorf("needs-you comes first, got %s", Urgent(s))
	}
	if got := Summary(s); got != "1 needs you · 1 working · 2 done" {
		t.Errorf("summary: %q", got)
	}
	if Urgent(engine.State{}) != "" || Summary(engine.State{}) != "" {
		t.Errorf("nothing running: no status, no summary")
	}
}

func TestIcons(t *testing.T) {
	for _, st := range []string{"waiting", "working", "done", "error", ""} {
		img, err := png.Decode(bytes.NewReader(IconPNG(st, 32)))
		if err != nil || img.Bounds().Dx() != 32 {
			t.Fatalf("%q: a 32px PNG: %v", st, err)
		}
	}
	center := func(st string) [3]uint32 {
		img, _ := png.Decode(bytes.NewReader(IconPNG(st, 32)))
		r, g, b, _ := img.At(16, 16).RGBA()
		return [3]uint32{r >> 8, g >> 8, b >> 8}
	}
	if c := center("waiting"); c[0] < 200 || c[1] < 150 || c[2] > 60 {
		t.Errorf("needs you is amber: %v", c)
	}
	if c := center("working"); c[0] < 200 || c[1] > 120 {
		t.Errorf("working is red: %v", c)
	}
	ico := ICO(IconPNG("done", 32), 32)
	if ico[2] != 1 || ico[4] != 1 || !bytes.Equal(ico[22:30], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		t.Errorf("a valid one-image .ico holding the PNG")
	}
}
