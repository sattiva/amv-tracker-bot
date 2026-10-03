package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const size = 64

type EmojiRegistry struct {
	sync.RWMutex
	cache map[string]*discordgo.Emoji
}

var (
	Emojis    = &EmojiRegistry{cache: make(map[string]*discordgo.Emoji)}
	emojiHTTP = &http.Client{Timeout: 5 * time.Second}
)

// discord wants emoji as raw image data and we dont ship png files, so the
// button icons get drawn here and uploaded on startup
func badge(px func(x, y int) (color.RGBA, bool)) string {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if c, ok := px(x, y); ok {
				img.SetRGBA(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func shape(c color.RGBA, keep func(x, y int) bool) string {
	return badge(func(x, y int) (color.RGBA, bool) { return c, keep(x, y) })
}

func genCircle(r, g, b, a uint8) string {
	c := color.RGBA{r, g, b, a}
	return badge(func(x, y int) (color.RGBA, bool) {
		d := math.Hypot(float64(x)-31.5, float64(y)-31.5)
		if d <= 23 {
			return c, true
		}
		if d <= 25 {
			fade := c
			fade.A = uint8(float64(a) * (1 - (d-23)/2))
			return fade, true
		}
		return c, false
	})
}

func genHome(r, g, b, a uint8) string {
	return shape(color.RGBA{r, g, b, a}, func(x, y int) bool {
		fx, fy := float64(x), float64(y)
		// roof peaks at (31.5, 8), base at y=31
		roof := fy >= 8+22*math.Abs((fx-31.5)/21.5) && fy <= 31 && fx >= 10 && fx <= 53
		body := fy >= 31 && fy <= 56 && fx >= 16 && fx <= 47
		door := fy >= 42 && fy <= 56 && fx >= 26 && fx <= 36
		return (roof || body) && !door
	})
}

func genReset(r, g, b, a uint8) string {
	return shape(color.RGBA{r, g, b, a}, func(x, y int) bool {
		// ring with the top right slice missing, arrow tip fills it.
		// tip is on pixel centres so its the only one using x+0.5
		d := math.Hypot(float64(x)-31.5, float64(y)-31.5)
		gap := math.Atan2(float64(y)-31.5, float64(x)-31.5)
		ring := d >= 16 && d <= 22 && !(gap >= -1.45 && gap <= -0.1)
		fx, fy := float64(x)+0.5, float64(y)+0.5
		tip := fx >= 32 && fx <= 48 && math.Abs(fy-13) <= (48-fx)*(9.0/16.0)
		return ring || tip
	})
}

func genCross(r, g, b, a uint8) string {
	return shape(color.RGBA{r, g, b, a}, func(x, y int) bool {
		dx, dy := math.Abs(float64(x)-31.5), math.Abs(float64(y)-31.5)
		return dx <= 20 && dy <= 20 && math.Abs(dx-dy) <= 5
	})
}

func genSearch(r, g, b, a uint8) string {
	return shape(color.RGBA{r, g, b, a}, func(x, y int) bool {
		fx, fy := float64(x), float64(y)
		d := math.Hypot(fx-25.5, fy-25.5)
		ring := d >= 10.5 && d <= 14
		handle := fx >= 35 && fx <= 52 && fy >= 35 && fy <= 52 && math.Abs(fx-fy) <= 3
		return ring || handle
	})
}

func genArrow(r, g, b, a uint8, right bool) string {
	return shape(color.RGBA{r, g, b, a}, func(x, y int) bool {
		dx, dy := float64(x)-31.5, math.Abs(float64(y)-31.5)
		if !right {
			dx = -dx
		}
		shaft := dx >= -18 && dx <= 18 && dy <= 8
		head := dx >= 0 && dx <= 20 && dy <= 20-dx
		return shaft || head
	})
}

func (r *EmojiRegistry) add(s *discordgo.Session, guildID, name, dataURI string) {
	r.RLock()
	_, ok := r.cache[name]
	r.RUnlock()
	if ok || dataURI == "" {
		return
	}
	em, err := s.GuildEmojiCreate(guildID, &discordgo.EmojiParams{Name: name, Image: dataURI})
	if err != nil || em == nil {
		return
	}
	r.Lock()
	r.cache[name] = em
	r.Unlock()
}

func fetchImage(u string) string {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	res, err := emojiHTTP.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		return ""
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil || len(b) == 0 {
		return ""
	}
	return "data:image/webp;base64," + base64.StdEncoding.EncodeToString(b)
}

func (r *EmojiRegistry) Init(s *discordgo.Session, guildID string) error {
	if guildID == "" {
		return fmt.Errorf("empty guild id")
	}
	existing, err := s.GuildEmojis(guildID)
	if err != nil {
		return err
	}

	r.Lock()
	for _, e := range existing {
		if strings.HasPrefix(e.Name, "amv_") {
			r.cache[e.Name] = e
		}
	}
	r.Unlock()

	white := color.RGBA{255, 255, 255, 255}
	for _, b := range []struct {
		name, uri string
	}{
		{"amv_win", genCircle(46, 204, 113, 255)},
		{"amv_lose", genCircle(231, 76, 60, 255)},
		{"amv_fair", genCircle(241, 196, 15, 255)},
		{"amv_btn_base", genHome(white.R, white.G, white.B, white.A)},
		{"amv_btn_reset", genReset(white.R, white.G, white.B, white.A)},
		{"amv_btn_side1", genArrow(0, 210, 255, 255, false)},
		{"amv_btn_side2", genArrow(155, 89, 182, 255, true)},
		{"amv_btn_clear", genCross(231, 76, 60, 255)},
		{"amv_btn_reg", genCircle(189, 195, 199, 255)},
		{"amv_btn_search", genSearch(white.R, white.G, white.B, white.A)},
	} {
		r.add(s, guildID, b.name, b.uri)
	}

	// the pet/egg buttons just borrow some random item icon. if amvgg ever
	// removes that item the emoji is a broken image forever so, yeah, good luck
	egg, pet := "Safari Egg", "Bat Dragon"
	DB.RLock()
	if len(DB.Eggs) > 0 {
		egg = DB.Eggs[rand.Intn(len(DB.Eggs))].Name
	}
	if len(DB.Pets) > 0 {
		pet = DB.Pets[rand.Intn(len(DB.Pets))].Name
	}
	DB.RUnlock()

	for name, u := range map[string]string{
		"amv_fly":     "https://amvgg.com/_next/static/media/FlyIcon.104017eb.webp",
		"amv_ride":    "https://amvgg.com/_next/static/media/RideIcon.66461c68.webp",
		"amv_neon":    "https://amvgg.com/_next/static/media/NeonIcon.aba60d73.webp",
		"amv_mega":    "https://amvgg.com/_next/static/media/MegaIcon.a4fe8b8a.webp",
		"amv_ridepot": "https://amvgg.com/items/Ride-A-Pet%20Potion.webp",
		"amv_flypot":  "https://amvgg.com/items/Fly-A-Pet%20Potion.webp",
		"amv_btn_egg": imgURL(egg),
		"amv_btn_pet": imgURL(pet),
	} {
		r.add(s, guildID, name, fetchImage(u))
	}
	return nil
}

func (r *EmojiRegistry) Tag(name string) string {
	e := r.get(name)
	if e == nil {
		return ""
	}
	return fmt.Sprintf("<:%s:%s>", e.Name, e.ID)
}

func (r *EmojiRegistry) CompMap(name string) map[string]interface{} {
	e := r.get(name)
	if e == nil {
		return nil
	}
	return map[string]interface{}{"id": e.ID, "name": e.Name, "animated": false}
}

func (r *EmojiRegistry) get(name string) *discordgo.Emoji {
	r.RLock()
	defer r.RUnlock()
	return r.cache[name]
}
