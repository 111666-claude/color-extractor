package color_extractor

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func solidImage(c color.RGBA, width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// halvesImage returns an image whose left half is `left` and right half is `right`.
func halvesImage(left, right color.RGBA, width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			if x < width/2 {
				img.Set(x, y, left)
			} else {
				img.Set(x, y, right)
			}
		}
	}
	return img
}

func colorDistance(a, b color.Color) float64 {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	return math.Sqrt(
		math.Pow(float64(ar>>8)-float64(br>>8), 2) +
			math.Pow(float64(ag>>8)-float64(bg>>8), 2) +
			math.Pow(float64(ab>>8)-float64(bb>>8), 2))
}

func findByID(palette []PaletteColor, id int) (PaletteColor, bool) {
	for _, p := range palette {
		if p.ID == id {
			return p, true
		}
	}
	return PaletteColor{}, false
}

// A slowly drifting blue must keep a single stable ID and move smoothly.
func TestSequenceGradient(t *testing.T) {
	extractor := NewSequenceExtractor(DefaultSequenceConfig())

	var previous *PaletteColor
	for frame := 0; frame < 40; frame++ {
		blue := uint8(100 + frame*2) // 100 .. 178
		palette := extractor.NextFrame(solidImage(color.RGBA{B: blue, A: 255}, 32, 32))

		if len(palette) != 1 {
			t.Fatalf("frame %d: expected 1 color, got %v", frame, palette)
		}
		current := palette[0]
		if previous != nil {
			if current.ID != previous.ID {
				t.Fatalf("frame %d: ID jumped from %d to %d", frame, previous.ID, current.ID)
			}
			if d := colorDistance(current.Color, previous.Color); d > 8 {
				t.Fatalf("frame %d: color jumped by %v between frames", frame, d)
			}
		}
		c := current
		previous = &c
	}

	// The smoothed color must have followed the gradient.
	final := color.RGBA{B: 178, A: 255}
	if d := colorDistance(previous.Color, final); d > 20 {
		t.Fatalf("final color %v too far from gradient end %v", previous.Color, final)
	}
}

// A color hidden for a few frames must come back with the same ID.
func TestSequenceOcclusion(t *testing.T) {
	config := DefaultSequenceConfig()
	config.MaxMissing = 5
	extractor := NewSequenceExtractor(config)

	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}

	greenID := -1
	for frame := 0; frame < 10; frame++ {
		palette := extractor.NextFrame(halvesImage(red, green, 32, 32))
		if frame == 9 {
			for _, p := range palette {
				if distanceTo(p.Color, green) < distanceTo(p.Color, red) {
					greenID = p.ID
				}
			}
		}
	}
	if greenID < 0 {
		t.Fatal("green not found before occlusion")
	}

	// Green is occluded for 3 frames (fewer than MaxMissing).
	for frame := 0; frame < 3; frame++ {
		palette := extractor.NextFrame(solidImage(red, 32, 32))
		if _, ok := findByID(palette, greenID); !ok {
			t.Fatalf("occlusion frame %d: green ID %d lost too early", frame, greenID)
		}
	}

	// Green returns and must keep its ID.
	palette := extractor.NextFrame(halvesImage(red, green, 32, 32))
	if _, ok := findByID(palette, greenID); !ok {
		t.Fatalf("green ID %d not preserved after occlusion, got %v", greenID, palette)
	}
}

// A color missing for longer than MaxMissing must be retired.
func TestSequenceOcclusionExpiry(t *testing.T) {
	config := DefaultSequenceConfig()
	config.MaxMissing = 2
	extractor := NewSequenceExtractor(config)

	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}

	before := extractor.NextFrame(halvesImage(red, green, 32, 32))
	if len(before) != 2 {
		t.Fatalf("expected 2 colors, got %v", before)
	}

	for frame := 0; frame < 5; frame++ {
		extractor.NextFrame(solidImage(red, 32, 32))
	}
	after := extractor.NextFrame(halvesImage(red, green, 32, 32))

	greenID := func(palette []PaletteColor) int {
		for _, p := range palette {
			if distanceTo(p.Color, green) < distanceTo(p.Color, red) {
				return p.ID
			}
		}
		return -1
	}
	oldID, newID := greenID(before), greenID(after)
	if oldID < 0 || newID < 0 {
		t.Fatalf("green missing: before %v after %v", before, after)
	}
	if oldID == newID {
		t.Fatalf("expected retired ID %d to be replaced, still present", oldID)
	}
}

// A hard cut must replace the palette deterministically: old IDs retire,
// new IDs appear, and the extractor settles on the new scene.
func TestSequenceHardCut(t *testing.T) {
	config := DefaultSequenceConfig()
	config.MaxMissing = 2
	extractor := NewSequenceExtractor(config)

	sceneA := func(frame int) image.Image {
		return halvesImage(color.RGBA{B: 255, A: 255}, color.RGBA{R: 255, G: 255, A: 255}, 32, 32)
	}
	sceneB := func(frame int) image.Image {
		return halvesImage(color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}, 32, 32)
	}

	idsA := map[int]bool{}
	for frame := 0; frame < 10; frame++ {
		for _, p := range extractor.NextFrame(sceneA(frame)) {
			idsA[p.ID] = true
		}
	}

	var settled []PaletteColor
	for frame := 0; frame < 10; frame++ {
		settled = extractor.NextFrame(sceneB(frame))
	}

	if len(settled) != 2 {
		t.Fatalf("expected 2 colors after cut, got %v", settled)
	}
	for _, p := range settled {
		if idsA[p.ID] {
			t.Fatalf("stale ID %d from scene A survived the cut: %v", p.ID, settled)
		}
	}
	if distanceTo(settled[0].Color, color.RGBA{R: 255, A: 255}) > 40 &&
		distanceTo(settled[0].Color, color.RGBA{G: 255, A: 255}) > 40 {
		t.Fatalf("dominant color %v does not match scene B", settled[0].Color)
	}
}

// Empty and transparent frames must not panic, must not create colors, and
// must keep existing slots until they age out.
func TestSequenceEmptyAndTransparentFrames(t *testing.T) {
	config := DefaultSequenceConfig()
	config.MaxMissing = 2
	extractor := NewSequenceExtractor(config)

	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	transparent := solidImage(color.RGBA{A: 0}, 32, 32)

	if palette := extractor.NextFrame(empty); len(palette) != 0 {
		t.Fatalf("empty first frame: expected no colors, got %v", palette)
	}
	if palette := extractor.NextFrame(transparent); len(palette) != 0 {
		t.Fatalf("transparent frame: expected no colors, got %v", palette)
	}

	red := color.RGBA{R: 255, A: 255}
	palette := extractor.NextFrame(solidImage(red, 32, 32))
	if len(palette) != 1 {
		t.Fatalf("expected 1 color, got %v", palette)
	}
	redID := palette[0].ID

	// A gap of empty frames keeps the slot alive.
	for frame := 0; frame < 2; frame++ {
		palette = extractor.NextFrame(empty)
		if _, ok := findByID(palette, redID); !ok {
			t.Fatalf("empty frame %d: red ID %d lost too early", frame, redID)
		}
	}
	// Once aged out, the slot is gone.
	for frame := 0; frame < 3; frame++ {
		palette = extractor.NextFrame(transparent)
	}
	if len(palette) != 0 {
		t.Fatalf("expected empty palette after ageing out, got %v", palette)
	}
}

// Frames with varying numbers of colors must stay within the palette size.
func TestSequenceVaryingColorCount(t *testing.T) {
	config := DefaultSequenceConfig()
	config.ColorCount = 3
	config.MaxMissing = 0
	extractor := NewSequenceExtractor(config)

	frames := []image.Image{
		solidImage(color.RGBA{R: 255, A: 255}, 32, 32),                                             // 1 color
		halvesImage(color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}, 32, 32),                // 2 colors
		quartersImage(32, 32),                                                                      // 4 colors
		solidImage(color.RGBA{B: 255, A: 255}, 32, 32),                                             // 1 color
	}
	for frame, img := range frames {
		if palette := extractor.NextFrame(img); len(palette) > config.ColorCount {
			t.Fatalf("frame %d: palette %v exceeds ColorCount %d", frame, palette, config.ColorCount)
		}
	}
}

// Long sequences must keep bounded state and deterministic output.
func TestSequenceLongRunBoundedAndDeterministic(t *testing.T) {
	const frames = 20000

	run := func() []PaletteColor {
		config := DefaultSequenceConfig()
		config.ColorCount = 4
		extractor := NewSequenceExtractor(config)
		// Deterministic pseudo-random colors (LCG), including hard jumps.
		state := uint32(12345)
		next := func() uint8 {
			state = state*1664525 + 1013904223
			return uint8(state >> 24)
		}
		var last []PaletteColor
		for frame := 0; frame < frames; frame++ {
			c := color.RGBA{R: next(), G: next(), B: next(), A: 255}
			last = extractor.NextFrame(solidImage(c, 8, 8))
			if len(last) > config.ColorCount {
				t.Fatalf("frame %d: state grew beyond %d slots", frame, config.ColorCount)
			}
		}
		return last
	}

	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("non-deterministic palette sizes: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("non-deterministic result at %d: %v vs %v", i, first[i], second[i])
		}
	}
	if len(first) == 0 {
		t.Fatal("expected a non-empty palette at the end of the run")
	}
}

// Smoothing must reduce frame-to-frame oscillation compared to no smoothing.
func TestSequenceSmoothingEffect(t *testing.T) {
	jittery := func(frame int) image.Image {
		blue := uint8(120)
		if frame%2 == 1 {
			blue = 160
		}
		return solidImage(color.RGBA{B: blue, A: 255}, 32, 32)
	}

	totalJump := func(smoothing float64) float64 {
		config := DefaultSequenceConfig()
		config.Smoothing = smoothing
		extractor := NewSequenceExtractor(config)
		total := 0.
		var previous *PaletteColor
		for frame := 0; frame < 20; frame++ {
			palette := extractor.NextFrame(jittery(frame))
			if len(palette) != 1 {
				t.Fatalf("smoothing %v frame %d: expected 1 color, got %v", smoothing, frame, palette)
			}
			if previous != nil {
				total += colorDistance(previous.Color, palette[0].Color)
			}
			c := palette[0]
			previous = &c
		}
		return total
	}

	unsmoothed := totalJump(0)
	smoothed := totalJump(0.8)
	if smoothed >= unsmoothed {
		t.Fatalf("smoothing did not reduce oscillation: smoothed %v, unsmoothed %v", smoothed, unsmoothed)
	}
}

func quartersImage(width, height int) image.Image {
	colors := []color.RGBA{
		{R: 255, A: 255},
		{G: 255, A: 255},
		{B: 255, A: 255},
		{R: 255, G: 255, B: 255, A: 255},
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			quadrant := 0
			if x >= width/2 {
				quadrant++
			}
			if y >= height/2 {
				quadrant += 2
			}
			img.Set(x, y, colors[quadrant])
		}
	}
	return img
}

func distanceTo(c color.Color, target color.RGBA) float64 {
	return colorDistance(c, target)
}
