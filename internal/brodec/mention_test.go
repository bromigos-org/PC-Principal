package brodec

import "testing"

func TestMentionStripper_cuts_every_way_the_bot_may_be_written(t *testing.T) {
	const userID = "@a0123456789abcdef01234567:brodec.ai"
	stripper := newMentionStripper("pc_principal", "PC Principal", userID)

	cases := map[string]struct{ body, want string }{
		"the handle":                      {"@pc_principal what's the score?", "what's the score?"},
		"the handle with a colon":         {"@pc_principal: what's the score?", "what's the score?"},
		"the handle in another case":      {"@PC_Principal what's the score?", "what's the score?"},
		"the name":                        {"@PC Principal what's the score?", "what's the score?"},
		"the name at the end":             {"what's the score, @PC Principal", "what's the score,"},
		"the Matrix ID":                   {userID + " what's the score?", "what's the score?"},
		"the Matrix ID in the middle":     {"hey " + userID + ": what's the score?", "hey what's the score?"},
		"a link to the Matrix ID":         {"[PC Principal](https://matrix.to/#/" + userID + ") what's the score?", "what's the score?"},
		"a bare link to the Matrix ID":    {"https://matrix.to/#/" + userID + " what's the score?", "what's the score?"},
		"the bare name leading":           {"PC Principal, what's the score?", "what's the score?"},
		"the bare handle leading":         {"pc_principal: what's the score?", "what's the score?"},
		"twice":                           {"@pc_principal @PC Principal hello", "hello"},
		"nothing but the mention":         {"@pc_principal", ""},
		"the bare name as a subject":      {"who is PC Principal?", "who is PC Principal?"},
		"a longer handle that starts so":  {"@pc_principal2 is someone else", "@pc_principal2 is someone else"},
		"no mention at all":               {"what's the score so far?", "what's the score so far?"},
		"line breaks without a mention":   {"remind me at 21:40\n\"HEAT\" · café", "remind me at 21:40\n\"HEAT\" · café"},
		"line breaks around a mention":    {"@pc_principal first line\nsecond line", "first line\nsecond line"},
		"another person's mention stays":  {"@pc_principal ask @someone_else about it", "ask @someone_else about it"},
		"another Matrix ID stays":         {"@pc_principal is @b3f9a1c05d7e2b46a8c0d1e9:brodec.ai in?", "is @b3f9a1c05d7e2b46a8c0d1e9:brodec.ai in?"},
		"surrounding blanks are trimmed":  {"   @pc_principal   hi   ", "hi"},
		"a word that only starts so":      {"PC Principals are a type", "PC Principals are a type"},
		"the name leading, nothing after": {"PC Principal", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			// When
			got := stripper.strip(c.body)

			// Then
			if got != c.want {
				t.Fatalf("expected %q, got %q", c.want, got)
			}
		})
	}
}

func TestMentionStripper_without_a_Matrix_ID_still_cuts_handle_and_name(t *testing.T) {
	// Given: the bot's Matrix ID is not configured
	stripper := newMentionStripper("pc_principal", "PC Principal", "")

	// When
	got := stripper.strip("@pc_principal hello")

	// Then
	if got != "hello" {
		t.Fatalf("expected %q, got %q", "hello", got)
	}
}
