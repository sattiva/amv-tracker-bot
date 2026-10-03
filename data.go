package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Pet struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	RegularValue   string  `json:"regularValue"`
	NpRegularValue *string `json:"npRegularValue"`
	NeonValue      string  `json:"neonValue"`
	NpNeonValue    *string `json:"npNeonValue"`
	MegaValue      string  `json:"megaValue"`
	NpMegaValue    *string `json:"npMegaValue"`
	RegularDemand  string  `json:"regularDemand"`
	NeonDemand     string  `json:"neonDemand"`
	MegaDemand     string  `json:"megaDemand"`
	Origin         string  `json:"origin"`
	Category       int     `json:"category"`
	RValue         *string `json:"rValue"`
	FValue         *string `json:"fValue"`
	NrValue        *string `json:"nrValue"`
	NfValue        *string `json:"nfValue"`
	MrValue        *string `json:"mrValue"`
	MfValue        *string `json:"mfValue"`
}

type Egg struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Demand string `json:"demand"`
}

type ValueDiff struct {
	Name, Variant, OldVal, NewVal string
	IsEgg                         bool
}

type Store struct {
	sync.RWMutex
	Pets        []Pet
	Eggs        []Egg
	OnUpdate    func(diffs []ValueDiff)
	prevPets    map[string]Pet
	prevEggs    map[string]Egg
	lastModPets string
	lastModEggs string
}

var (
	DB = &Store{}
	hc = &http.Client{Timeout: 15 * time.Second}
)

func imgURL(name string) string {
	return "https://amvgg.com/items/" + url.PathEscape(name) + ".webp"
}

// ?_rsc=1 skips the html and hands back just the data, way smaller.
// if you dont do this youre a fucking dumbass lmao
// if-modified-since means nothing changed comes back 304, so nil = no change
func pullRSC(path, key, lastMod string) ([]byte, string, error) {
	req, err := http.NewRequest("GET", "https://amvgg.com"+path+"?_rsc=1", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("RSC", "1")
	if lastMod != "" {
		req.Header.Set("If-Modified-Since", lastMod)
	}

	res, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		return nil, lastMod, nil
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", err
	}
	chunk, err := sliceKey(body, key)
	if err != nil {
		return nil, "", err
	}
	return chunk, res.Header.Get("Last-Modified"), nil
}

// flight payload is lines of "<id>:<json>" with our array buried in one of them.
// cant unmarshal the whole thing (the rest isnt valid json) so find the marker
// and count brackets till it closes
func sliceKey(body []byte, key string) ([]byte, error) {
	marker := `"` + key + `":[`
	for _, line := range strings.SplitAfter(string(body), "\n") {
		i := strings.Index(line, marker)
		if i == -1 {
			continue
		}
		chunk := line[i+len(marker)-1:]
		depth := 0
		for j, c := range chunk {
			if c == '[' {
				depth++
			} else if c == ']' {
				if depth--; depth == 0 {
					return []byte(chunk[:j+1]), nil
				}
			}
		}
	}
	return nil, fmt.Errorf("key %s not found in rsc", key)
}

func pull[T any](path, key, lastMod string) ([]T, string, error) {
	raw, newMod, err := pullRSC(path, key, lastMod)
	if err != nil || raw == nil {
		return nil, newMod, err
	}
	var out []T
	err = json.Unmarshal(raw, &out)
	return out, newMod, err
}

func (s *Store) Sync() error {
	s.RLock()
	lastPets, lastEggs := s.lastModPets, s.lastModEggs
	s.RUnlock()

	pets, newPets, err := pull[Pet]("/values/pets", "pets", lastPets)
	if err != nil {
		return err
	}
	eggs, newEggs, err := pull[Egg]("/values/eggs", "items", lastEggs)
	if err != nil {
		return err
	}
	// both nil means both pages were 304, nothing to diff
	if pets == nil && eggs == nil {
		return nil
	}

	s.Lock()
	var diffs []ValueDiff
	if pets != nil {
		diffs = append(diffs, petDiffs(s.prevPets, pets)...)
		s.prevPets, s.Pets = petIndex(pets), pets
	}
	if eggs != nil {
		diffs = append(diffs, eggDiffs(s.prevEggs, eggs)...)
		s.prevEggs, s.Eggs = eggIndex(eggs), eggs
	}
	if newPets != "" {
		s.lastModPets = newPets
	}
	if newEggs != "" {
		s.lastModEggs = newEggs
	}
	cb := s.OnUpdate
	s.Unlock()

	if len(diffs) > 0 && cb != nil {
		go cb(diffs)
	}
	return nil
}

func petIndex(list []Pet) map[string]Pet {
	m := make(map[string]Pet, len(list))
	for _, p := range list {
		m[p.Name] = p
	}
	return m
}

func eggIndex(list []Egg) map[string]Egg {
	m := make(map[string]Egg, len(list))
	for _, e := range list {
		m[e.Name] = e
	}
	return m
}

func petDiffs(old map[string]Pet, cur []Pet) []ValueDiff {
	var out []ValueDiff
	for _, p := range cur {
		o, ok := old[p.Name]
		if !ok {
			continue
		}
		for _, v := range []struct{ variant, before, after string }{
			{"reg", o.RegularValue, p.RegularValue},
			{"neon", o.NeonValue, p.NeonValue},
			{"mega", o.MegaValue, p.MegaValue},
		} {
			if v.before != v.after {
				out = append(out, ValueDiff{p.Name, v.variant, v.before, v.after, false})
			}
		}
	}
	return out
}

func eggDiffs(old map[string]Egg, cur []Egg) []ValueDiff {
	var out []ValueDiff
	for _, e := range cur {
		if o, ok := old[e.Name]; ok && o.Value != e.Value {
			out = append(out, ValueDiff{e.Name, "reg", o.Value, e.Value, true})
		}
	}
	return out
}

func (s *Store) StartWatcher(d time.Duration) {
	_ = s.Sync()
	go func() {
		for range time.Tick(d) {
			_ = s.Sync()
		}
	}()
}

func (s *Store) FindPet(name string) *Pet {
	s.RLock()
	defer s.RUnlock()
	for i := range s.Pets {
		if strings.EqualFold(s.Pets[i].Name, name) {
			return &s.Pets[i]
		}
	}
	return nil
}

func (s *Store) FindEgg(name string) *Egg {
	s.RLock()
	defer s.RUnlock()
	for i := range s.Eggs {
		if strings.EqualFold(s.Eggs[i].Name, name) {
			return &s.Eggs[i]
		}
	}
	return nil
}

func match(hay, needle string) int {
	switch {
	case strings.EqualFold(hay, needle):
		return 3
	case strings.HasPrefix(strings.ToLower(hay), needle):
		return 2
	case strings.Contains(strings.ToLower(hay), needle):
		return 1
	}
	return 0
}

func (s *Store) Search(q string) (*Pet, *Egg) {
	low := strings.ToLower(strings.TrimSpace(q))
	if low == "" {
		return nil, nil
	}

	s.RLock()
	defer s.RUnlock()

	var pet *Pet
	egg, best := (*Egg)(nil), 0
	for i := range s.Pets {
		if r := match(s.Pets[i].Name, low); r > best {
			best, pet = r, &s.Pets[i]
		}
	}
	for i := range s.Eggs {
		if r := match(s.Eggs[i].Name, low); r > best {
			best, egg = r, &s.Eggs[i]
		}
	}
	return pet, egg
}

// one message per changed item now so a bad scrape is hundreds of messages
// lmao blame zeta (1025844251193528381)
const maxDiffEmbeds = 40

func valLine(emoji, label, before, after string) string {
	tag := Emojis.Tag(emoji)
	if tag != "" {
		tag += " "
	}
	return fmt.Sprintf("%s**%s**: `%s` -> `%s`", tag, label, before, after)
}

func itemLine(emoji, name, val, demand string) string {
	tag := Emojis.Tag(emoji)
	if tag != "" {
		tag += " "
	}
	return fmt.Sprintf("| %s**%s**: `%s` | Demand: **%s**", tag, name, val, demand)
}

func (s *Store) FormatDiffBatchesV2(diffs []ValueDiff, serverName, serverIcon string) [][]discordgo.MessageComponent {
	type itemDiff struct {
		name  string
		isEgg bool
		lines []string
	}

	seen := make(map[string]int, len(diffs))
	var items []itemDiff
	for _, d := range diffs {
		i, ok := seen[d.Name]
		if !ok {
			seen[d.Name] = len(items)
			items = append(items, itemDiff{name: d.Name, isEgg: d.IsEgg})
			i = len(items) - 1
		}

		emoji, label := "amv_btn_pet", "Value"
		switch {
		case d.IsEgg:
			emoji = "amv_btn_egg"
		case d.Variant == "neon":
			emoji, label = "amv_neon", "Neon Value"
		case d.Variant == "mega":
			emoji, label = "amv_mega", "Mega Value"
		}
		items[i].lines = append(items[i].lines, valLine(emoji, label, d.OldVal, d.NewVal))
	}

	extra := 0
	if len(items) > maxDiffEmbeds {
		extra, items = len(items)-maxDiffEmbeds, items[:maxDiffEmbeds]
	}

	out := make([][]discordgo.MessageComponent, 0, len(items)+1)
	for _, it := range items {
		cat := "pets"
		if it.isEgg {
			cat = "eggs"
		}
		body := "## " + it.name + "\n\n" + strings.Join(it.lines, "\n") + "\n" + footer(serverName)
		out = append(out, container(GetColor(it.name, cat), section(strings.TrimSpace(body), imgURL(it.name))))
	}

	if extra > 0 {
		body := fmt.Sprintf("## Adopt Me Value Changes\n\n**+%d more items updated**\n\n%s", extra, footer(serverName))
		out = append(out, container(colGray, section(strings.TrimSpace(body), serverIcon)))
	}
	return out
}

func (s *Store) BuildLatestSummaryV2(serverName, serverIcon string) []discordgo.MessageComponent {
	s.RLock()
	defer s.RUnlock()

	pets := make([]string, 0, 6)
	for _, p := range s.Pets[:min(6, len(s.Pets))] {
		pets = append(pets, itemLine("amv_btn_pet", p.Name, p.RegularValue, p.RegularDemand))
	}
	eggs := make([]string, 0, 6)
	for _, e := range s.Eggs[:min(6, len(s.Eggs))] {
		eggs = append(eggs, itemLine("amv_btn_egg", e.Name, e.Value, e.Demand))
	}

	body := "## Adopt Me Value Update\n\n**High-Tier Pets**\n" + strings.Join(pets, "\n") +
		"\n\n**Iconic Eggs**\n" + strings.Join(eggs, "\n") + "\n" + footer(serverName)
	return container(colGray, section(strings.TrimSpace(body), serverIcon))
}

// fly/ride pots bump the base value, fr is both at once. these came off the
// amvgg sheet, not out of my ass
var potMult = map[string]float64{"fly": 1.15, "ride": 1.075, "fr": 1.24}

func petVal(p *Pet, variant, mode string) string {
	base, ride, fly := p.RegularValue, p.RValue, p.FValue
	switch variant {
	case "neon":
		base, ride, fly = p.NeonValue, p.NrValue, p.NfValue
	case "mega":
		base, ride, fly = p.MegaValue, p.MrValue, p.MfValue
	}
	if mode == "" || mode == "base" {
		return base
	}
	pot := ride
	if mode != "ride" {
		pot = fly
	}
	if pot != nil && *pot != "" {
		return *pot
	}
	f, err := strconv.ParseFloat(base, 64)
	if err != nil || f == 0 {
		return "N/A"
	}
	return fmt.Sprintf("%.3f", f*potMult[mode])
}
