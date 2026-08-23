package main

import (
	"encoding/json"
	"testing"
)

func TestWirePayloadsUseRustClientFieldNames(t *testing.T) {
	content, err := json.Marshal(newContentPayload("movie", "tt123", "", "Example"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"type":"content","contentId":"tt123","contentType":"movie","title":"Example"}` {
		t.Fatalf("unexpected content payload: %s", content)
	}

	sync, err := json.Marshal(newSyncPayload(playbackState{
		PositionMs: 1200, DurationMs: 5000, Playing: true,
		ContentID: "tt123", ContentType: "movie", Title: "Example",
	}, 4, "server", 99))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(sync, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"type", "sequence", "senderId", "serverTimeMs", "positionMs", "durationMs", "playing", "buffering", "contentId", "contentType", "title"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("missing wire field %q in %s", key, sync)
		}
	}
}
