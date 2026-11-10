// Package core is PC Principal's brain: everything the bot does that does
// not depend on the chat platform it is running on.
//
// A front end (today only Discord, in internal/commands) turns each message
// it receives into an Inbound, and hands it to the core together with two
// things only the platform can provide: a Responder, which is how the brain
// answers, and a Describer, which tells the brain who is speaking and where
// in words fit for a prompt.
//
// The core owns the persona prompt, the conversation history, prompt
// assembly, the model call, memory recall and write-back, reasoning traces,
// reviewed skills, and the decision of which handler a message goes to. It
// never imports a platform library.
//
// Who may talk to the bot (roles, allowlists) is the front end's business:
// a front end does not hand the core a message from someone it would not
// answer.
package core
