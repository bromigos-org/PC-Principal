package commands

import "github.com/bwmarrin/discordgo"

type Command struct {
	Name        string
	Usage       string
	Description string
	Handler     func(*discordgo.Session, *discordgo.MessageCreate)
}

var registry []Command

func register(name, usage, description string, handler func(*discordgo.Session, *discordgo.MessageCreate)) {
	registry = append(registry, Command{
		Name:        name,
		Usage:       usage,
		Description: description,
		Handler:     handler,
	})
}
