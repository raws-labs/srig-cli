package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/coder/websocket"
)

type fakeWS struct{ frames []string }

func (f *fakeWS) Read(context.Context) (websocket.MessageType, []byte, error) {
	if len(f.frames) == 0 {
		return 0, nil, errors.New("closed")
	}
	fr := f.frames[0]
	f.frames = f.frames[1:]
	return websocket.MessageText, []byte(fr), nil
}

func serialFrame(s string) string {
	return `{"type":"serial_data","data":"` + base64.StdEncoding.EncodeToString([]byte(s)) + `"}`
}

func TestWaitFlashDoneKeepsOutputAfterFirstFlashOutput(t *testing.T) {
	ws := &fakeWS{frames: []string{
		serialFrame("old firmware DONE\n"),
		`{"type":"flash_output","line":"st-flash 1.9.0"}`,
		`{"type":"flash_output","line":"-> Flash page at 0x8000000 erased"}`,
		serialFrame("banner\n"),
		`{"type":"flash_output","line":"Flash written and verified! jolly good!"}`,
		serialFrame("selftest ok\n"),
		`{"type":"flash_done","success":true}`,
	}}
	early, err := waitFlashDone(context.Background(), ws, true)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for _, b := range early {
		got += string(b)
	}
	if got != "banner\nselftest ok\n" {
		t.Errorf("early output = %q, want the new firmware's output only", got)
	}
}

func TestWaitFlashDoneWithoutFlashOutputKeepsNothing(t *testing.T) {
	ws := &fakeWS{frames: []string{
		serialFrame("old firmware\n"),
		`{"type":"flash_done","success":true}`,
	}}
	early, err := waitFlashDone(context.Background(), ws, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(early) != 0 {
		t.Errorf("early output = %q, want none", early)
	}
}

func TestWaitFlashDoneReportsFailure(t *testing.T) {
	ws := &fakeWS{frames: []string{
		`{"type":"flash_output","line":"st-flash 1.9.0"}`,
		serialFrame("noise"),
		`{"type":"flash_done","success":false,"error":"exit status 255"}`,
	}}
	if _, err := waitFlashDone(context.Background(), ws, true); err == nil || err.Error() != "flash failed: exit status 255" {
		t.Errorf("err = %v, want flash failed: exit status 255", err)
	}
}
