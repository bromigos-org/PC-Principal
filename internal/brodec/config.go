package brodec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultGatewayURL = "http://127.0.0.1:8788"
	defaultListen     = "127.0.0.1:8081"
	defaultHandle     = "pc_principal"
	defaultName       = "PC Principal"
	defaultAIWhere    = "an AI model on Bromigos' own servers"

	// EventsPath is where the gateway posts its deliveries.
	EventsPath = "/brodec/events"

	publicKeyFile = "webhook-public-key"
	tokenFile     = "local-api-token"
)

// Config is how the BRODEC front end is set up.
type Config struct {
	// Listen is the address the event endpoint listens on. The gateway
	// runs beside the bot and reaches it over loopback.
	Listen string
	// GatewayURL is the gateway's local API.
	GatewayURL string
	// GatewayData is the gateway's data directory, shared read-only with
	// the bot. The bot reads two files there: the gateway's public key and
	// the token of its local API.
	GatewayData string
	// PublicKey is the gateway's public key itself. When it is set, the key
	// file is not read.
	PublicKey string

	// AllowedRooms are the rooms the bot answers in.
	AllowedRooms []string
	// AllowedDMUsers are the people (Matrix IDs) the bot answers in a
	// direct message.
	AllowedDMUsers []string
	// Admins are the people (Matrix IDs) who run the bot. They are answered
	// in a direct message as well.
	Admins []string

	// Handle, Name and UserID are the ways the bot may be written in a
	// message that mentions it. They are cut out of what goes to the model.
	Handle string
	Name   string
	UserID string

	// AIWhere finishes the sentence "What you say to me goes to ..." in the
	// privacy text. It must say where the model at LITELLM_BASE_URL runs.
	AIWhere string
}

// LoadConfigFromEnv reads the configuration from the environment.
func LoadConfigFromEnv() Config {
	return Config{
		Listen:         envDefault("BRODEC_LISTEN", defaultListen),
		GatewayURL:     envDefault("BRODEC_GATEWAY_URL", defaultGatewayURL),
		GatewayData:    strings.TrimSpace(os.Getenv("BRODEC_GATEWAY_DATA")),
		PublicKey:      strings.TrimSpace(os.Getenv("BRODEC_GATEWAY_PUBLIC_KEY")),
		AllowedRooms:   splitList(os.Getenv("BRODEC_ALLOWED_ROOMS")),
		AllowedDMUsers: splitList(os.Getenv("BRODEC_ALLOWED_DM_USERS")),
		Admins:         splitList(os.Getenv("BRODEC_ADMINS")),
		Handle:         envDefault("BRODEC_BOT_HANDLE", defaultHandle),
		Name:           envDefault("BRODEC_BOT_NAME", defaultName),
		UserID:         strings.TrimSpace(os.Getenv("BRODEC_BOT_USER_ID")),
		AIWhere:        envDefault("BRODEC_AI_WHERE", defaultAIWhere),
	}
}

// validate reports what is missing from a configuration.
func (c Config) validate() error {
	if c.GatewayData == "" {
		return errors.New("BRODEC_GATEWAY_DATA is not set: it is the gateway's data directory, where the bot reads the gateway's public key and the token of its local API")
	}
	if c.GatewayURL == "" {
		return errors.New("BRODEC_GATEWAY_URL is empty")
	}
	return nil
}

func (c Config) publicKeyPath() string { return filepath.Join(c.GatewayData, publicKeyFile) }

func (c Config) tokenPath() string { return filepath.Join(c.GatewayData, tokenFile) }

func envDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// splitList reads a list written with commas, spaces or line breaks between
// its items.
func splitList(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})
}

// allowlist decides where the bot answers. It is the stopgap for a gateway
// that joins every room it is invited to: being in a room is not the same as
// being allowed to spend the model there.
type allowlist struct {
	rooms   map[string]bool
	dmUsers map[string]bool
	admins  map[string]bool
}

func newAllowlist(c Config) allowlist {
	return allowlist{rooms: set(c.AllowedRooms), dmUsers: set(c.AllowedDMUsers), admins: set(c.Admins)}
}

func set(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}

// isAdmin reports whether the person runs the bot.
func (a allowlist) isAdmin(userID string) bool { return a.admins[userID] }

// answersDM reports whether the bot answers the person in a direct message.
func (a allowlist) answersDM(userID string) bool { return a.dmUsers[userID] || a.admins[userID] }

// allows reports whether the bot answers a delivery.
//
// A message, a mention or a command is answered in a group on the room
// list, and in a direct message from someone on the people lists. A tap
// carries no word on which of the two its room is, so it is answered when
// either holds; a button only exists on a card the bot posted where it was
// allowed.
func (a allowlist) allows(d Delivery) bool {
	switch d.Type {
	case deliveryCommand, deliveryMention, deliveryMessage:
		if d.IsGroup {
			return a.rooms[d.RoomID]
		}
		return a.answersDM(d.Sender)
	case deliveryButton:
		return a.rooms[d.RoomID] || a.answersDM(d.Sender)
	default:
		return false
	}
}
