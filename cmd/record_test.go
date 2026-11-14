package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raws-labs/srig-cli/client"
)

// The upload carries the record's settings next to the firmware, and the wait
// returns once the record has left the recording state.
func TestRecordUploadsAndWaits(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/run-records":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse form: %v", err)
			}
			got := map[string]string{
				"board_type": r.FormValue("board_type"), "name": r.FormValue("name"),
				"send": r.FormValue("send"), "run_seconds": r.FormValue("run_seconds"),
			}
			want := map[string]string{"board_type": "stm32-h753", "name": "bench", "send": "go\r", "run_seconds": "20"}
			for k, v := range want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
			if _, _, err := r.FormFile("firmware"); err != nil {
				t.Errorf("no firmware: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(client.RunRecord{ID: "rec_1", State: "recording", RunSeconds: 20, URL: "https://x/record/rec_1"})
		case r.Method == "GET" && r.URL.Path == "/v1/run-records/rec_1":
			state := "recording"
			if polls.Add(1) >= 3 {
				state = "ready"
			}
			json.NewEncoder(w).Encode(client.RunRecord{ID: "rec_1", State: state})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := client.New(srv.URL, "key_test")

	created, err := c.CreateRunRecord("stm32-h753", "bench", "go\r", 20, "firmware.bin", []byte("image"))
	if err != nil || created.ID != "rec_1" || created.URL == "" {
		t.Fatalf("create: %+v, %v", created, err)
	}
	rec, err := waitForRecord(context.Background(), c, "rec_1", time.Millisecond)
	if err != nil || rec.State != "ready" || polls.Load() != 3 {
		t.Errorf("wait: %+v, %v after %d polls", rec, err, polls.Load())
	}
}

// A record that is gone stops the wait at once; a coordinator that is down for
// a moment does not.
func TestWaitForRecordErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/run-records/rec_gone":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"record not found"}`))
		case "/v1/run-records/rec_flaky":
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			json.NewEncoder(w).Encode(client.RunRecord{ID: "rec_flaky", State: "failed", Error: "writing the firmware failed"})
		}
	}))
	defer srv.Close()
	c := client.New(srv.URL, "")

	if _, err := waitForRecord(context.Background(), c, "rec_gone", time.Millisecond); err == nil {
		t.Error("a missing record kept the wait going")
	}
	rec, err := waitForRecord(context.Background(), c, "rec_flaky", time.Millisecond)
	if err != nil || rec.State != "failed" {
		t.Errorf("flaky: %+v, %v", rec, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(client.RunRecord{ID: "rec_slow", State: "recording"})
	}))
	defer srv2.Close()
	if _, err := waitForRecord(ctx, client.New(srv2.URL, ""), "rec_slow", time.Millisecond); err == nil {
		t.Error("the wait outlived its deadline")
	}
}
