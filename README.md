Simple image color extractor written in Go with no external dependencies.

[![Go Reference](https://pkg.go.dev/badge/github.com/marekm4/color-extractor.svg)](https://pkg.go.dev/github.com/marekm4/color-extractor)
[![Go Report Card](https://goreportcard.com/badge/github.com/marekm4/color-extractor)](https://goreportcard.com/report/github.com/marekm4/color-extractor)
[![Go Coverage](https://github.com/marekm4/color-extractor/wiki/coverage.svg)](https://raw.githack.com/wiki/marekm4/color-extractor/coverage.html)

Demo:

https://color-extractor-demo.marekm4.com/

Blog post:

https://medium.com/@marek.michalik/c-vs-rust-vs-go-performance-analysis-945ab749056c

Usage:
```go
package main

import (
	"fmt"
	"image"
	_ "image/jpeg"
	"os"

	"github.com/marekm4/color-extractor"
)

func main() {
	file := "Fotolia_45549559_320_480.jpg"
	imageFile, _ := os.Open(file)
	defer imageFile.Close()

	image, _, _ := image.Decode(imageFile)
	colors := color_extractor.ExtractColors(image)

	fmt.Println(colors)
}
```

Example image:

![Image](https://raw.githubusercontent.com/marekm4/color-extractor/master/example/Fotolia_45549559_320_480.jpg)

Extracted colors:

![Colors](https://raw.githubusercontent.com/marekm4/color-extractor/master/example/colors.png)

Stable palettes for frame sequences (e.g. animated wallpapers):

`SequenceExtractor` reuses the single-image extraction on every frame, then
matches colors across frames so each color keeps a stable ID instead of
flickering between near-identical values. Colors and weights are smoothed
with an exponential moving average, and IDs survive short occlusions.

```go
extractor := color_extractor.NewSequenceExtractor(color_extractor.DefaultSequenceConfig())
for _, frame := range frames {
	palette := extractor.NextFrame(frame) // []PaletteColor{ID, Color, Weight}, sorted by weight
	fmt.Println(palette)
}
```

`SequenceConfig` knobs (see `DefaultSequenceConfig` for defaults):

- `ColorCount` — target palette size; state never grows beyond this many slots.
- `Smoothing` — EMA factor in [0, 1]; higher means smoother, slower changes.
- `MatchDistance` — RGB distance below which a color keeps its ID; farther colors are new.
- `MinWeight` — minimum frame fraction for a new color to enter the palette.
- `MaxMissing` — frames a color may be absent (occluded) before its ID is retired.

Empty or fully transparent frames add no colors and simply age existing
slots; results are deterministic and memory stays bounded for arbitrarily
long sequences.
