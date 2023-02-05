package main

import (
	"flag"
	"fmt"
	_ "github.com/grammaton76/chattools/pkg/chat_output/sc_dbtable"
	"github.com/grammaton76/g76golib/pkg/shared"
	"github.com/grammaton76/g76golib/pkg/sjson"
	"github.com/grammaton76/g76golib/pkg/slogger"
	"os"
	"time"
)

var log slogger.Logger

var Config shared.Configuration

var Cli struct {
	Inifile      string
	ReadOnly     bool
	Debug        bool
	IniBlock     string
	Channel      string
	Options      string
	SetTopic     string
	Sender       string
	Text         string
	ItemRef      string
	IdFile       string
	Pin          bool
	Unpin        bool
	RespondTo    int
	UpdateId     int64
	UpdateLabel  string
	Label        string
	LabelIfReply string
}

var Global struct {
	Chat     *shared.ChatHandle
	ChatType shared.ChatType
	ReadOnly bool
}

func LoadConfigValues(inifiles ...string) {
	slogger.SetLogger(&log)
	OtherIni := flag.String("inifile", "", "Specify an INI file for settings")
	ReadOnly := flag.Bool("readonly", false, "No writes to database or filesystem.")
	Debug := flag.Bool("debug", false, "Enable verbose debugging.")
	IniBlock := flag.String("iniblock", "defaultchat", "Which block in the ini file to use")
	Channel := flag.String("channel", "", "Specify a channel (takes precedent over config block value)")
	Handle := flag.String("handle", "", "Specify a handle (takes precedent over config block value)")
	Options := flag.String("options", "", "Options block (sjson)")
	Text := flag.String("text", "", "Message text")
	PinPost := flag.Bool("pin", false, "Pin post (requires postid)")
	UnPinPost := flag.Bool("unpin", false, "Unpin post (requires postid)")
	SetTopic := flag.String("settopic", "", "Set channel topic to")
	RespondTo := flag.Int("respondto", 0, "Respond underneath (requires postid)")
	UpdateId := flag.Int64("updateid", 0, "Update a post (requires postid)")
	IdFile := flag.String("statefile", "", "State file to write the chat's numeric ID to (enables you to script updates/unpins)")
	Label := flag.String("label", "", "Message label (creates conflict intentionally - default conflict behavior is no chat; see below)")
	UpdateLabel := flag.String("updatelabel", "", "Update a post (via label)")
	LabelIfReply := flag.String("labelifreply", "", "Alternate label to use if primary label is exists and reply is the resolution path")
	LabelRespond := flag.Bool("labelconflict-respond", false, "If label conflict, respond to label-holding post's thread.")
	LabelUpdate := flag.Bool("labelconflict-update", false, "If label conflict, update label-holding post's text.")
	flag.Parse()
	Cli.Inifile = *OtherIni
	Cli.ReadOnly = *ReadOnly
	Cli.Debug = *Debug
	Cli.Channel = *Channel
	Cli.Sender = *Handle
	Cli.Options = *Options
	Cli.SetTopic = *SetTopic
	Cli.IniBlock = *IniBlock
	Cli.Text = *Text
	Cli.Pin = *PinPost
	Cli.Label = *Label
	Cli.Unpin = *UnPinPost
	Cli.RespondTo = *RespondTo
	Cli.UpdateId = *UpdateId
	Cli.UpdateLabel = *UpdateLabel
	Cli.LabelIfReply = *LabelIfReply
	Cli.IdFile = *IdFile

	if Cli.Debug {
		log.SetThreshold(slogger.DEBUG)
	}
	if *LabelRespond == true {
		Global.ChatType = shared.MsgPostOrReply
	}
	if *LabelUpdate == true {
		Global.ChatType = shared.MsgPostOrUpdate
	}
	Global.ReadOnly = Cli.ReadOnly
	if Cli.Inifile != "" {
		inifiles = []string{Cli.Inifile}
	}
	Config.SetDefaultIni(inifiles).OrDie("")
}

func InitAndConnect() {
	slogger.SetLogger(&log)
	shared.SetLogger(&log)
	log.Init()
	if Cli.Debug {
		log.SetThreshold(slogger.DEBUG)
	}
	ThreadPurpose := shared.NewThreadPurpose()
	go ThreadPurpose.SetDeadman(5 * time.Second)
	// Block start
	ThreadPurpose.WgAdd(1)
	go func(fwg *shared.ThreadPurpose) {
		defer ThreadPurpose.Done()
		Global.Chat = Config.NewChatHandle(Cli.IniBlock)
		if Cli.Sender != "" {
			Global.Chat.SetDefaultSender(Cli.Sender)
		}
		if Cli.Channel != "" {
			Global.Chat.SetDefaultChannel(Cli.Channel)
		}
		Global.Chat.PrintChatOnly = Global.ReadOnly
		//Global.Chat.PrintChatOnly = true
	}(ThreadPurpose)
	// Block stop
	ThreadPurpose.WgWait()
	ThreadPurpose.DisarmDeadman()
}

func main() {
	LoadConfigValues("${CONFIG}/chatcli.ini", "/data/config/chatcli.ini")
	InitAndConnect()
	Msg := Global.Chat.NewMessage()
	Msg.MsgType = Global.ChatType
	Msg.Message = Cli.Text
	Msg.Label = Cli.Label
	if Cli.Options != "" {
		Msg.Options.IngestFromString(Cli.Options)
	}
	if Cli.LabelIfReply != "" {
		Msg.LabelIfReply = Cli.LabelIfReply
	}
	if Msg.Options == nil {
		Caw := sjson.NewJson()
		Msg.Options = &Caw
	}
	if Cli.RespondTo != 0 {
		(*(Msg.Options))["respondto"] = Cli.RespondTo
	}
	if Cli.SetTopic != "" {
		(*(Msg.Options))["SetTopic"] = Cli.SetTopic
	}
	if Cli.UpdateId != 0 {
		Update := &shared.ChatUpdateHandle{
			NativeData: nil,
			ChannelId:  "",
			Timestamp:  "",
			MsgId:      Cli.UpdateId,
			UpdateId:   0,
		}
		Msg.UpdateHandle = Update
		if Cli.Unpin {
			(*(Msg.Options))["UnpinPost"] = true
		}
	}
	if Cli.UpdateLabel != "" {
		Msg.Label = Cli.UpdateLabel
		Msg.MsgType = shared.MsgUpdate
	}
	if Cli.Pin {
		(*(Msg.Options))["PinPost"] = true
	}
	ChatId, err := Global.Chat.Send(Msg)
	if ChatId != nil {
		log.Debugf("Message passed to handler. Id %d obtained (if an update, this will be the same one you supplied).\n",
			*ChatId)
		if Cli.IdFile != "" {
			f, err := os.Create(Cli.IdFile)
			if err != nil {
				log.Printf("Inserted chat, but error opening statefile '%s': '%s'\n", Cli.IdFile, err)
			} else {
				f.Write([]byte(fmt.Sprintf("%d", ChatId.UpdateId)))
				f.Close()
			}
		}
	} else {
		if err := os.RemoveAll(Cli.IdFile); err != nil {
			log.Printf("ERROR deleting statefile '%s': %s'\n", Cli.IdFile, err)
		} else {
			log.Printf("Due to chat error, we have deleted the statefile.\n")
		}
	}
	if err != nil {
		log.Printf("ERROR inserting chat: %s\n", err)
	}
}
