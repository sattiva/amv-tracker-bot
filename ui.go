package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

type ViewState struct {
	UserID, Cat, Name, Mode, Var string
}

func parseCustomID(id string) ViewState {
	p := strings.Split(id, ":")
	vs := ViewState{Cat: "pets", Name: "Bat Dragon", Mode: "base", Var: "reg"}
	if len(p) > 1 {
		vs.UserID = p[1]
	}
	if len(p) > 2 {
		vs.Cat = p[2]
	}
	if len(p) > 3 {
		vs.Name = p[3]
	}
	if len(p) > 4 {
		vs.Mode = p[4]
	}
	if len(p) > 5 {
		vs.Var = p[5]
	}
	return vs
}

func (v ViewState) String() string {
	return fmt.Sprintf("v:%s:%s:%s:%s:%s", v.UserID, v.Cat, v.Name, v.Mode, v.Var)
}

func (v ViewState) selID() string {
	return "sel:" + strings.TrimPrefix(v.String(), "v:")
}

type V2Component map[string]interface{}

func (v V2Component) Type() discordgo.ComponentType {
	if t, ok := v["type"].(int); ok {
		return discordgo.ComponentType(t)
	}
	return discordgo.ComponentType(17)
}

func (v V2Component) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}(v))
}

// v2 component types: 17 container, 9 section, 10 text, 11 media, 14 divider,
// 1 row, 2 button, 3 select. discordgo has no types for any of it.

func container(accent int, parts ...interface{}) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		V2Component{"type": 17, "accent_color": accent, "components": parts},
	}
}

func text(content string) map[string]interface{} {
	return map[string]interface{}{"type": 10, "content": content}
}

func section(content, img string) map[string]interface{} {
	s := map[string]interface{}{"type": 9, "components": []interface{}{text(content)}}
	if img != "" {
		s["accessory"] = map[string]interface{}{"type": 11, "media": map[string]interface{}{"url": img}}
	}
	return s
}

func divider() map[string]interface{} {
	return map[string]interface{}{"type": 14, "divider": true, "spacing": 1}
}

func row(parts ...interface{}) map[string]interface{} {
	return map[string]interface{}{"type": 1, "components": parts}
}

func btn(label, emoji string, style int, customID string) map[string]interface{} {
	b := map[string]interface{}{"type": 2, "style": style, "label": label, "custom_id": customID}
	if e := Emojis.CompMap(emoji); e != nil {
		b["emoji"] = e
	}
	return b
}

func picker(customID, placeholder string, opts []interface{}) map[string]interface{} {
	return row(map[string]interface{}{"type": 3, "custom_id": customID, "placeholder": placeholder, "options": opts})
}

// every embed gets one of these. remove it and i will put it back
func footer(parts ...string) string {
	bits := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		if p != "" {
			bits = append(bits, p)
		}
	}
	return "-# " + strings.Join(append(bits, "sativa was here"), " | ")
}

// tapping the tab youre already on turns it off
func toggle(cur, val, off string) string {
	if cur == val {
		return off
	}
	return val
}

// 1 primary (the active one), 2 secondary
func style(on bool) int {
	if on {
		return 1
	}
	return 2
}

// custom ids are built by string concat so the tail can be missing, fall back
func arg(p []string, i int, def string) string {
	if i < len(p) {
		return p[i]
	}
	return def
}

func option(label, desc string) map[string]interface{} {
	return map[string]interface{}{"label": label, "value": label, "description": desc}
}

func itemOptions(cat, variant, def string) []interface{} {
	DB.RLock()
	defer DB.RUnlock()

	// 25 is all discord takes in one select
	const limit = 25

	var opts []interface{}
	if cat == "eggs" {
		for i := 0; i < min(limit, len(DB.Eggs)); i++ {
			e := DB.Eggs[i]
			opts = append(opts, option(e.Name, fmt.Sprintf("Val: %s | Demand: %s", e.Value, e.Demand)))
		}
		return opts
	}
	for i := 0; i < min(limit, len(DB.Pets)); i++ {
		p := DB.Pets[i]
		val := p.RegularValue
		if variant == "neon" {
			val = p.NeonValue
		}
		if variant == "mega" {
			val = p.MegaValue
		}
		o := option(p.Name, fmt.Sprintf("Val: %s (%s) | Demand: %s", val, variant, p.RegularDemand))
		if p.Name == def {
			o["default"] = true
		}
		opts = append(opts, o)
	}
	return opts
}

func detail(v ViewState) (name, heading string, accent int, body string) {
	if v.Cat == "eggs" {
		e := DB.FindEgg(v.Name)
		if e == nil {
			DB.RLock()
			if len(DB.Eggs) > 0 {
				e = &DB.Eggs[0]
			}
			DB.RUnlock()
		}
		if e == nil {
			return v.Name, "## " + v.Name, 0xF1C40F, ""
		}
		val := petVal(&Pet{RegularValue: e.Value}, "reg", v.Mode)
		if v.Mode == "fly" || v.Mode == "ride" {
			val += " " + strings.ToUpper(v.Mode[:1]) + v.Mode[1:] + " Pots"
		}
		return e.Name, "## " + e.Name, GetColor(e.Name, "eggs"), fmt.Sprintf("**Value**: `%s` | **Demand**: **%s**", val, e.Demand)
	}

	p := DB.FindPet(v.Name)
	if p == nil {
		DB.RLock()
		if len(DB.Pets) > 0 {
			p = &DB.Pets[0]
		}
		DB.RUnlock()
	}
	if p == nil {
		return v.Name, "## " + v.Name, colGray, ""
	}
	name, accent = p.Name, GetColor(p.Name, "pets")
	heading = "## " + p.Name

	if (v.Var == "" || v.Var == "reg") && (v.Mode == "" || v.Mode == "base") {
		body = fmt.Sprintf("**Value**: `%s` | **Demand**: **%s**", p.RegularValue, p.RegularDemand)
		if p.Origin != "" {
			body += "\n**Origin**: " + p.Origin
		}
		return name, heading, accent, body
	}

	label, demand := "Value", p.RegularDemand
	np, ride, fly := p.NpRegularValue, p.RValue, p.FValue
	tag := Emojis.Tag("amv_btn_reg")
	switch v.Var {
	case "neon":
		label, demand = "Neon Value", p.NeonDemand
		np, ride, fly = p.NpNeonValue, p.NrValue, p.NfValue
		tag = Emojis.Tag("amv_neon")
		heading = "## [Neon] " + p.Name
	case "mega":
		label, demand = "Mega Value", p.MegaDemand
		np, ride, fly = p.NpMegaValue, p.MrValue, p.MfValue
		tag = Emojis.Tag("amv_mega")
		heading = "## [Mega] " + p.Name
	}

	var sb strings.Builder
	if tag != "" {
		tag += " "
	}
	val, suffix := petVal(p, v.Var, v.Mode), ""
	if v.Mode == "fly" || v.Mode == "ride" {
		suffix = " " + strings.ToUpper(v.Mode[:1]) + v.Mode[1:] + " Pots"
	}
	sb.WriteString(fmt.Sprintf("%s**%s**: `%s%s` | **Demand**: **%s**", tag, label, val, suffix, demand))

	var pots []string
	if np != nil {
		pots = append(pots, fmt.Sprintf("No-Pot: `%s`", *np))
	}
	if ride != nil {
		pots = append(pots, fmt.Sprintf("%s Ride: `%s`", Emojis.Tag("amv_ride"), *ride))
	}
	if fly != nil {
		pots = append(pots, fmt.Sprintf("%s Fly: `%s`", Emojis.Tag("amv_fly"), *fly))
	}
	if len(pots) > 0 {
		sb.WriteString("\n**Potions**: " + strings.Join(pots, " | "))
	}
	if p.Origin != "" {
		sb.WriteString("\n**Origin**: " + p.Origin)
	}
	return name, heading, accent, sb.String()
}

func RenderV2(v ViewState, guildName string) []discordgo.MessageComponent {
	name, heading, accent, body := detail(v)

	parts := []interface{}{
		section(heading+"\n\n"+body+"\n"+footer(guildName), imgURL(name)),
		divider(),
		picker(v.selID(), "Select "+strings.TrimSuffix(v.Cat, "s")+"...", itemOptions(v.Cat, v.Var, name)),
	}

	swap, swapLabel, swapEmoji := ViewState{v.UserID, "eggs", "Safari Egg", v.Mode, v.Var}, "Eggs", "amv_btn_egg"
	if v.Cat == "eggs" {
		swap, swapLabel, swapEmoji = ViewState{v.UserID, "pets", "Bat Dragon", v.Mode, v.Var}, "Pets", "amv_btn_pet"
	}
	search := btn("Search", "amv_btn_search", 2, "v:search:"+v.String())
	swapBtn := btn(swapLabel, swapEmoji, 3, swap.String())

	type tab struct {
		label, emoji string
		vs           ViewState
	}
	tabs := []tab{{"Base", "amv_btn_base", ViewState{v.UserID, v.Cat, name, "base", "reg"}}}
	if v.Cat != "eggs" {
		tabs = append(tabs,
			tab{"Neon", "amv_neon", ViewState{v.UserID, v.Cat, name, v.Mode, toggle(v.Var, "neon", "reg")}},
			tab{"Mega", "amv_mega", ViewState{v.UserID, v.Cat, name, v.Mode, toggle(v.Var, "mega", "reg")}},
			tab{"Fly", "amv_fly", ViewState{v.UserID, v.Cat, name, toggle(v.Mode, "fly", "base"), v.Var}},
			tab{"Ride", "amv_ride", ViewState{v.UserID, v.Cat, name, toggle(v.Mode, "ride", "base"), v.Var}},
		)
	}

	btns := make([]interface{}, 0, len(tabs))
	for _, t := range tabs {
		btns = append(btns, btn(t.label, t.emoji, style(t.vs.Mode == v.Mode && t.vs.Var == v.Var), t.vs.String()))
	}
	if v.Cat == "eggs" {
		return container(accent, append(parts, row(append(btns, search, swapBtn)...))...)
	}
	return container(accent, append(parts, row(btns...), row(search, swapBtn))...)
}
