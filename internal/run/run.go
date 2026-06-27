package run

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bromigos-org/pc-principal/internal/commands"
	"github.com/bromigos-org/pc-principal/internal/discordevent"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

const pcPrincipalAgentID = "pc-principal"

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
	dg.AddHandler(commands.VoiceStateUpdate)
	dg.AddHandler(commands.MessageReactionAdd)

	// debug // dg.Identify.Intents = discordgo.IntentsAll
	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsGuildVoiceStates | discordgo.IntentsGuildMessageReactions | discordgo.IntentMessageContent | discordgo.IntentsDirectMessages | discordgo.IntentsGuilds | discordgo.IntentGuildMessages | discordgo.IntentsGuildMembers | discordgo.IntentsGuildPresences

	// Open a websocket connection to Discord and begin listening.
	err = dg.Open()
	if err != nil {
		log.Printf("Error opening connection: %v\n", err)
		return
	}

	log.Println("Bot is now running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	dg.Close() // Cleanly close down the Discord session.
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
}

func ingestLiveMessage(message *discordgo.MessageCreate) {
	normalizer := discordevent.New(discordevent.Config{TenantID: liveMemoryTenantID, AgentID: pcPrincipalAgentID, SourceMarker: discordevent.SourceMarkerLive, ObservedAt: time.Now().UTC()})
	if _, err := liveMemory.IngestEvents(context.Background(), normalizer.NormalizeMessageCreate(message.Message)); err != nil {
		log.Printf("agents-memory live message ingest failed: %v", err)
	}
}

// onReady is called when the bot is ready to start receiving events.
func onReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Println("Bot is ready.")
	SetDiscordReady(true)
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
