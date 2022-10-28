package sc_notify

import (
	"github.com/gen2brain/beeep"
	"github.com/grammaton76/g76golib/shared"
)

type chatNotify struct {
	Parent *shared.ChatHandle
}

func xFpSendMessage(cth *shared.ChatHandle, msg *shared.ChatMessage) (*shared.ChatUpdateHandle, error) {
	err := beeep.Alert(msg.Label, msg.Message, "assets/warning.png")
	return nil, err
}

func xFpSendSimple(cth *shared.ChatHandle, tgt *shared.ChatTarget, msg string) (*shared.ChatUpdateHandle, error) {
	err := beeep.Alert("", msg, "assets/warning.png")
	return nil, err
}

func BindToHandle(cth *shared.ChatHandle, cfg *shared.Configuration, Section string) error {
	cth.NativeClient = chatNotify{Parent: cth}
	cth.FpSendMessage = xFpSendMessage
	cth.FpSendSimple = xFpSendSimple
	return nil
}

func init() {
	CTC := shared.ChatTypeConfig{
		BindFunc: BindToHandle,
	}
	shared.ChatHandleConfigs["notify"] = &CTC
}
