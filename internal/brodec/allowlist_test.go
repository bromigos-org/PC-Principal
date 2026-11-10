package brodec

import (
	"reflect"
	"testing"
	"time"
)

const strangersRoom = "!xYzAbCdEfGhIjKlMnO:brodec.ai"

// The bot joins every room it is invited to, and anyone can message it.
// Being there is not being allowed to spend the model there: off the
// allowlists the bot says one fixed line, at most once a day per room.

func TestRoom_off_the_allowlist_gets_the_fixed_line_once_a_day_and_no_model_call(t *testing.T) {
	// Given: a mention, a command and another mention in a room nobody
	// allowed
	h := newHarness(t)
	mention := edited(t, "delivery/mention.json", map[string]any{"room_id": strangersRoom})
	command := edited(t, "delivery/command.json", map[string]any{"room_id": strangersRoom})
	again := edited(t, "delivery/mention.json", map[string]any{"room_id": strangersRoom, "event_id": "$mention-again"})

	// When
	first := h.deliver(mention)
	second := h.deliver(command)
	third := h.deliver(again)

	// Then: the line once, inline, and silence after it
	if want := []Action{{Type: "reply", Text: notAllowedLine}}; !reflect.DeepEqual(first, want) {
		t.Fatalf("expected %v, got %v", want, first)
	}
	if len(second) != 0 || len(third) != 0 {
		t.Fatalf("expected silence after the first line, got %v and %v", second, third)
	}
	if h.llm.calls() != 0 {
		t.Fatalf("expected no model call, got %d", h.llm.calls())
	}
	if len(h.gateway.localCalls()) != 0 {
		t.Fatal("expected nothing through the local API")
	}
	// and nothing is kept but the mark that the line was said
	if keys := h.keys(); !reflect.DeepEqual(keys, []string{"brodec:notice:" + strangersRoom}) {
		t.Fatalf("expected only the notice mark in the store, got %v", keys)
	}

	// When: a day has passed
	h.redis.FastForward(24*time.Hour + time.Second)
	nextDay := h.deliver(edited(t, "delivery/mention.json", map[string]any{"room_id": strangersRoom, "event_id": "$mention-next-day"}))

	// Then
	if want := []Action{{Type: "reply", Text: notAllowedLine}}; !reflect.DeepEqual(nextDay, want) {
		t.Fatalf("expected the line again a day later, got %v", nextDay)
	}
	if h.llm.calls() != 0 {
		t.Fatal("expected no model call a day later either")
	}
}

func TestDirectMessage_from_someone_off_the_list_gets_the_fixed_line_and_no_model_call(t *testing.T) {
	// Given
	h := newHarness(t)
	fromAStranger := edited(t, "delivery/message.json", map[string]any{"sender": otherSender})
	again := edited(t, "delivery/message.json", map[string]any{"sender": otherSender, "event_id": "$dm-2"})

	// When
	first := h.deliver(fromAStranger)
	second := h.deliver(again)

	// Then
	if want := []Action{{Type: "reply", Text: notAllowedLine}}; !reflect.DeepEqual(first, want) {
		t.Fatalf("expected %v, got %v", want, first)
	}
	if len(second) != 0 {
		t.Fatalf("expected silence after the first line, got %v", second)
	}
	if h.llm.calls() != 0 || len(h.gateway.localCalls()) != 0 {
		t.Fatal("expected no model call and nothing through the local API")
	}
}

func TestStore_that_is_down_means_silence_off_the_allowlist(t *testing.T) {
	// Given: no way to tell whether the line was said today
	h := newHarness(t)
	h.redis.Close()

	// When
	inline := h.deliver(edited(t, "delivery/mention.json", map[string]any{"room_id": strangersRoom}))

	// Then
	if len(inline) != 0 || h.llm.calls() != 0 {
		t.Fatalf("expected silence, got %v", inline)
	}
}

func TestTap_from_a_place_off_the_allowlists_is_refused_privately(t *testing.T) {
	// Given: a tap in a room nobody allowed, by someone nobody allowed
	h := newHarness(t)
	tap := edited(t, "delivery/button.json", map[string]any{"room_id": strangersRoom, "sender": otherSender, "button_id": "help:privacy"})

	// When
	inline := h.deliver(tap)

	// Then: only the person who tapped sees the line, so the room is not
	// charged for it
	if want := []Action{{Type: "fail", Text: notAllowedLine}}; !reflect.DeepEqual(inline, want) {
		t.Fatalf("expected %v, got %v", want, inline)
	}
	if len(h.keys()) != 0 {
		t.Fatalf("expected nothing kept, got %v", h.keys())
	}
}

func TestAllowlist_decides_by_room_in_a_group_and_by_person_in_a_direct_message(t *testing.T) {
	list := newAllowlist(Config{
		AllowedRooms:   []string{"!group:brodec.ai"},
		AllowedDMUsers: []string{"@friend:brodec.ai"},
		Admins:         []string{"@admin:brodec.ai"},
	})
	cases := map[string]struct {
		delivery Delivery
		want     bool
	}{
		"a mention in a listed room, by anyone":            {Delivery{Type: "mention", RoomID: "!group:brodec.ai", Sender: "@anyone:brodec.ai", IsGroup: true}, true},
		"a command in a listed room":                       {Delivery{Type: "command", RoomID: "!group:brodec.ai", Sender: "@anyone:brodec.ai", IsGroup: true}, true},
		"a mention in another room, by a listed person":    {Delivery{Type: "mention", RoomID: "!other:brodec.ai", Sender: "@friend:brodec.ai", IsGroup: true}, false},
		"a mention in another room, by an admin":           {Delivery{Type: "mention", RoomID: "!other:brodec.ai", Sender: "@admin:brodec.ai", IsGroup: true}, false},
		"a direct message from a listed person":            {Delivery{Type: "message", RoomID: "!dm:brodec.ai", Sender: "@friend:brodec.ai"}, true},
		"a direct message from an admin":                   {Delivery{Type: "message", RoomID: "!dm:brodec.ai", Sender: "@admin:brodec.ai"}, true},
		"a direct message from anyone else":                {Delivery{Type: "message", RoomID: "!dm:brodec.ai", Sender: "@anyone:brodec.ai"}, false},
		"a command in a direct message from anyone else":   {Delivery{Type: "command", RoomID: "!dm:brodec.ai", Sender: "@anyone:brodec.ai"}, false},
		"a direct message in a room with a listed room id": {Delivery{Type: "message", RoomID: "!group:brodec.ai", Sender: "@anyone:brodec.ai"}, false},
		"a tap in a listed room":                           {Delivery{Type: "button", RoomID: "!group:brodec.ai", Sender: "@anyone:brodec.ai"}, true},
		"a tap by a listed person":                         {Delivery{Type: "button", RoomID: "!dm:brodec.ai", Sender: "@friend:brodec.ai"}, true},
		"a tap by anyone else, elsewhere":                  {Delivery{Type: "button", RoomID: "!other:brodec.ai", Sender: "@anyone:brodec.ai"}, false},
		"a ping":                                           {Delivery{Type: "ping"}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := list.allows(c.delivery); got != c.want {
				t.Fatalf("expected %v, got %v", c.want, got)
			}
		})
	}
	if !list.isAdmin("@admin:brodec.ai") || list.isAdmin("@friend:brodec.ai") {
		t.Fatal("only the people on the admin list are admins")
	}
}

func TestAllowlists_that_are_empty_allow_nobody(t *testing.T) {
	// Given: a bot nobody has allowed anywhere yet
	h := newHarness(t, withConfig(func(c *Config) {
		c.AllowedRooms, c.AllowedDMUsers, c.Admins = nil, nil, nil
	}))

	// When
	inGroup := h.deliver(fixture(t, "delivery/mention.json"))
	inDM := h.deliver(fixture(t, "delivery/message.json"))

	// Then
	for _, inline := range [][]Action{inGroup, inDM} {
		if want := []Action{{Type: "reply", Text: notAllowedLine}}; !reflect.DeepEqual(inline, want) {
			t.Fatalf("expected %v, got %v", want, inline)
		}
	}
	if h.llm.calls() != 0 {
		t.Fatal("expected no model call")
	}
}

func TestLoadConfigFromEnv_reads_the_lists_and_the_defaults(t *testing.T) {
	// Given
	t.Setenv("BRODEC_GATEWAY_DATA", "/data")
	t.Setenv("BRODEC_ALLOWED_ROOMS", " !a:brodec.ai, !b:brodec.ai\n!c:brodec.ai ")
	t.Setenv("BRODEC_ALLOWED_DM_USERS", "@one:brodec.ai")
	t.Setenv("BRODEC_ADMINS", "@admin:brodec.ai @other:brodec.ai")
	for _, name := range []string{"BRODEC_LISTEN", "BRODEC_GATEWAY_URL", "BRODEC_GATEWAY_PUBLIC_KEY", "BRODEC_BOT_HANDLE", "BRODEC_BOT_NAME", "BRODEC_BOT_USER_ID", "BRODEC_AI_WHERE"} {
		t.Setenv(name, "")
	}

	// When
	got := LoadConfigFromEnv()

	// Then
	want := Config{
		Listen:         "127.0.0.1:8081",
		GatewayURL:     "http://127.0.0.1:8788",
		GatewayData:    "/data",
		AllowedRooms:   []string{"!a:brodec.ai", "!b:brodec.ai", "!c:brodec.ai"},
		AllowedDMUsers: []string{"@one:brodec.ai"},
		Admins:         []string{"@admin:brodec.ai", "@other:brodec.ai"},
		Handle:         "pc_principal",
		Name:           "PC Principal",
		AIWhere:        "an AI model on Bromigos' own servers",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected\n%+v\ngot\n%+v", want, got)
	}
	if got.publicKeyPath() != "/data/webhook-public-key" || got.tokenPath() != "/data/local-api-token" {
		t.Fatalf("unexpected file paths: %s, %s", got.publicKeyPath(), got.tokenPath())
	}
}

func TestNew_needs_the_gateways_data_directory_and_a_store(t *testing.T) {
	// Given
	h := newHarness(t)
	withoutData := h.config
	withoutData.GatewayData = ""

	// When
	_, dataErr := New(withoutData, Deps{Redis: h.rdb, LLM: h.llm})
	_, storeErr := New(h.config, Deps{LLM: h.llm})
	_, modelErr := New(h.config, Deps{Redis: h.rdb})

	// Then
	if dataErr == nil || storeErr == nil || modelErr == nil {
		t.Fatalf("expected an error for each missing piece, got %v, %v, %v", dataErr, storeErr, modelErr)
	}
}
