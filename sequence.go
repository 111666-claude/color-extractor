package color_extractor

import (
	"image"
	"image/color"
	"math"
	"sort"
)

// SequenceConfig controls how SequenceExtractor turns per-frame dominant
// colors into a stable palette.
type SequenceConfig struct {
	// Config is the single-image extraction config reused for every frame.
	Config Config
	// ColorCount is the target palette size: the maximum number of color
	// slots tracked at once. Values <= 0 are treated as 5.
	ColorCount int
	// Smoothing is the exponential moving average factor in [0, 1] applied
	// to slot colors and weights. Higher values give smoother, slower
	// changes; 0 tracks each frame exactly.
	Smoothing float64
	// MatchDistance is the maximum RGB euclidean distance (channels in
	// 0-255) at which an extracted color is considered the same as an
	// existing slot. Colors farther away are treated as new. Values <= 0
	// are treated as 64.
	MatchDistance float64
	// MinWeight is the minimum frame weight (fraction of the frame, 0-1) a
	// new color needs to be admitted into the palette. Values <= 0 are
	// treated as 0.05.
	MinWeight float64
	// MaxMissing is the number of consecutive frames a slot may be absent
	// (e.g. occluded) before its ID is retired. Negative values are
	// treated as 0.
	MaxMissing int
}

// DefaultSequenceConfig returns a SequenceConfig with sane defaults for
// animated wallpapers.
func DefaultSequenceConfig() SequenceConfig {
	return SequenceConfig{
		Config: Config{
			DownSizeTo:  224.,
			SmallBucket: .01,
		},
		ColorCount:    5,
		Smoothing:     0.6,
		MatchDistance: 64,
		MinWeight:     0.05,
		MaxMissing:    5,
	}
}

func (c SequenceConfig) normalized() SequenceConfig {
	if c.ColorCount <= 0 {
		c.ColorCount = 5
	}
	if c.Smoothing < 0 {
		c.Smoothing = 0
	}
	if c.Smoothing > 1 {
		c.Smoothing = 1
	}
	if c.MatchDistance <= 0 {
		c.MatchDistance = 64
	}
	if c.MinWeight <= 0 {
		c.MinWeight = 0.05
	}
	if c.MaxMissing < 0 {
		c.MaxMissing = 0
	}
	return c
}

// PaletteColor is one entry of a frame's stable palette.
type PaletteColor struct {
	// ID is stable across frames: the same ID tracks the same color as it
	// drifts, and survives short occlusions.
	ID int
	// Color is the smoothed color of this entry.
	Color color.Color
	// Weight is the smoothed fraction of the frame covered by this color.
	Weight float64
}

// slot is the internal mutable state of one palette entry.
type slot struct {
	id               int
	red, green, blue float64
	weight           float64
	missing          int
}

// SequenceExtractor extracts a stable palette from a sequence of frames.
// Its state is bounded: at most ColorCount slots are kept, no frame history
// is stored. It is not safe for concurrent use.
type SequenceExtractor struct {
	config SequenceConfig
	slots  []slot
	nextID int
}

// NewSequenceExtractor creates a SequenceExtractor with the given config.
func NewSequenceExtractor(config SequenceConfig) *SequenceExtractor {
	return &SequenceExtractor{config: config.normalized()}
}

// NextFrame feeds one frame and returns the frame's stable palette, sorted
// by weight (descending), ties broken by ID (ascending). Empty or fully
// transparent frames yield no new colors; existing slots are kept (and
// aged) so the result is always deterministic.
func (s *SequenceExtractor) NextFrame(img image.Image) []PaletteColor {
	buckets, total := extractBuckets(img, s.config.Config)

	type candidate struct {
		red, green, blue float64
		weight           float64
	}
	candidates := make([]candidate, 0, len(buckets))
	if total > 0 {
		for _, b := range buckets {
			candidates = append(candidates, candidate{
				red:    b.Red,
				green:  b.Green,
				blue:   b.Blue,
				weight: b.Count / total,
			})
		}
	}

	// Greedy matching: consider every (slot, candidate) pair within
	// MatchDistance, closest first, and assign each pair at most once.
	// Tie-breaking by slot ID and candidate index keeps it deterministic.
	type pair struct {
		distance  float64
		slot      int
		candidate int
	}
	pairs := []pair{}
	for si := range s.slots {
		for ci, c := range candidates {
			d := math.Sqrt(
				math.Pow(s.slots[si].red-c.red, 2) +
					math.Pow(s.slots[si].green-c.green, 2) +
					math.Pow(s.slots[si].blue-c.blue, 2))
			if d <= s.config.MatchDistance {
				pairs = append(pairs, pair{distance: d, slot: si, candidate: ci})
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].distance != pairs[j].distance {
			return pairs[i].distance < pairs[j].distance
		}
		if pairs[i].slot != pairs[j].slot {
			return s.slots[pairs[i].slot].id < s.slots[pairs[j].slot].id
		}
		return pairs[i].candidate < pairs[j].candidate
	})

	matchedSlots := make([]bool, len(s.slots))
	matchedCandidates := make([]bool, len(candidates))
	for _, p := range pairs {
		if matchedSlots[p.slot] || matchedCandidates[p.candidate] {
			continue
		}
		matchedSlots[p.slot] = true
		matchedCandidates[p.candidate] = true
		sl := &s.slots[p.slot]
		c := candidates[p.candidate]
		keep := s.config.Smoothing
		sl.red = keep*sl.red + (1-keep)*c.red
		sl.green = keep*sl.green + (1-keep)*c.green
		sl.blue = keep*sl.blue + (1-keep)*c.blue
		sl.weight = keep*sl.weight + (1-keep)*c.weight
		sl.missing = 0
	}

	// Admit new colors, strongest first, while there is room.
	newOnes := []candidate{}
	for ci, c := range candidates {
		if !matchedCandidates[ci] && c.weight >= s.config.MinWeight {
			newOnes = append(newOnes, c)
		}
	}
	sort.Slice(newOnes, func(i, j int) bool { return newOnes[i].weight > newOnes[j].weight })
	for _, c := range newOnes {
		if len(s.slots) >= s.config.ColorCount {
			break
		}
		s.slots = append(s.slots, slot{
			id:     s.nextID,
			red:    c.red,
			green:  c.green,
			blue:   c.blue,
			weight: c.weight,
		})
		s.nextID++
	}

	// Age unmatched slots and retire ones missing for too long. This keeps
	// IDs stable through short occlusions and the state bounded. Slots
	// admitted above were matched this frame, so they are never aged here.
	kept := s.slots[:0]
	for i := range s.slots {
		if i < len(matchedSlots) && !matchedSlots[i] {
			s.slots[i].missing++
		}
		if s.slots[i].missing <= s.config.MaxMissing {
			kept = append(kept, s.slots[i])
		}
	}
	s.slots = kept

	// Export, sorted by weight (desc), ID (asc) for deterministic output.
	palette := make([]PaletteColor, 0, len(s.slots))
	for _, sl := range s.slots {
		palette = append(palette, PaletteColor{
			ID: sl.id,
			Color: color.RGBA{
				R: uint8(math.Round(sl.red)),
				G: uint8(math.Round(sl.green)),
				B: uint8(math.Round(sl.blue)),
				A: 255,
			},
			Weight: sl.weight,
		})
	}
	sort.Slice(palette, func(i, j int) bool {
		if palette[i].Weight != palette[j].Weight {
			return palette[i].Weight > palette[j].Weight
		}
		return palette[i].ID < palette[j].ID
	})
	return palette
}
