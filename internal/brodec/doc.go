// Package brodec is PC Principal's BRODEC front end.
//
// BRODEC rooms are end-to-end encrypted, so the bot does not talk to BRODEC
// itself. BRODEC's bot gateway runs beside it, holds the bot's keys, and
// does the decrypting and encrypting. This package is the plain HTTP end of
// that pair:
//
//   - the gateway POSTs each event meant for the bot (a mention, a reply to
//     it, a direct message, one of its commands, a tap on one of its
//     buttons) to the event endpoint, signed with the gateway's own Ed25519
//     key; the endpoint refuses anything it cannot verify
//   - the bot answers at once with a list of actions when it can, and later
//     through the gateway's local API when the answer needs the model
//
// Each event becomes a core.Inbound and goes to the same brain the Discord
// front end uses. What differs here is decided by privacy:
//
//   - the bot only ever receives what was addressed to it, and keeps only a
//     short rolling context of that: per room, at most 12 messages, gone two
//     hours after the last one
//   - long-term memory is off: the memory service is never called, and its
//     settings are not even read in this mode
//   - the bot answers only in the rooms and to the people on its allowlists;
//     anywhere else it says one fixed line, at most once a day per room, and
//     never calls the model
//
// The wire format is the gateway's, and testdata/wire holds the gateway's
// own fixtures of it.
package brodec
