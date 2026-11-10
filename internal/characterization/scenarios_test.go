package characterization

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	rolesJSON   = `[{"id":"role-admin","name":"Admin"},{"id":"role-gamers","name":"Gamers"}]`
	membersJSON = `[{"user":{"id":"bot-1","username":"PC Principal","bot":true},"roles":[]},` +
		`{"user":{"id":"user-2","username":"alex"},"nick":"AlexTheGreat","roles":["role-gamers"]},` +
		`{"user":{"id":"user-3","username":"casey"},"roles":["role-admin"]}]`
	recentJSON = `[{"id":"m-9","author":{"id":"user-3","username":"casey"}},` +
		`{"id":"m-8","author":{"id":"bot-1","username":"PC Principal","bot":true}},` +
		`{"id":"m-7","author":{"id":"user-2","username":"alex"}},` +
		`{"id":"m-6","author":{"id":"user-3","username":"casey"}}]`
	emojisJSON = `{"items":[{"id":"emoji-1","name":"PC"}]}`

	memoryContextJSON = `{"sections":[` +
		`{"source":"long_term_facts","content":"blackflame likes expansive graph memory","facts":[{"kind":"preference","summary":"use combined context","weight":2}]},` +
		`{"source":"graph","memory_type":"graph","content":"Graph fact: #general discussed Dragonfly yesterday"}]}`
	shortTermContextJSON = `{"sections":[{"memory_type":"short_term","content":"Continue the current Discord conversation thread."}]}`
	skillsJSON           = `{"skills":[` +
		`{"skill_id":"skill-1","tenant_id":"bromigos","agent_id":"pc-principal","name":"Summarize channel","description":"Summarize visible Discord channel context for review.","status":"approved","scope":"agent_shared","metadata":{"reviewed":true}},` +
		`{"skill_id":"skill-2","tenant_id":"bromigos","agent_id":"pc-principal","name":"Draft shell helper","description":"Unreviewed proposal.","status":"proposed","scope":"agent_shared","metadata":{"reviewed":false}},` +
		`{"skill_id":"skill-3","tenant_id":"bromigos","agent_id":"pc-principal","name":"Approved but unreviewed","description":"Must stay out of the prompt.","status":"approved","scope":"agent_shared","metadata":{}}]}`
	channelActivityJSON = `{"sections":[{"source":"graph","facts":[` +
		`{"type":"channel_activity","rank":2,"user_display_name":"BlackDave","channel_name":"server-suggestion-box","message_count":4},` +
		`{"type":"channel_activity","rank":1,"user_display_name":"BlackDave","channel_name":"general-chat","message_count":247},` +
		`{"type":"channel_activity","rank":3,"user_display_name":"BlackDave","channel_name":"promos","message_count":1}]}]}`
)

// guildFixtures answers the lookups a guild conversation makes.
func (w *world) guildFixtures() {
	w.discord.routes["GET /applications/bot-1/emojis"] = emojisJSON
	w.discord.routes["GET /guilds/guild-1/roles"] = rolesJSON
	w.discord.routes["GET /guilds/guild-1/members"] = membersJSON
	w.discord.routes["GET /channels/channel-1/messages"] = recentJSON
	w.discord.routes["GET /guilds/guild-1"] = `{"id":"guild-1","name":"Bromigos"}`
	w.discord.routes["GET /channels/channel-1"] = `{"id":"channel-1","guild_id":"guild-1","name":"general","type":0}`
}

// guildState puts the guild and the channel in the session's cache, as they
// are once the gateway has delivered them.
func (w *world) guildState() {
	w.t.Helper()
	if err := w.session.State.GuildAdd(&discordgo.Guild{ID: "guild-1", Name: "Bromigos"}); err != nil {
		w.t.Fatalf("add guild state: %v", err)
	}
	if err := w.session.State.ChannelAdd(&discordgo.Channel{ID: "channel-1", GuildID: "guild-1", Name: "general"}); err != nil {
		w.t.Fatalf("add channel state: %v", err)
	}
}

func TestConversationInGuild(t *testing.T) {
	w := newWorld(t)
	w.guildFixtures()
	w.guildState()
	w.gnosis.responses["/v1/memory/context"] = memoryContextJSON
	w.gnosis.responses["/v1/skills"] = skillsJSON
	w.llm.queue("I'm PC, Texas A&M! You PC, bro?", "You said prove it, bro.", "Memory is fine, dude.")

	w.deliver("first turn, another member mentioned", w.message("<@bot-1> prove you're pc, <@user-2> doubts it", alsoMentions("user-2", "alex")))
	w.deliver("second turn, nickname mention form and a leading hey", w.message("<@!bot-1> hey what did I just say?"))
	w.deliver("third turn, the bot's mention comes later in the text", w.message("<@user-2> said memory was broken <@bot-1> hey what do you know?", alsoMentions("user-2", "alex")))

	w.check("conversation_in_guild")
}

func TestConversationServerLookup(t *testing.T) {
	w := newWorld(t)
	w.guildFixtures()
	w.gnosis.responses["/v1/memory/context"] = shortTermContextJSON
	w.llm.queue("Alex and Casey, bro.", "No idea, dude.")

	w.deliver("roster question, names looked up over REST", w.message("<@bot-1> who is in the server and what roles are there?", from("user-9", "newbie")))

	for _, key := range []string{"GET /guilds/guild-1/roles", "GET /guilds/guild-1/members", "GET /channels/channel-1/messages", "GET /guilds/guild-1", "GET /channels/channel-1"} {
		w.discord.fail[key] = true
	}
	w.deliver("roster question, every lookup failing", w.message("<@bot-1> who is in the server?"))

	w.check("conversation_server_lookup")
}

func TestConversationInDirectMessage(t *testing.T) {
	w := newWorld(t)
	w.discord.routes["GET /applications/bot-1/emojis"] = emojisJSON
	w.discord.routes["GET /channels/dm-1"] = `{"id":"dm-1","type":1}`
	w.discord.routes["GET /channels/dm-1/messages"] = `[]`
	w.gnosis.responses["/v1/memory/context"] = memoryContextJSON
	w.llm.queue("Sup, bro.", "Still here, bro.")

	w.deliver("first turn in a DM", w.message("<@bot-1> you there?", directMessage("dm-1")))
	w.deliver("second turn in a DM", w.message("<@bot-1> hey still there?", directMessage("dm-1")))

	w.check("conversation_in_dm")
}

func TestMentionEdgeCases(t *testing.T) {
	w := newWorld(t)
	w.guildFixtures()
	w.guildState()

	w.deliver("mention with nothing after it", w.message("<@bot-1>"))
	w.deliver("mention followed only by hey", w.message("<@bot-1> hey"))
	w.deliver("reply that pings the bot without its mention in the text", w.message("tell me more", replyPingingBot()))
	w.deliver("one-word reply that pings the bot", w.message("why?", replyPingingBot()))
	w.deliver("no mention at all", w.message("quiet channel chatter"))
	w.deliver("message from another bot", w.message("<@bot-1> beep", func(m *discordgo.Message) {
		m.Author = &discordgo.User{ID: "bot-2", Username: "Other Bot", Bot: true}
	}))
	w.deliver("message from the bot itself", w.message("<@bot-1> echo", func(m *discordgo.Message) {
		m.Author = &discordgo.User{ID: botID, Username: "PC Principal", Bot: true}
	}))

	w.check("mention_edge_cases")
}

func TestMentionCommands(t *testing.T) {
	w := newWorld(t)
	w.guildFixtures()
	w.guildState()
	w.discord.routes["POST /users/@me/channels"] = `{"id":"dm-9","type":1}`
	w.llm.queue("Pong is a command, bro.")

	w.deliver("ping", w.message("<@bot-1> ping"))
	w.deliver("ping in capitals with trailing words", w.message("<@bot-1> PING right now"))
	w.deliver("command word second, the bot's mention last", w.message("<@user-2> ping <@bot-1>", alsoMentions("user-2", "alex")))
	w.deliver("hey before a command word is conversation", w.message("<@bot-1> hey ping"))
	w.deliver("help", w.message("<@bot-1> help"))
	w.deliver("mdelete without a number", w.message("<@bot-1> mdelete"))
	w.deliver("mpost without a channel", w.message("<@bot-1> mpost hello there"))

	w.discord.fail["POST /channels/dm-9/messages"] = true
	w.deliver("help when the DM cannot be sent", w.message("<@bot-1> help"))
	w.discord.fail["POST /users/@me/channels"] = true
	w.deliver("help when the DM channel cannot be opened", w.message("<@bot-1> help"))

	w.discord.fail["GET /applications/bot-1/emojis"] = true
	w.discord.routes["GET /guilds/guild-1/emojis"] = `[{"id":"emoji-2","name":"pc"}]`
	w.deliver("ping when only the guild has the PC emoji", w.message("<@bot-1> ping"))

	w.check("mention_commands")
}

func TestChannelActivityAnswer(t *testing.T) {
	w := newWorld(t)
	w.guildFixtures()
	w.guildState()
	w.gnosis.responses["/v1/memory/context"] = channelActivityJSON

	w.deliver("answered from graph facts, no model call", w.message("<@bot-1> hey what are @BlackDave top 5 most active channels??"))

	w.gnosis.fail["/v1/messages"] = true
	w.deliver("answered from graph facts, write-back failing", w.message("<@bot-1> again please"))
	delete(w.gnosis.fail, "/v1/messages")

	w.discord.fail["POST /channels/channel-1/messages"] = true
	w.deliver("answered from graph facts, Discord send failing", w.message("<@bot-1> once more"))
	delete(w.discord.fail, "POST /channels/channel-1/messages")

	w.redis.setFailSet(true)
	w.deliver("answered from graph facts, history save failing", w.message("<@bot-1> and again"))

	w.check("channel_activity_answer")
}

func TestConversationFailures(t *testing.T) {
	w := newWorld(t)
	w.guildFixtures()
	w.guildState()
	w.gnosis.responses["/v1/memory/context"] = memoryContextJSON
	w.gnosis.responses["/v1/skills"] = skillsJSON

	w.llm.queueFailure()
	w.deliver("model call fails", w.message("<@bot-1> hidden note marker"))

	w.llm.queue("Still PC, bro.")
	w.gnosis.fail["/v1/memory/context"] = true
	w.gnosis.fail["/v1/skills"] = true
	w.gnosis.fail["/v1/messages"] = true
	w.deliver("recall, skills and write-back fail", w.message("<@bot-1> keep talking"))
	w.gnosis.fail = map[string]bool{}

	w.llm.queue("Untraced, bro.")
	w.gnosis.fail["/v1/reasoning/traces"] = true
	w.deliver("reasoning trace cannot start", w.message("<@bot-1> no trace"))
	w.gnosis.fail = map[string]bool{}

	w.llm.queue("Half traced, bro.")
	w.gnosis.fail["/v1/reasoning/traces/*"] = true
	w.gnosis.fail["/v1/reasoning/steps/*"] = true
	w.deliver("reasoning steps and completion fail", w.message("<@bot-1> half a trace"))
	w.gnosis.fail = map[string]bool{}

	w.llm.queue("You will not see this, bro.")
	w.discord.fail["POST /channels/channel-1/messages"] = true
	w.deliver("Discord send fails", w.message("<@bot-1> answer safely"))
	delete(w.discord.fail, "POST /channels/channel-1/messages")

	w.llm.queue("Not saved, bro.")
	w.redis.setFailSet(true)
	w.deliver("history save fails", w.message("<@bot-1> remember this"))

	w.check("conversation_failures")
}

func TestChatThread(t *testing.T) {
	w := newWorld(t)
	w.guildFixtures()
	w.guildState()
	w.llm.queue("Kubernetes is sweet, bro.", "Cilium rocks, dude.", "Calico too, bro.", "Calico too, in the thread, bro.")

	w.deliver("chat with a topic starts a thread", w.message("<@bot-1> chat kubernetes networking"))
	w.deliver("message in the thread without a mention", w.message("what about cilium?", inChannel("thread-1"), from("user-2", "alex", "role-gamers")))
	w.deliver("mention inside the thread: both the mention and the thread handler answer", w.message("<@bot-1> and calico?", inChannel("thread-1")))
	w.deliver("chat without a topic", w.message("<@bot-1> chat"))

	w.llm.queueFailure()
	w.deliver("thread follow-up when the model call fails", w.message("still there?", inChannel("thread-1")))

	w.llm.queueFailure()
	w.deliver("chat when the model call fails", w.message("<@bot-1> chat helm charts"))

	w.discord.fail["POST /channels/channel-1/messages/message-7/threads"] = true
	w.deliver("chat when the thread cannot be created", w.message("<@bot-1> chat istio"))

	w.llm.queue("Saved nowhere, bro.", "Thread reply that cannot be sent, bro.")
	w.redis.setFailSet(true)
	w.deliver("chat when the history cannot be saved", w.message("<@bot-1> chat argo"))
	w.discord.fail["POST /channels/thread-1/messages"] = true
	w.deliver("thread follow-up when neither save nor send works", w.message("hello?", inChannel("thread-1")))

	w.check("chat_thread")
}

func TestAmbientSession(t *testing.T) {
	w := newWorld(t)
	w.guildFixtures()
	w.guildState()
	w.enableAmbient()

	w.llm.queue("I'm PC, Texas A&M!")
	w.deliver("mention conversation starts the session", w.message("<@bot-1> are you pc?"))
	w.clock.advance(2 * time.Minute)
	w.llm.queue("Sweet, bro.")
	w.deliver("same member, no mention: ambient reply", w.message("I am also PC"))
	w.deliver("same member again at once: cooldown", w.message("and you?"))
	w.clock.advance(2 * time.Minute)
	w.deliver("another member, no mention: ignored", w.message("me too", from("user-3", "casey", "role-admin")))
	w.deliver("command while the session is active", w.message("<@bot-1> ping"))
	w.deliver("blank message while the session is active", w.message("   "))
	w.llm.queue("Casey gets it.")
	w.deliver("another member replying to the bot: ambient reply", w.message("for real though", from("user-3", "casey", "role-admin"), replyingToBot()))
	w.clock.advance(2 * time.Minute)
	w.deliver("two replies in a row end the session", w.message("more?"))
	w.deliver("no session: silence", w.message("anyone?"))

	w.llm.queue("Back again, bro.")
	w.deliver("mention conversation starts a second session", w.message("<@bot-1> back again"))
	w.deliver("mention asking to stop ends the session without a reply", w.message("<@bot-1> please stop"))

	w.llm.queue("One more, bro.")
	w.deliver("mention conversation starts a third session", w.message("<@bot-1> one more"))
	w.clock.advance(2 * time.Minute)
	w.llm.queueFailure()
	w.deliver("ambient reply when the model call fails", w.message("you good?"))
	w.deliver("plain stop ends the session", w.message("Stop."))

	w.llm.queue("One.")
	w.deliver("mention conversation starts a fourth session", w.message("<@bot-1> one"))
	w.clock.advance(2 * time.Minute)
	w.llm.queue("Two.")
	w.deliver("ambient reply", w.message("two"))
	w.clock.advance(21 * time.Minute)
	w.deliver("session expired", w.message("three"))

	w.check("ambient_session")
}

func TestLongAndEmptyReplies(t *testing.T) {
	w := newWorld(t)
	w.guildFixtures()
	w.guildState()
	w.llm.queue(strings.Repeat("é", 2500), "   \n ")

	w.deliver("reply longer than one Discord message, cut between characters", w.message("<@bot-1> say a lot"))
	w.deliver("blank reply", w.message("<@bot-1> say nothing"))

	w.check("long_and_empty_replies")
}
