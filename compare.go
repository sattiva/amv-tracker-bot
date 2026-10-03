package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

type TradeItem struct {
	Name, Variant string
	Value         float64
	IsEgg         bool
}

type CompareSession struct {
	ID, Variant, Cat string
	Side1, Side2     []TradeItem
	ActiveSide       int
	Updated          time.Time
}

func (cs *CompareSession) Total(side int) float64 {
	items := cs.Side1
	if side == 2 {
		items = cs.Side2
	}
	sum := 0.0
	for _, it := range items {
		sum += it.Value
	}
	return sum
}

func (cs *CompareSession) Add(name string) {
	var val float64
	isEgg := cs.Cat == "eggs"
	if isEgg {
		if e := DB.FindEgg(name); e != nil {
			val, _ = strconv.ParseFloat(e.Value, 64)
		}
	} else if p := DB.FindPet(name); p != nil {
		raw := p.RegularValue
		if cs.Variant == "neon" {
			raw = p.NeonValue
		}
		if cs.Variant == "mega" {
			raw = p.MegaValue
		}
		val, _ = strconv.ParseFloat(raw, 64)
	}

	it := TradeItem{Name: name, Variant: cs.Variant, Value: val, IsEgg: isEgg}
	if cs.ActiveSide == 1 {
		cs.Side1 = append(cs.Side1, it)
	} else {
		cs.Side2 = append(cs.Side2, it)
	}
}

type CompareManager struct {
	sync.RWMutex
	sessions map[string]*CompareSession
}

var Compare = &CompareManager{sessions: make(map[string]*CompareSession)}

func (cm *CompareManager) Get(id string) *CompareSession {
	cm.Lock()
	defer cm.Unlock()
	sess, ok := cm.sessions[id]
	if !ok {
		sess = &CompareSession{ID: id, ActiveSide: 1, Variant: "reg", Cat: "pets", Updated: time.Now()}
		cm.sessions[id] = sess
	}
	sess.Updated = time.Now()
	return sess
}

func (cm *CompareManager) Clean() {
	cutoff := time.Now().Add(-30 * time.Minute)
	cm.Lock()
	defer cm.Unlock()
	for id, sess := range cm.sessions {
		if sess.Updated.Before(cutoff) {
			delete(cm.sessions, id)
		}
	}
}

func itemLines(items []TradeItem) string {
	if len(items) == 0 {
		return "-# *No items added yet*"
	}
	var sb strings.Builder
	for _, it := range items {
		tag := Emojis.Tag("amv_btn_pet")
		switch {
		case it.IsEgg:
			tag = Emojis.Tag("amv_btn_egg")
		case it.Variant == "neon":
			tag = Emojis.Tag("amv_neon")
		case it.Variant == "mega":
			tag = Emojis.Tag("amv_mega")
		}
		if tag != "" {
			tag += " "
		}
		sb.WriteString(fmt.Sprintf("- %s%s `(%.3f)`\n", tag, it.Name, it.Value))
	}
	return sb.String()
}

func verdict(t1, t2 float64) (string, int) {
	res := fmt.Sprintf("%s **FAIR**", Emojis.Tag("amv_fair"))
	if t1 <= 0 && t2 <= 0 {
		return res, colGray
	}
	// fair while the gap is under 5% of the bigger side. 0.02 floor so cheap
	// trades dont get decided by rounding
	edge := math.Max(0.02, 0.05*math.Max(t1, t2))
	switch diff := t2 - t1; {
	case diff > edge:
		return fmt.Sprintf("%s **WIN (+%.3f)**", Emojis.Tag("amv_win"), diff), 0x2ECC71
	case diff < -edge:
		return fmt.Sprintf("%s **LOSE (-%.3f)**", Emojis.Tag("amv_lose"), -diff), colErr
	}
	return fmt.Sprintf("%s **FAIR (diff: %.3f)**", Emojis.Tag("amv_fair"), t2-t1), colGray
}

func RenderCompareV2(sess *CompareSession, guildName string) []discordgo.MessageComponent {
	t1, t2 := sess.Total(1), sess.Total(2)
	res, accent := verdict(t1, t2)

	var sb strings.Builder
	sb.WriteString("## Trade Comparison\n\n")
	sb.WriteString(fmt.Sprintf("%s **Your Offer (Side 1)** | Total: `%.3f`\n%s", Emojis.Tag("amv_btn_side1"), t1, itemLines(sess.Side1)))
	sb.WriteString(fmt.Sprintf("\n%s **Their Offer (Side 2)** | Total: `%.3f`\n%s", Emojis.Tag("amv_btn_side2"), t2, itemLines(sess.Side2)))
	sb.WriteString(fmt.Sprintf("\n### Result: %s\n", res))
	sb.WriteString(footer(fmt.Sprintf("Editing: Side %d (%s)", sess.ActiveSide, sess.Variant), guildName))

	catLabel, catEmoji := "Eggs", "amv_btn_egg"
	if sess.Cat == "eggs" {
		catLabel, catEmoji = "Pets", "amv_btn_pet"
	}

	return container(accent,
		text(strings.TrimSpace(sb.String())),
		divider(),
		picker("cmp:sel:"+sess.ID,
			fmt.Sprintf("Add %s to Side %d (%s)...", sess.Cat, sess.ActiveSide, sess.Variant),
			itemOptions(sess.Cat, sess.Variant, "")),
		row(
			btn("Your Offer (Side 1)", "amv_btn_side1", style(sess.ActiveSide == 1), "cmp:side:1:"+sess.ID),
			btn("Their Offer (Side 2)", "amv_btn_side2", style(sess.ActiveSide == 2), "cmp:side:2:"+sess.ID),
			btn(catLabel, catEmoji, 3, "cmp:cat:"+sess.ID),
			btn("Clear Side", "amv_btn_clear", 4, "cmp:clear:"+sess.ID),
			btn("Reset", "amv_btn_reset", 2, "cmp:reset:"+sess.ID),
		),
		row(
			btn("Base", "amv_btn_base", style(sess.Variant == "reg"), "cmp:var:reg:"+sess.ID),
			btn("Neon", "amv_neon", style(sess.Variant == "neon"), "cmp:var:neon:"+sess.ID),
			btn("Mega", "amv_mega", style(sess.Variant == "mega"), "cmp:var:mega:"+sess.ID),
			btn("Search", "amv_btn_search", 2, "cmp:search:"+sess.ID),
		),
	)
}
