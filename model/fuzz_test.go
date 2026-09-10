package model_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/abubakarsiddik31/golem/model"
)

// FuzzMessageJSONIsDurable pins the durability half of the message-JSON
// contract against arbitrary input: whatever decodes must re-marshal to
// bytes that decode again (nothing Golem writes is unreadable), and that
// second marshaling must be byte-identical to the first — persisting what
// you read never rewrites meaning. The first marshaling is a
// normalization round, so equality is only required from the second on:
// compacted tool-call arguments and escaped strings differ from their
// input spelling by design.
func FuzzMessageJSONIsDurable(f *testing.F) {
	f.Add([]byte(`[{"role":"user","content":"hi"}]`))
	f.Add([]byte(`[{"role":"system","content":"Be concise."},` +
		`{"role":"assistant","toolCalls":[{"id":"call-1","name":"roll_dice","args":{"guess":4}}]},` +
		`{"role":"tool","content":"4","toolCallId":"call-1","toolName":"roll_dice"}]`))
	f.Add([]byte(`[{"role":"user","content":"look","parts":[{"kind":"image","url":"https://example.com/cat.png"}]}]`))
	f.Add([]byte(`[{"role":"user","parts":[{"kind":"image","data":"AQID","mediaType":"image/png"}]}]`))
	f.Add([]byte(`[{"role":"user","parts":[{"kind":"document","url":"https://example.com/a.pdf","mediaType":"application/pdf"}]}]`))
	f.Add([]byte(`[{"role":"user","parts":[{"kind":"audio","data":"AQID","mediaType":"audio/wav"}]}]`))
	f.Add([]byte(`[{"role":"user","parts":[{"kind":"video","data":"AQID","mediaType":"video/mp4"}]}]`))
	f.Add([]byte(`[{"role":"assistant","content":"thinking it through","thinking":[{"text":"hmm","signature":"sig"},{"redacted":"tok"}]}]`))
	f.Add([]byte(`[{"role":"user","content":"hi","futureField":true},{"role":"assistant"}]`))
	f.Add([]byte(`[{"role":"assistant","toolCalls":[{"id":"c","name":"n","args":{"a":  1, "b":[2,3]}}]}]`))
	f.Add([]byte(`[{"role":"user","content":"a<b>&c"}]`))
	f.Add([]byte(`[null]`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"role":"user","content":"truncated"`))
	f.Add([]byte(`[{"role":"tool","content":"mailbox is read-only","toolCallId":"c1","toolName":"archive","failed":true}]`))
	f.Add([]byte(`[{"role":"tool","content":"screenshot","toolCallId":"c1","toolName":"camera","parts":[{"kind":"image","data":"AQID","mediaType":"image/png"}]}]`))
	f.Add([]byte(`[{"role":"user","content":"hi","runId":"0198c7b2-6b7a-7cc3-9f4e-3f1d2a5b6c7d","conversationId":"0198c7b2-6b70-7aa1-8e2d-4c5b6a7f8e9d"}]`))
	f.Add([]byte(`not json at all`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var first []model.Message
		if err := json.Unmarshal(data, &first); err != nil {
			return
		}
		encoded, err := json.Marshal(first)
		if err != nil {
			t.Fatalf("Marshal of a decoded history failed: %v", err)
		}
		var second []model.Message
		if err := json.Unmarshal(encoded, &second); err != nil {
			t.Fatalf("Golem's own encoding does not decode back: %v\nencoded: %s", err, encoded)
		}
		reencoded, err := json.Marshal(second)
		if err != nil {
			t.Fatalf("Marshal of the re-decoded history failed: %v", err)
		}
		if !bytes.Equal(encoded, reencoded) {
			t.Fatalf("encoding is not a fixed point:\nfirst:  %s\nsecond: %s", encoded, reencoded)
		}
	})
}
