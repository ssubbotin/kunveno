package httpapi

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssubbotin/kunveno/internal/lobby"
	"github.com/ssubbotin/kunveno/internal/meetings"
	"github.com/ssubbotin/kunveno/internal/recording"
	"github.com/ssubbotin/kunveno/internal/room"
)

type listedMeeting struct {
	Start  string `json:"start"`
	End    string `json:"end"`
	People int    `json:"people"`
}

func lastMeetings(t *testing.T, body string) map[string]*listedMeeting {
	t.Helper()
	var list []struct {
		Slug        string         `json:"slug"`
		LastMeeting *listedMeeting `json:"last_meeting"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]*listedMeeting{}
	for _, r := range list {
		out[r.Slug] = r.LastMeeting
	}
	return out
}

func meetingsStore(t *testing.T) *meetings.Store {
	t.Helper()
	return meetings.Open(filepath.Join(t.TempDir(), "meetings.json"), nil)
}

func recordingStore(t *testing.T, now *time.Time) *recording.Store {
	t.Helper()
	s, err := recording.Open(t.TempDir(), 24*time.Hour, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRoomsListShowsTheMeetingOfTheKeptNotes(t *testing.T) {
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	reg.Add(room.New("dev", "Разработка", "5678", 15))
	recordMeeting(t, store, "ops") // notes of the 9:37 meeting
	meets := meetingsStore(t)
	_ = meets.Add("ops", meetings.Summary{Start: meetingAt(9, 37), End: meetingAt(10, 2), People: 7})
	_ = meets.Add("dev", meetings.Summary{Start: meetingAt(9, 0), End: meetingAt(9, 30), People: 3}) // no notes, no recording

	rec := do(t, Rooms(RoomsConfig{Registry: reg, Kept: lobby.Kept{Notes: store, NotesEnabled: true, Meetings: meets}}), "GET", "/api/rooms", "")
	got := lastMeetings(t, rec.Body.String())
	m := got["ops"]
	if m == nil || m.People != 7 {
		t.Fatalf("ops last_meeting = %+v", m)
	}
	start, err1 := time.Parse(time.RFC3339, m.Start)
	end, err2 := time.Parse(time.RFC3339, m.End)
	if err1 != nil || err2 != nil || !start.Equal(meetingAt(9, 37)) || !end.Equal(meetingAt(10, 2)) {
		t.Fatalf("ops last_meeting = %+v", m)
	}
	if got["dev"] != nil {
		t.Fatalf("last_meeting on a room without notes or a recording: %+v", got["dev"])
	}
	if strings.Contains(rec.Body.String(), "Анна") {
		t.Fatal("a participant's name in /api/rooms")
	}
	rec = do(t, Rooms(RoomsConfig{Registry: reg, Kept: lobby.Kept{Notes: store, Meetings: meets}}), "GET", "/api/rooms", "")
	if strings.Contains(rec.Body.String(), "last_meeting") {
		t.Fatal("last_meeting while notes are disabled")
	}
}

func TestRoomsListUsesTheMeetingOfTheKeptRecording(t *testing.T) {
	now := meetingAt(12, 0)
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	rec := recordingStore(t, &now)
	if _, err := rec.Put("ops", meetingAt(9, 37), meetingAt(9, 37), strings.NewReader("v"), 1<<20); err != nil {
		t.Fatal(err)
	}
	meets := meetingsStore(t)
	_ = meets.Add("ops", meetings.Summary{Start: meetingAt(9, 37), End: meetingAt(10, 2), People: 4})
	// A later meeting without notes or a recording is the newest summary.
	_ = meets.Add("ops", meetings.Summary{Start: meetingAt(11, 0), End: meetingAt(11, 10), People: 2})
	m := lastMeetings(t, do(t, Rooms(RoomsConfig{Registry: reg, Kept: lobby.Kept{Recordings: rec, RecordingEnabled: true, Meetings: meets}}), "GET", "/api/rooms", "").Body.String())["ops"]
	if m == nil || m.People != 4 {
		t.Fatalf("last_meeting = %+v; want the recorded meeting of 4 people", m)
	}
}

func TestRoomsListPrefersTheRecordingsMeeting(t *testing.T) {
	now := meetingAt(10, 5)
	store := notesStore(t, &now)
	reg := room.NewRegistry()
	reg.Add(room.New("ops", "Операции", "1234", 15))
	recordMeeting(t, store, "ops") // notes of the 9:37 meeting
	rec := recordingStore(t, &now)
	if _, err := rec.Put("ops", meetingAt(8, 0), meetingAt(8, 0), strings.NewReader("v"), 1<<20); err != nil {
		t.Fatal(err)
	}
	meets := meetingsStore(t)
	_ = meets.Add("ops", meetings.Summary{Start: meetingAt(8, 0), End: meetingAt(8, 30), People: 5})
	_ = meets.Add("ops", meetings.Summary{Start: meetingAt(9, 37), End: meetingAt(10, 2), People: 7})
	m := lastMeetings(t, do(t, Rooms(RoomsConfig{Registry: reg, Kept: lobby.Kept{Notes: store, NotesEnabled: true, Recordings: rec, RecordingEnabled: true, Meetings: meets}}), "GET", "/api/rooms", "").Body.String())["ops"]
	if m == nil || m.People != 5 {
		t.Fatalf("last_meeting = %+v; want the recording's meeting", m)
	}
	// Without a summary of the recording's meeting the notes' meeting is used.
	_ = meets.Delete("ops")
	_ = meets.Add("ops", meetings.Summary{Start: meetingAt(9, 37), End: meetingAt(10, 2), People: 7})
	m = lastMeetings(t, do(t, Rooms(RoomsConfig{Registry: reg, Kept: lobby.Kept{Notes: store, NotesEnabled: true, Recordings: rec, RecordingEnabled: true, Meetings: meets}}), "GET", "/api/rooms", "").Body.String())["ops"]
	if m == nil || m.People != 7 {
		t.Fatalf("last_meeting = %+v; want the notes' meeting", m)
	}
}

func TestDeletingARoomDeletesItsMeetings(t *testing.T) {
	reg := room.NewRegistry()
	if _, err := reg.Create("Retro", "432100"); err != nil {
		t.Fatal(err)
	}
	meets := meetingsStore(t)
	_ = meets.Add("retro", meetings.Summary{Start: meetingAt(9, 37), End: meetingAt(10, 2), People: 7})
	rec := do(t, Rooms(RoomsConfig{Registry: reg, Kept: lobby.Kept{Meetings: meets}}), "DELETE", "/api/rooms/retro", `{"pin":"432100"}`)
	if rec.Code != 204 {
		t.Fatalf("delete = %d", rec.Code)
	}
	if got := meets.List("retro"); len(got) != 0 {
		t.Fatalf("meetings of a deleted room stay: %+v", got)
	}
}
