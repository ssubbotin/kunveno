package signal

import "testing"

// FuzzParseClient: no input may panic the parser, and an accepted join keeps its invariants.
func FuzzParseClient(f *testing.F) {
	for _, s := range []string{
		`{"type":"join","room":"ops","pin":"1234","name":"Ann"}`,
		`{"type":"answer","sdp":"v=0\r\n"}`,
		`{"type":"candidate","candidate":{"candidate":""}}`,
		`{"type":"phrase","speaker":"a","start":1,"end":2,"text":"x"}`,
		`{"type":"react","emoji":"👍"}`,
		`{"type":"join","room":"ops","name":"A","role":"notes","restart":{"reason":"ws_closed","after":1}}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ParseClient(data)
		if err != nil {
			return
		}
		if m.Type == "join" && (!slugRe.MatchString(m.Room) || m.Name == "") {
			t.Fatalf("accepted a join with room %q name %q", m.Room, m.Name)
		}
	})
}
