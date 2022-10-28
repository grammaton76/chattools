package main

import (
	"flag"
	"fmt"
	resty "github.com/go-resty/resty/v2"
	_ "github.com/grammaton76/chattools/pkg/chat_output/sc_dbtable"
	"github.com/grammaton76/g76golib/shared"
	"github.com/grammaton76/g76golib/sjson"
	"github.com/grammaton76/g76golib/slogger"
	"io/ioutil"
	"net/url"
	"os"
	"time"
)

var log slogger.Logger

var Config shared.Configuration

var Cli struct {
	Inifile  string
	ReadOnly bool
	Debug    bool
	InitMode bool
}

var Global struct {
	ReadOnly     bool
	Client       *resty.Client
	EmojiUrl     string
	ChatSection  string
	ChatToken    string
	Chat         *shared.ChatTarget
	StateFile    string
	LastEmoji    sjson.JSON
	CurrentEmoji sjson.JSON
}

func LoadConfigValues(inifile string) {
	log.Init()
	slogger.SetLogger(&log)
	shared.SetLogger(&log)
	OtherIni := flag.String("inifile", "", "Specify an INI file for settings")
	ReadOnly := flag.Bool("readonly", false, "No writes to database or filesystem.")
	Debug := flag.Bool("debug", false, "Enable verbose debugging.")
	InitMode := flag.Bool("init", false, "Initialization; skip loading the emoji state file.")
	flag.Parse()
	Cli.Inifile = *OtherIni
	Cli.ReadOnly = *ReadOnly
	Cli.Debug = *Debug
	Cli.InitMode = *InitMode

	if Cli.Debug {
		log.SetThreshold(slogger.DEBUG)
	}
	if Cli.ReadOnly {
		Global.ReadOnly = true
	}
	if Cli.Inifile != "" {
		inifile = Cli.Inifile
	}
	Config.SetDefaultIni(inifile).OrDie("Could not load INI file '%s'", inifile)
	Global.EmojiUrl = Config.GetStringOrDie("check-slack-emoji.emojiurl", "")
	Global.ChatSection = Config.GetStringOrDie("check-slack-emoji.chathandle", "")
	CheckToken := Config.GetStringOrDie("check-slack-emoji.checktoken", "Need a path to a secret token (i.e. token.emojidumper)")
	Global.ChatToken = Config.GetStringOrDie(CheckToken, "The secret path pointed to by check-slack-emoji.checktoken must resolve to a valid Slack token.")
	Global.StateFile = Config.GetStringOrDie("check-slack-emoji.statefile", "")
}

func InitAndConnect() {
	Global.Client = resty.New()
	Global.Client.SetTimeout(time.Second * 10)
	ThreadPurpose := shared.NewThreadPurpose()
	go ThreadPurpose.SetDeadman(5 * time.Second)
	ThreadPurpose.WgAdd(1)
	go func(fwg *shared.ThreadPurpose) {
		defer ThreadPurpose.Done()
		ThreadPurpose.Set("Database setup thread")
	}(ThreadPurpose)
	ThreadPurpose.WgWait()
	ThreadPurpose.DisarmDeadman()
	Handle := Config.NewChatHandle(Global.ChatSection).OrDie("Failed to load chat handle")
	Global.Chat = Handle.OutputChannel.OrDie("Failed to set chat target.")
	if Global.ReadOnly {
		Global.Chat.Handle.PrintChatOnly = true
	}
	if Cli.Debug {
		log.Debugf("Chat: %s\n", Global.Chat.Identifier())
	}
}

func GetSlackEmojiList() error {
	if Global.ChatToken == "" {
		log.Fatalf("It is not possible to do anything with a blank chat token. Configuration is off.\n")
	}
	resp, err := Global.Client.R().
		EnableTrace().
		SetFormData(map[string]string{
			"token": Global.ChatToken}).
		Post(Global.EmojiUrl)
	if err != nil {
		log.Printf("ERROR fetching emoji url '%s'!\n", Global.EmojiUrl)
		return err
	}
	if resp.StatusCode() != 200 {
		log.Printf("ERROR fetching Slack emojis: '%s' returned status code %d!\n", Global.EmojiUrl, resp.StatusCode())
		return fmt.Errorf("http status %d", resp.StatusCode())
	}
	var Response sjson.JSON
	err = Response.IngestFromBytes(resp.Body())
	if err != nil {
		_ = Global.Chat.LogErrorf("Error fetching emoji from slack: %s\n", err)
		os.Exit(3)
	}
	err = Global.CurrentEmoji.IngestFromObject(Response["emoji"])
	if err != nil {
		_ = Global.Chat.LogErrorf("Error decoding emoji from slack - check logs for details, but short version is: %s\n", err)
		log.Warnf("Emoji body\n===\n%s\n===\n", resp.Body())
		os.Exit(3)
	}
	return nil
}

func ReadEmojiFromStateFile() (sjson.JSON, error) {
	dat, err := ioutil.ReadFile(Global.StateFile)
	if err != nil {
		return nil, fmt.Errorf("state file '%s' not loadable: '%s'", Global.StateFile, err)
	}
	var Caw sjson.JSON
	log.Debugf("Ingested %d bytes from state file '%s'.\n", len(dat), Global.StateFile)
	err = Caw.IngestFromBytes(dat)
	log.FatalIff(err, "Emoji not readable from state file. Run with -init flag if this is the first run. Error message: %s\n")
	return Caw, nil
}

func WriteEmojiToStateFile(Emoji sjson.JSON) error {
	err := ioutil.WriteFile(Global.StateFile, Emoji.Bytes(), 644)
	if err != nil {
		return fmt.Errorf("state file '%s' not writable: %s", Global.StateFile, err)
	}
	log.Printf("Wrote emoji to '%s'\n", Global.StateFile)
	return nil
}

func EvalEmojiDiffs() {
	var Bitmap = make(map[string]int)
	for k := range Global.LastEmoji {
		Bitmap[k] |= 1
	}
	var CurCount int
	for k := range Global.CurrentEmoji {
		Bitmap[k] |= 2
		CurCount++
	}
	var Lines []string
	var Adds int
	var Removes int
	var Changes int
	for k, v := range Bitmap {
		var Buf string
		enc_k := url.QueryEscape(k)
		switch v {
		case 1:
			Buf = fmt.Sprintf("Emoji '%s' is now gone; was: %s\n", enc_k, Global.LastEmoji[k])
			Removes++
		case 2:
			Buf = fmt.Sprintf(":%s: New emoji '%s' added: %s\n", enc_k, enc_k, Global.CurrentEmoji[k])
			Adds++
		case 3:
			if Global.LastEmoji[k] != Global.CurrentEmoji[k] {
				Changes++
				Buf = fmt.Sprintf(":%s: Emoji change detected - '%s' went from '%s' to '%s'\n", enc_k, enc_k, Global.LastEmoji[k], Global.CurrentEmoji[k])
			}
		}
		if Buf != "" {
			Lines = append(Lines, Buf)
		}
	}
	if Adds+Removes+Changes == 0 {
		log.Printf("There were no changes at all (no adds, removes, or deletes). Exiting.\n")
		os.Exit(0)
	}
	if Adds+Removes+Changes > 50 {
		log.Printf("Sanity check failed; we shouldn't have 50+ emoji changes! (%d adds, %d removes, %d changes since last run)\n",
			Adds, Removes, Changes)
		return
	}
	Options := sjson.NewJson()
	Options["mediaunfurl"] = true
	Send := shared.ChatMessage{
		Options: &Options,
	}
	var Count = 0
	for _, v := range Lines {
		Count++
		log.Printf("Sending '%s' ...\n", v)
		Send.Message = v
		Global.Chat.Send(&Send)
		if Count >= 20 {
			return
		}
	}
	Global.Chat.Sendf("There are now %d emojis and aliases in this Slack instance (%d adds, %d removes, %d changes since last run).\n",
		CurCount, Adds, Removes, Changes)
}

func main() {
	LoadConfigValues("/data/config/check-slack-emoji.ini")
	InitAndConnect()
	var err error
	Global.LastEmoji, err = ReadEmojiFromStateFile()
	if err != nil {
		log.Printf("Failed to read emoji: '%s'!\n", err)
	}
	GetSlackEmojiList()
	//delete(Global.LastEmoji, "404")
	//delete(Global.CurrentEmoji, "doge")
	EvalEmojiDiffs()
	if !Global.ReadOnly {
		WriteEmojiToStateFile(Global.CurrentEmoji)
	}
}
