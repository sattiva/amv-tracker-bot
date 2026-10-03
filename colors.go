package main

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/webp"
)

const colorFile = "colors.json"

type ColorCache struct {
	sync.Mutex
	Colors map[string]int
}

var (
	Colors     = &ColorCache{Colors: make(map[string]int)}
	colorHTTP  = &http.Client{Timeout: 5 * time.Second}
	vibrantPal = [16]int{
		0x00D2FF, 0x9B59B6, 0x2ECC71, 0xE74C3C,
		0xF1C40F, 0xE67E22, 0x1ABC9C, 0xFD79A8,
		0x6C5CE7, 0x0984E3, 0x00CEC9, 0xFF7675,
		0xFDCB6E, 0xE84393, 0x27AE60, 0x3498DB,
	}
)

func init() {
	if b, err := os.ReadFile(colorFile); err == nil {
		_ = json.Unmarshal(b, &Colors.Colors)
	}
}

func (c *ColorCache) save() {
	b, _ := json.Marshal(c.Colors)
	_ = os.WriteFile(colorFile, b, 0644)
}

func quant(v uint32) int {
	return int(v>>3) << 3
}

func pickColor(counts map[int]int) int {
	best, score := 0, 0.0
	plain, most := 0, 0
	for key, n := range counts {
		if n > most {
			most, plain = n, key
		}
		r, g, b := float64(key>>16&0xFF)/255, float64(key>>8&0xFF)/255, float64(key&0xFF)/255
		hi, lo := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
		if lit := (hi + lo) / 2; lit < 0.08 || lit > 0.96 {
			continue
		}
		if s := float64(n) * (0.3 + 0.7*(hi-lo)/hi); s > score {
			score, best = s, key
		}
	}
	if best != 0 {
		return best
	}
	return plain
}

func extractDominantColor(name string) (int, error) {
	res, err := colorHTTP.Get(imgURL(name))
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	img, err := webp.Decode(res.Body)
	if err != nil {
		return 0, err
	}

	// icons are mostly transparent background, drop it before it wins the count
	step := 1
	if b := img.Bounds(); b.Dx() > 128 {
		step = 2 // every other pixel over 128px, still plenty to vote with
	}
	counts := map[int]int{}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y += step {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x += step {
			r, g, b, a := img.At(x, y).RGBA()
			// mostly transparent background, 60 cuts it out of the vote
			if uint8(a>>8) < 60 {
				continue
			}
			counts[quant(r>>8)<<16|quant(g>>8)<<8|quant(b>>8)]++
		}
	}
	return pickColor(counts), nil
}

func hash(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h = (h ^ uint32(s[i])) * 16777619
	}
	return h
}

// first call cant wait on the download so we park -1 as in flight and hand back
// a palette color, the goroutine swaps in the real one a moment later
func GetColor(name, cat string) int {
	key := strings.ToLower(strings.TrimSpace(name))

	Colors.Lock()
	c, cached := Colors.Colors[key]
	if !cached {
		Colors.Colors[key] = -1
	}
	Colors.Unlock()

	if cached {
		if c > 0 {
			return c
		}
	} else {
		go func() {
			found, err := extractDominantColor(name)
			Colors.Lock()
			defer Colors.Unlock()
			if err != nil || found == 0 {
				delete(Colors.Colors, key)
				return
			}
			Colors.Colors[key] = found
			Colors.save()
		}()
	}

	if cat == "eggs" {
		return 0xF1C40F
	}
	// no cached color yet so hash the name into the palette. same item always
	// lands on the same color which is the only reason this looks intentional
	return vibrantPal[hash(key)%16]
}
