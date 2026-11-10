// Package characterization holds black-box transcripts of the Discord bot's
// conversation paths.
//
// The tests drive the exported Discord handlers with fixed messages and
// record, in order, everything the bot does in answer: each Discord REST
// request, each chat-completion request, each gnosis request, each read and
// write of the conversation history, each change of the ambient session, and
// each log line. The record is compared with a golden file under testdata.
//
// The golden files were written from the code as it was before the bot's
// brain moved to internal/core. A change that is meant to keep Discord
// behaviour the same must leave every golden file untouched; a change that is
// meant to alter behaviour updates them with
//
//	go test ./internal/characterization -update
//
// and the diff of the golden files is the behaviour change, in full.
//
// Only test files live here besides this one. The package is separate from
// internal/commands so that it can point the shared history store at a fake
// without touching that package's own tests.
package characterization
