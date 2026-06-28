package run

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bromigos-org/pc-principal/internal/ambient"
	"github.com/bromigos-org/pc-principal/internal/commands"
	"github.com/bromigos-org/pc-principal/internal/discordevent"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/preflight"
	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

const (
	pcPrincipalAgentID       = "pc-principal"
	liveMemoryEventBatchSize = 100
)

var (
	liveMemory         memory.Client = memory.NewClient(memory.Config{}, nil)
	liveMemoryTenantID               = "bromigos"
)

func Init() {
	_ = godotenv.Load()

	if addr := os.Getenv("DRAGONFLY_ADDR"); addr != "" {
		store.Init(addr)
		log.Printf("Connected to DragonflyDB at %s", addr)
	} else {
		log.Println("Warning: DRAGONFLY_ADDR not set — PC Principal thread and channel memory will not persist")
	}
	memoryConfig := memory.LoadConfigFromEnv()
	commands.ConfigureMemoryTenant(memoryConfig.TenantID)
	memoryClient := memory.NewClient(memoryConfig, nil)
	commands.ConfigureMemory(memoryClient)
	configureLiveMessageIngestion(memoryClient, memoryConfig.TenantID)
	configureAmbientReplies()

	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		log.Println("Warning: DISCORD_BOT_TOKEN not set in .env, ensure it's set in your environment")
		return
	}

	// Create a new Discord session using the provided bot token.
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Printf("Error creating Discord session: %v\n", err)
		return
	}

	// Register event handlers
	dg.AddHandler(onReady)
	dg.AddHandler(onDisconnect)
	dg.AddHandler(onReconnect)

	dg.AddHandler(liveMessageHandler)
	dg.AddHandler(liveChannelCreateHandler)
	dg.AddHandler(liveChannelUpdateHandler)
	dg.AddHandler(liveChannelDeleteHandler)
	dg.AddHandler(liveThreadCreateHandler)
	dg.AddHandler(liveThreadUpdateHandler)
	dg.AddHandler(liveThreadDeleteHandler)
	dg.AddHandler(liveRoleCreateHandler)
	dg.AddHandler(liveRoleUpdateHandler)
	dg.AddHandler(liveRoleDeleteHandler)
	dg.AddHandler(liveMemberUpdateHandler)
	dg.AddHandler(liveReactionAddHandler)
	dg.AddHandler(liveReactionRemoveHandler)
	dg.AddHandler(commands.VoiceStateUpdate)

	dg.Identify.Intents = discordIntents()

	// Open a websocket connection to Discord and begin listening.
	err = dg.Open()
	if err != nil {
		log.Printf("Error opening connection: %v\n", err)
		return
	}
	logPreflightWarnings(preflight.NewProbe(preflight.NewDiscordGoClient(dg), preflight.Config{Intents: dg.Identify.Intents}).Run(context.Background()))
	runHistoryBackfill(dg, memoryClient)

	log.Println("Bot is now running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	dg.Close() // Cleanly close down the Discord session.
}

func discordIntents() discordgo.Intent {
	return discordgo.IntentsGuildMessages | discordgo.IntentsGuildVoiceStates | discordgo.IntentsGuildMessageReactions | discordgo.IntentMessageContent | discordgo.IntentsDirectMessages | discordgo.IntentsGuilds | discordgo.IntentGuildMessages | discordgo.IntentsGuildMembers | discordgo.IntentsGuildPresences
}

func logPreflightWarnings(report preflight.Report) {
	for _, warning := range report.Warnings {
		log.Printf("Discord preflight warning: code=%s guild=%s channel=%s intent=%s permission=%s detail=%s", warning.Code, warning.GuildName, warning.Channel, warning.Intent, warning.Permission, warning.Detail)
	}
}

func configureLiveMessageIngestion(client memory.Client, tenantID string) {
	liveMemory = client
	if liveMemory == nil {
		liveMemory = memory.NewClient(memory.Config{}, nil)
	}
	if tenantID != "" {
		liveMemoryTenantID = tenantID
	}
}

func liveMessageHandler(s *discordgo.Session, message *discordgo.MessageCreate) {
	if message.Author == nil || message.Author.ID == s.State.User.ID || message.Author.Bot {
		return
	}
	ingestLiveMessage(message)
	commands.BotMention(s, message)
	commands.HeyThreadHandler(s, message)
	commands.VentAnonymously(s, message)
	commands.HandleThreadMessages(s, message)
	commands.AmbientReply(s, message)
}

func configureAmbientReplies() {
	config := ambient.LoadConfigFromEnv()
	if !config.Enabled {
		commands.ConfigureAmbient(nil)
		return
	}
	redisClient := store.Client()
	if redisClient == nil {
		log.Println("Discord ambient replies enabled but DRAGONFLY_ADDR is not configured; skipping")
		commands.ConfigureAmbient(nil)
		return
	}
	commands.ConfigureAmbient(ambient.NewManager(config, ambient.NewRedisStore(redisClient), nil))
}

func ingestLiveMessage(message *discordgo.MessageCreate) {
	normalizer := discordevent.New(discordevent.Config{TenantID: liveMemoryTenantID, AgentID: pcPrincipalAgentID, SourceMarker: discordevent.SourceMarkerLive, ObservedAt: time.Now().UTC()})
	if _, err := liveMemory.IngestEvents(context.Background(), normalizer.NormalizeMessageCreate(message.Message)); err != nil {
		log.Printf("agents-memory live message ingest failed: %v", err)
	}
}

func liveChannelCreateHandler(s *discordgo.Session, event *discordgo.ChannelCreate) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("channel create", normalizer.NormalizeChannelCreate(event.Channel))
}

func liveChannelUpdateHandler(s *discordgo.Session, event *discordgo.ChannelUpdate) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("channel update", normalizer.NormalizeChannelUpdate(event.Channel, event.BeforeUpdate))
}

func liveChannelDeleteHandler(s *discordgo.Session, event *discordgo.ChannelDelete) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("channel delete", normalizer.NormalizeChannelDelete(event.Channel))
}

func liveThreadCreateHandler(s *discordgo.Session, event *discordgo.ThreadCreate) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("thread create", normalizer.NormalizeThreadCreate(event.Channel))
}

func liveThreadUpdateHandler(s *discordgo.Session, event *discordgo.ThreadUpdate) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("thread update", normalizer.NormalizeThreadUpdate(event.Channel, event.BeforeUpdate))
}

func liveThreadDeleteHandler(s *discordgo.Session, event *discordgo.ThreadDelete) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("thread delete", normalizer.NormalizeThreadDelete(event.Channel))
}

func liveRoleCreateHandler(s *discordgo.Session, event *discordgo.GuildRoleCreate) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("role create", normalizer.NormalizeRoleCreate(event.GuildID, event.Role))
}

func liveRoleUpdateHandler(s *discordgo.Session, event *discordgo.GuildRoleUpdate) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("role update", normalizer.NormalizeRoleUpdate(event.GuildID, event.Role, event.BeforeUpdate))
}

func liveRoleDeleteHandler(s *discordgo.Session, event *discordgo.GuildRoleDelete) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("role delete", normalizer.NormalizeRoleDelete(event.GuildID, event.RoleID, event.BeforeDelete))
}

func liveMemberUpdateHandler(s *discordgo.Session, event *discordgo.GuildMemberUpdate) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("member update", normalizer.NormalizeMemberUpdateWithPrevious(event.Member, event.BeforeUpdate))
}

func liveReactionAddHandler(s *discordgo.Session, event *discordgo.MessageReactionAdd) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("reaction add", normalizer.NormalizeReactionAdd(event.MessageReaction, event.Member))
	commands.MessageReactionAdd(s, event)
}

func liveReactionRemoveHandler(s *discordgo.Session, event *discordgo.MessageReactionRemove) {
	normalizer := liveNormalizer(s, discordevent.SourceMarkerLive)
	ingestLiveTopology("reaction remove", normalizer.NormalizeReactionRemove(event.MessageReaction))
}

func liveNormalizer(s *discordgo.Session, source discordevent.SourceMarker) discordevent.Normalizer {
	return discordevent.New(discordevent.Config{TenantID: liveMemoryTenantID, AgentID: pcPrincipalAgentID, SourceMarker: source, ObservedAt: time.Now().UTC(), Snapshot: channelSnapshot(s)})
}

func channelSnapshot(s *discordgo.Session) discordevent.Snapshot {
	channels := map[string]*discordgo.Channel{}
	if s == nil || s.State == nil {
		return discordevent.Snapshot{Channels: channels}
	}
	for _, guild := range s.State.Guilds {
		for _, channel := range guild.Channels {
			channels[channel.ID] = channel
		}
		for _, thread := range guild.Threads {
			channels[thread.ID] = thread
		}
	}
	return discordevent.Snapshot{Channels: channels}
}

func ingestLiveTopology(label string, events []memory.ClientEvent) {
	if len(events) == 0 {
		return
	}
	for start := 0; start < len(events); start += liveMemoryEventBatchSize {
		end := start + liveMemoryEventBatchSize
		if end > len(events) {
			end = len(events)
		}
		if _, err := liveMemory.IngestEvents(context.Background(), events[start:end]); err != nil {
			log.Printf("agents-memory live %s ingest failed: %v", label, err)
		}
	}
}

// onReady is called when the bot is ready to start receiving events.
func onReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Println("Bot is ready.")
	SetDiscordReady(true)
	ingestStartupSnapshot(s, event)
}

// onDisconnect is called when the bot disconnects from Discord.
func onDisconnect(s *discordgo.Session, event *discordgo.Disconnect) {
	log.Println("Bot disconnected.")
	SetDiscordReady(false)
}

// onReconnect is called when the bot reconnects to Discord.
func onReconnect(s *discordgo.Session, event *discordgo.Connect) {
	log.Println("Bot reconnected.")
	SetDiscordReady(true)
}
