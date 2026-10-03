package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	colErr  = 0xE74C3C
	colOK   = 0x2ECC71
	colGray = 0x4E5058
	// 32768 = IS_COMPONENTS_V2, nothing here sends a real embed
	v2Flags = 32768
	hidden  = 64 // ephemeral
	// guild we rename to and push updates from
	homeGuild = "1525581720517345300"
	// nobody is guessing this name so its also a free admin check
	webhookCmd  = "sativaneedsmoneypleasesendmemone"
	webhookFile = "webhook.json"
)

func loadEnv() {
	f, err := os.Open(".env")
	if err != nil {
		if exe, err := os.Executable(); err == nil {
			f, err = os.Open(filepath.Join(filepath.Dir(exe), ".env"))
		}
	}
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if kv := strings.SplitN(l, "=", 2); len(kv) == 2 {
			k := strings.TrimSpace(kv[0])
			if os.Getenv(k) == "" {
				os.Setenv(k, strings.Trim(strings.TrimSpace(kv[1]), "\"'"))
			}
		}
	}
}

func targetGuild() string {
	if id := os.Getenv("TARGET_GUILD"); id != "" {
		return id
	}
	return homeGuild
}

func emojiGuild() string {
	if id := os.Getenv("EMOJI_GUILD"); id != "" {
		return id
	}
	return os.Getenv("EMOJI_GUILD_ID")
}

// cache first, api on a miss
func guildInfo(s *discordgo.Session, id string) (string, string) {
	if id == "" {
		return "", ""
	}
	g, err := s.State.Guild(id)
	if err != nil || g == nil {
		if g, err = s.Guild(id); err != nil || g == nil {
			return "", ""
		}
	}
	return g.Name, g.IconURL("512")
}

func userID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func isAdmin(s *discordgo.Session, guildID, id string) bool {
	g, err := s.State.Guild(guildID)
	if err != nil || g == nil || id == "" {
		return false
	}
	if g.OwnerID == id {
		return true
	}
	m, err := s.GuildMember(guildID, id)
	if err != nil {
		return false
	}
	for _, rid := range m.Roles {
		for _, r := range g.Roles {
			if r.ID == rid && r.Permissions&discordgo.PermissionAdministrator != 0 {
				return true
			}
		}
	}
	return false
}

type WebhookManager struct {
	sync.RWMutex
	ID, Token, GuildID, Channel string
}

var ActiveWebhook = &WebhookManager{}

func (wm *WebhookManager) Load() {
	if b, err := os.ReadFile(webhookFile); err == nil {
		wm.Lock()
		_ = json.Unmarshal(b, wm)
		wm.Unlock()
	}
}

func (wm *WebhookManager) Save() {
	wm.RLock()
	b, _ := json.Marshal(wm)
	wm.RUnlock()
	_ = os.WriteFile(webhookFile, b, 0644)
}

func (wm *WebhookManager) Send(s *discordgo.Session, comps []discordgo.MessageComponent) error {
	wm.RLock()
	id, tok := wm.ID, wm.Token
	wm.RUnlock()
	if id == "" || tok == "" {
		return fmt.Errorf("no webhook configured")
	}
	name, icon := guildInfo(s, wm.GuildID)
	_, err := s.WebhookExecute(id, tok, false, &discordgo.WebhookParams{
		Username: name, AvatarURL: icon, Flags: v2Flags, Components: comps,
	})
	return err
}

func pushUpdate(s *discordgo.Session) error {
	name, icon := guildInfo(s, targetGuild())
	return ActiveWebhook.Send(s, DB.BuildLatestSummaryV2(name, icon))
}

func reply(s *discordgo.Session, i *discordgo.InteractionCreate, kind discordgo.InteractionResponseType, comps []discordgo.MessageComponent, flags discordgo.MessageFlags) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: kind,
		Data: &discordgo.InteractionResponseData{Flags: flags, Components: comps},
	})
}

func show(s *discordgo.Session, i *discordgo.InteractionCreate, comps []discordgo.MessageComponent) {
	reply(s, i, discordgo.InteractionResponseChannelMessageWithSource, comps, v2Flags)
}

func edit(s *discordgo.Session, i *discordgo.InteractionCreate, comps []discordgo.MessageComponent) {
	reply(s, i, discordgo.InteractionResponseUpdateMessage, comps, v2Flags)
}

func say(s *discordgo.Session, i *discordgo.InteractionCreate, title, desc string, color int, hide bool) {
	flags := discordgo.MessageFlags(v2Flags)
	if hide {
		flags |= hidden
	}
	reply(s, i, discordgo.InteractionResponseChannelMessageWithSource,
		container(color, text("## "+title+"\n"+desc)), flags)
}

func post(s *discordgo.Session, channelID, title, desc string) {
	_, _ = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Flags: v2Flags, Components: container(colGray, text("## "+title+"\n"+desc)),
	})
}

func deny(s *discordgo.Session, i *discordgo.InteractionCreate) {
	say(s, i, "Access Denied", "Only administrators can use this command.", colErr, true)
}

func notYours(s *discordgo.Session, i *discordgo.InteractionCreate) {
	say(s, i, "Access Denied", "This is not your embed.", colErr, true)
}

func send(s *discordgo.Session, channelID string, comps []discordgo.MessageComponent) {
	_, _ = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Flags: v2Flags, Components: comps})
}

func ask(s *discordgo.Session, i *discordgo.InteractionCreate, customID, title, placeholder string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: customID,
			Title:    title,
			Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.TextInput{
					CustomID: "query", Label: "Pet or Egg Name", Style: discordgo.TextInputShort,
					Placeholder: placeholder, Required: true, MaxLength: 50,
				},
			}}},
		},
	})
}

func dataURI(url string) string {
	if url == "" {
		return ""
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		return ""
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return "data:" + http.DetectContentType(b) + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func setupWebhook(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !isAdmin(s, i.GuildID, userID(i)) {
		deny(s, i)
		return
	}

	channel, name, avatar := "", "sativa", ""
	for _, o := range i.ApplicationCommandData().Options {
		switch o.Name {
		case "channel":
			channel = o.ChannelValue(s).ID
		case "name":
			name = o.StringValue()
		case "avatar_url":
			avatar = o.StringValue()
		}
	}
	if channel == "" {
		say(s, i, "Error", "Missing target channel.", colErr, true)
		return
	}

	if hooks, err := s.GuildWebhooks(i.GuildID); err == nil {
		for _, h := range hooks {
			if h.User != nil && h.User.ID == s.State.User.ID {
				_ = s.WebhookDelete(h.ID)
			}
		}
	}

	created, err := s.WebhookCreate(channel, name, dataURI(avatar))
	if err != nil {
		say(s, i, "Error", fmt.Sprintf("Failed to create webhook: %v", err), colErr, true)
		return
	}

	ActiveWebhook.Lock()
	ActiveWebhook.ID, ActiveWebhook.Token = created.ID, created.Token
	ActiveWebhook.GuildID, ActiveWebhook.Channel = i.GuildID, channel
	ActiveWebhook.Unlock()
	ActiveWebhook.Save()

	_ = pushUpdate(s)
	say(s, i, "Webhook Configured", fmt.Sprintf("Created `%s` in <#%s> and sent initial value update.", created.Name, channel), colOK, true)
}

func initEmojis(s *discordgo.Session, guild string) {
	if guild == "" {
		return
	}
	if err := Emojis.Init(s, guild); err != nil {
		log.Printf("emojis bad: %v", err)
		return
	}
	log.Println("emojis good")
}

// makes bot look like server so server rbanded ig
func syncNick(s *discordgo.Session) {
	guild := targetGuild()
	if g, err := s.Guild(guild); err == nil && g != nil && g.Name != "" {
		_ = s.GuildMemberNickname(guild, "@me", g.Name)
	}
}

func onReady(s *discordgo.Session, r *discordgo.Ready) {
	log.Println("bot good")

	guild := emojiGuild()
	if guild == "" && len(r.Guilds) > 0 {
		guild = r.Guilds[0].ID
	}
	initEmojis(s, guild)
	syncNick(s)

	if ActiveWebhook.ID != "" {
		return
	}
	hooks, err := s.GuildWebhooks(targetGuild())
	if err != nil {
		return
	}
	for _, h := range hooks {
		if h.User == nil || h.User.ID != s.State.User.ID {
			continue
		}
		ActiveWebhook.Lock()
		ActiveWebhook.ID, ActiveWebhook.Token = h.ID, h.Token
		ActiveWebhook.GuildID, ActiveWebhook.Channel = targetGuild(), h.ChannelID
		ActiveWebhook.Unlock()
		ActiveWebhook.Save()
		break
	}
}

func onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}

	body := strings.TrimSpace(m.Content)
	lower := strings.ToLower(body)
	cmd, arg := "", ""
	for _, c := range []string{"values", "compare", "sendupdate"} {
		for _, p := range []string{"!", ".", ";", "$"} {
			if strings.HasPrefix(lower, p+c) {
				cmd, arg = c, strings.TrimSpace(body[len(p)+len(c):])
			}
		}
		if cmd != "" {
			break
		}
	}
	if cmd == "" || !Limiter.Allow(m.Author.ID) {
		return
	}

	guild, _ := guildInfo(s, m.GuildID)
	switch cmd {
	case "sendupdate":
		if !isAdmin(s, m.GuildID, m.Author.ID) {
			post(s, m.ChannelID, "Access Denied", "Only administrators can use this command.")
			return
		}
		if err := pushUpdate(s); err != nil {
			post(s, m.ChannelID, "Error", fmt.Sprintf("Failed to send webhook update: %v", err))
			return
		}
		post(s, m.ChannelID, "Update Sent", "Value updates broadcast to webhook channel successfully.")

	case "values":
		vs := ViewState{UserID: m.Author.ID, Cat: "pets", Name: "Bat Dragon", Mode: "base", Var: "reg"}
		if p := DB.FindPet(arg); p != nil {
			vs.Name = p.Name
		} else if e := DB.FindEgg(arg); e != nil {
			vs.Cat, vs.Name = "eggs", e.Name
		}
		send(s, m.ChannelID, RenderV2(vs, guild))

	case "compare":
		send(s, m.ChannelID, RenderCompareV2(Compare.Get(m.Author.ID), guild))
	}
}

func onCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	uid := userID(i)
	guild, _ := guildInfo(s, i.GuildID)

	switch i.ApplicationCommandData().Name {
	case "values":
		vs := ViewState{UserID: uid, Cat: "pets", Name: "Bat Dragon", Mode: "base", Var: "reg"}
		show(s, i, RenderV2(vs, guild))

	case "compare":
		show(s, i, RenderCompareV2(Compare.Get(uid), guild))

	case "yo":
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("yo : <@%s> %dms", uid, s.HeartbeatLatency().Milliseconds()),
			},
		})

	case "sendupdate":
		if !isAdmin(s, i.GuildID, uid) {
			deny(s, i)
			return
		}
		if err := pushUpdate(s); err != nil {
			say(s, i, "Error", fmt.Sprintf("Failed to send webhook update: %v", err), colErr, true)
			return
		}
		say(s, i, "Update Sent", "Value updates broadcast to webhook channel successfully.", colOK, true)

	case webhookCmd:
		setupWebhook(s, i)
	}
}

func onCompareButton(s *discordgo.Session, i *discordgo.InteractionCreate, uid, guild string, p []string, data discordgo.MessageComponentInteractionData) {
	if owner := arg(p, len(p)-1, ""); owner != "" && owner != uid {
		notYours(s, i)
		return
	}

	sess := Compare.Get(uid)
	action := arg(p, 1, "")
	if action == "search" {
		ask(s, i, "modal:cmp:"+sess.ID,
			fmt.Sprintf("Add to Side %d (%s)", sess.ActiveSide, sess.Variant),
			"e.g. Bat Dragon, Frost, Owl, Safari Egg...")
		return
	}
	if action == "sel" && len(data.Values) > 0 {
		sess.Add(data.Values[0])
	}

	switch action {
	case "side":
		if n, err := strconv.Atoi(arg(p, 2, "1")); err == nil {
			sess.ActiveSide = n
		}
	case "var":
		sess.Variant = arg(p, 2, "reg")
	case "cat":
		sess.Cat = toggle(sess.Cat, "eggs", "pets")
	case "clear":
		if sess.ActiveSide == 1 {
			sess.Side1 = nil
		} else {
			sess.Side2 = nil
		}
	case "reset":
		sess.Side1, sess.Side2 = nil, nil
	}
	edit(s, i, RenderCompareV2(sess, guild))
}

func onViewButton(s *discordgo.Session, i *discordgo.InteractionCreate, uid, guild, id string, p []string, data discordgo.MessageComponentInteractionData) {
	switch {
	case p[0] == "sel":
		owner := arg(p, 1, "")
		if owner != "" && owner != uid {
			notYours(s, i)
			return
		}
		vs := ViewState{owner, arg(p, 2, "pets"), data.Values[0], arg(p, 4, "base"), arg(p, 5, "reg")}
		edit(s, i, RenderV2(vs, guild))

	case len(p) > 2 && p[1] == "search":
		vs := parseCustomID(strings.Join(p[2:], ":"))
		if vs.UserID != "" && vs.UserID != uid {
			notYours(s, i)
			return
		}
		ask(s, i, "modal:v:"+vs.String(), "Search Adopt Me Values",
			"e.g. Bat Dragon, Shadow, Frost, Safari Egg...")

	case p[0] == "v":
		vs := parseCustomID(id)
		if vs.UserID != "" && vs.UserID != uid {
			notYours(s, i)
			return
		}
		edit(s, i, RenderV2(vs, guild))
	}
}

func onComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	uid := userID(i)
	guild, _ := guildInfo(s, i.GuildID)

	data := i.MessageComponentData()
	p := strings.Split(data.CustomID, ":")
	switch {
	case p[0] == "cmp":
		onCompareButton(s, i, uid, guild, p, data)
	case p[0] == "sel", p[0] == "v":
		onViewButton(s, i, uid, guild, data.CustomID, p, data)
	}
}

func onModal(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guild, _ := guildInfo(s, i.GuildID)
	data := i.ModalSubmitData()

	q := ""
	for _, r := range data.Components {
		if row, ok := r.(*discordgo.ActionsRow); ok {
			for _, c := range row.Components {
				if ti, ok := c.(*discordgo.TextInput); ok && ti.CustomID == "query" {
					q = strings.TrimSpace(ti.Value)
				}
			}
		}
	}

	p, e := DB.Search(q)
	if p == nil && e == nil {
		say(s, i, "Not Found", fmt.Sprintf("No pet or egg found matching `%s`.", q), colErr, true)
		return
	}

	switch {
	case strings.HasPrefix(data.CustomID, "modal:v:"):
		vs := parseCustomID(strings.TrimPrefix(data.CustomID, "modal:v:"))
		if p != nil {
			vs.Cat, vs.Name = "pets", p.Name
		} else {
			vs.Cat, vs.Name = "eggs", e.Name
		}
		edit(s, i, RenderV2(vs, guild))

	case strings.HasPrefix(data.CustomID, "modal:cmp:"):
		sess := Compare.Get(userID(i))
		if p != nil {
			sess.Cat = "pets"
			sess.Add(p.Name)
		} else {
			sess.Cat = "eggs"
			sess.Add(e.Name)
		}
		edit(s, i, RenderCompareV2(sess, guild))
	}
}

func onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !Limiter.Allow(userID(i)) {
		say(s, i, "Slow Down", "Please chill and wait a few seconds before trying again.", colErr, true)
		return
	}
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		onCommand(s, i)
	case discordgo.InteractionMessageComponent:
		onComponent(s, i)
	case discordgo.InteractionModalSubmit:
		onModal(s, i)
	}
}

func register(dg *discordgo.Session) {
	admin := int64(discordgo.PermissionAdministrator)
	cmds := []*discordgo.ApplicationCommand{
		{Name: "values", Description: "Check Adopt Me values"},
		{Name: "compare", Description: "Compare values in a visual trade"},
		{Name: "yo", Description: "yo"},
		{Name: "sendupdate", Description: "Broadcast value updates to webhook (Admin only)", DefaultMemberPermissions: &admin},
		{
			Name: webhookCmd, Description: "Configure webhook updates to a channel (Admin only)",
			DefaultMemberPermissions: &admin,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Target channel for webhook", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "name", Description: "Webhook name (optional)"},
				{Type: discordgo.ApplicationCommandOptionString, Name: "avatar_url", Description: "Webhook avatar URL (optional)"},
			},
		},
	}

	app := dg.State.User.ID
	guilds, _ := dg.UserGuilds(100, "", "", false)
	for _, g := range guilds {
		if _, err := dg.ApplicationCommandBulkOverwrite(app, g.ID, cmds); err != nil {
			log.Printf("slash bad (%s): %v", g.Name, err)
		}
	}
	_, _ = dg.ApplicationCommandBulkOverwrite(app, "", nil)
	log.Println("slash good")
}

func main() {
	loadEnv()
	token := os.Getenv("TOKEN")
	if token == "" {
		token = os.Getenv("DISCORD_TOKEN")
	}
	if token == "" {
		token = os.Getenv("BOT_TOKEN")
	}
	if token == "" {
		log.Fatal("very bad: missing TOKEN in .env")
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatalf("very bad: session: %v", err)
	}
	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentMessageContent

	ActiveWebhook.Load()
	DB.OnUpdate = func(diffs []ValueDiff) {
		guild, icon := guildInfo(dg, targetGuild())
		for _, batch := range DB.FormatDiffBatchesV2(diffs, guild, icon) {
			_ = ActiveWebhook.Send(dg, batch)
			// one message per item now so a big dump takes a bit.
			// 750ms keeps it under the webhook rate limit
			time.Sleep(750 * time.Millisecond)
		}
	}

	dg.AddHandler(onReady)
	dg.AddHandler(onMessage)
	dg.AddHandler(onInteraction)

	// basically what big companies do to people they wanna get rid of or the govermet
	DB.StartWatcher(time.Minute)
	go func() {
		for range time.Tick(5 * time.Minute) {
			Limiter.Clean()
			Compare.Clean()
			syncNick(dg)
		}
	}()

	if err := dg.Open(); err != nil {
		log.Fatalf("very bad: open: %v", err)
	}
	defer dg.Close()
	register(dg)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-quit
}
