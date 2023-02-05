package main

import (
	"encoding/json"
	"flag"
	"fmt"
	_ "github.com/grammaton76/chattools/pkg/chat_output/sc_slack"
	"github.com/grammaton76/g76golib/pkg/shared"
	"github.com/grammaton76/g76golib/pkg/sjson"
	"github.com/grammaton76/g76golib/pkg/slogger"
	"github.com/robertkrimen/otto"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var log slogger.Logger

var Cli struct {
	Inifile  string
	ReadOnly bool
	Debug    bool
}

var Global struct {
	Db             *shared.DbHandle
	Chat           *shared.ChatHandle
	PollInterval   time.Duration
	ReadOnly       bool
	DbSection      string
	Pidfile        string
	ResponseHandle string
	Owner          string
	Instance       string
	Hostname       string
	Scratchdir     string
	SearchBases    []string
	SearchPaths    []string
	JoinChannels   []string
}

var Rx struct {
	StripLink *regexp.Regexp
	CliStrip  *regexp.Regexp
}

/*
create table users (id serial primary key not null, name varchar(30) not null, slackid varchar(12), lastseen timestamp, attributes jsonb);
*/

var ApiArray map[string]*shared.DirectClient

var api *shared.DirectClient
var Config shared.Configuration

func EnsureChatConnection(handle string) *shared.DirectClient {
	if handle == "" {
		log.Errorf("Asked to connect with empty handle '%s'\n", handle)
		return nil
	}
	_, present := ApiArray[handle]
	if present == true {
		return ApiArray[handle]
	}
	found, value := Config.GetString(handle + ".chattype")
	if !found {
		value = "slack"
		log.Debugf("No '%s.chattype' defined; assuming 'slack'.\n", handle)
	}
	switch value {
	case "slack":
		log.Printf("Attempting to connect as handle '%s' via token.\n", handle)
		ApiArray[handle] = Global.Chat.DirectClient
	default:
		log.Errorf("Value for '%s.chattype' was '%s' (no directchat actions available)\n",
			handle, value)
	}
	log.Printf("Returning API key for handle '%s' (type '%s')\n", handle, value)
	return ApiArray[handle]
}

var Setting = make(map[string]map[string]string)

func SetUser(User string, Variable string, Value string) bool {
	if Setting[User] == nil {
		Setting[User] = make(map[string]string)
	}
	Setting[User][Variable] = Value
	UserBuffer, _ := json.Marshal(Setting[User])
	log.Printf("JSON user variables: '%s'\n", UserBuffer)
	res, _ := Global.Db.Exec("UPDATE users SET attributes=$1 WHERE slackid=$2;", UserBuffer, User)
	count, _ := res.RowsAffected()
	if count == 0 {
		uObj := api.ChatTargetUser(User)
		Username := uObj.Name
		_, err := Global.Db.Exec("INSERT INTO users (slackid, lastseen,attributes,name) VALUES ($1, NOW(), $2, $3);", User, UserBuffer, Username)
		if err != nil {
			fmt.Printf("ERROR on insert: '%s'\n", err.Error())
		}
	}
	return true
}

func FormResponse(Stimulus *shared.ResponseTo) {
	if Stimulus == nil {
		return
	}
	if len(Stimulus.LcArguments) == 0 {
		log.Printf("No message at all in form response; returning.\n")
		return
	}
	if len(Stimulus.LcArguments) == 0 {
		log.Printf("No message at all in form response; returning.\n")
		return
	}
	Command := Rx.CliStrip.ReplaceAllString(Stimulus.LcArguments[0], "$1")
	SafeChannel := Rx.CliStrip.ReplaceAllString(Stimulus.Target.Name, "$1")
	SafeUsername := Rx.CliStrip.ReplaceAllString(Stimulus.Sender.Name, "$1")
	lWords := Stimulus.LcArguments[1:]
	Words := Stimulus.Arguments[1:]
	T := sjson.NewJson()
	T["USER"] = SafeUsername
	if Stimulus.IsDM {
		T["CHANNEL"] = "dm.direct"
	} else {
		T["CHANNEL"] = SafeChannel
	}
	var FrontendPaths, DelegatePaths []string
	for _, Path := range Global.SearchPaths {
		T["DIR"] = "frontend"
		FrontendPaths = append(FrontendPaths, T.TemplateString(Path))
		T["DIR"] = "delegate"
		DelegatePaths = append(DelegatePaths, T.TemplateString(Path))
	}
	log.Debugf("Command: %s\nPaths:\n%v\n", Command, FrontendPaths)

	switch Command {
	default:
		SearchFile := Command + ".otto"
		Filename := shared.SearchPath(SearchFile, FrontendPaths)
		if Filename == "" {
			log.Debugf("Sought-after file '%s' not found in provided path.\n", SearchFile)
			return
		}
		dat, err := ioutil.ReadFile(Filename)
		log.FatalIff(err, "File '%s' didn't load!\n", Filename)
		vm := otto.New()
		var PacketFilename string
		Packet := sjson.NewJson()
		Packet["lcargs"] = lWords
		Packet["args"] = Words
		Packet["command"] = Command
		Packet["sender"] = Stimulus.Sender.ChatId
		Packet["senderid"] = Stimulus.Sender.NativeId
		Packet["originaltext"] = Stimulus.Message
		Packet["longterm"] = Stimulus.Sender.PermVal
		Packet["session"] = Stimulus.Sender.TempVal
		Packet["instance"] = Global.Instance
		Packet["host"] = Global.Hostname
		vm.Set("env", Packet)
		vm.Set("genpacket", func(call otto.FunctionCall) otto.Value {
			if PacketFilename == "" {
				PacketFilename = fmt.Sprintf("%s/responder-%s.json", Global.Scratchdir, shared.GenerateRandomStringOrDie(16))
				log.Printf("Writing IPC packet in JSON to '%s'...\n", PacketFilename)
				os.Setenv("RESPONDER_PACKET", PacketFilename)
			}
			err = Packet.WriteToFile(PacketFilename)
			log.ErrorIff(err, "error writing PacketFilename '%s'", PacketFilename)
			val, err := otto.ToValue(string(PacketFilename))
			log.PrintIff(err, "error translating PacketFilename '%s' to value", PacketFilename)
			return val
		})
		vm.Set("chaterror", func(call otto.FunctionCall) otto.Value {
			Global.Chat.SendError(call.Argument(0).String())
			return otto.Value{}
		})
		vm.Set("chatlog", func(call otto.FunctionCall) otto.Value {
			Global.Chat.SendDefaultf("%s", call.Argument(0).String())
			return otto.Value{}
		})
		vm.Set("reply", func(call otto.FunctionCall) otto.Value {
			Stimulus.Target.Sendf("%s", call.Argument(0).String())
			return otto.Value{}
		})
		vm.Set("delegate", func(call otto.FunctionCall) otto.Value {
			SafeCommand := Rx.CliStrip.ReplaceAllString(call.Argument(0).String(), "$1")
			Script := shared.SearchPath(SafeCommand, DelegatePaths)
			if Script == "" {
				log.Printf("Deferred sanitized command '%s' not found in paths %v.\n", SafeCommand, DelegatePaths)
				log.Debugf("Sought-after file '%s' not found in provided path.\n", Filename)
				return otto.NullValue()
			}
			log.Printf("Delegated '%s' (maps to '%s') to script '%s'\n", call.Argument(0).String(), SafeCommand, Script)
			var Params []string
			for k, v := range call.ArgumentList {
				Arg, err := v.ToString()
				if err != nil {
					Arg = ""
					log.Infof("Error '%s' when transcribing param %d ('%v') when delegating to '%s'\n", err, k, v, Script)
				} else {
					log.Debugf("Successfully transcribed arg %d to '%s'\n", k, Arg)
				}
				if k > 0 {
					Params = append(Params, Arg)
				}
			}
			log.Infof("Replication command: RESPONDER_PACKET=%s %s %s\n", PacketFilename, Script, strings.Join(Params, " "))
			out, err := exec.Command(Script, Params...).Output()
			if err != nil {
				log.PrintIff(err, "delegated command '%s' execution error when '%s' ran '%s' on '%s'\n", Script, SafeUsername, SafeCommand, SafeChannel)
			}
			Global.Chat.SendErrorf("User '%s' command '%s' failed with message '%s'\n", SafeUsername, SafeCommand, err)
			val, err := otto.ToValue(string(out))
			log.PrintIff(err, "error translating output to value")
			return val
		})
		_, err = vm.Run(string(dat))
		if err != nil {
			Global.Chat.SendErrorf("User '%s' ran '%s', and triggered error %s", Stimulus.Sender, Filename, err)
			return
		}
	}
}

func LoadConfigValues(inifile string) {
	log = *(slogger.NewLogger())
	slogger.SetLogger(&log)
	sjson.SetLogger(&log)
	shared.SetLogger(&log)
	OtherIni := flag.String("inifile", "", "Specify an INI file for settings")
	ReadOnly := flag.Bool("readonly", false, "No writes to database or filesystem.")
	Debug := flag.Bool("debug", false, "Enable verbose debugging.")
	Secret := flag.Bool("secret", false, "Enable secret debugging.")
	flag.Parse()
	Cli.Inifile = *OtherIni
	Cli.ReadOnly = *ReadOnly
	Cli.Debug = *Debug
	if Cli.Debug {
		log.SetThreshold(slogger.DEBUG)
	}
	if *Secret {
		log.SetThreshold(slogger.SECRET)
	}
	Global.ReadOnly = Cli.ReadOnly
	if Cli.Inifile != "" {
		inifile = Cli.Inifile
	}
	log.Debugf("Loading INI file from '%s'\n", inifile)
	Config.SetDefaultIni(inifile).OrDie("Could not access ini '%s'\n", inifile)
	Global.Pidfile = Config.GetStringOrDefault("responder.pidfile", "/data/statefiles/responder.pid", "")
	shared.ExitIfPidActive(Global.Pidfile)
	Global.DbSection = Config.GetStringOrDie("responder.dbhandle", "No database handle defined")
	Searches := Config.GetStringOrDie("responder.searchbase", "Base directory(ies) undefined; comma separate for multiples")
	Global.SearchBases = strings.Split(Searches, ",")
	Global.Hostname, _ = os.Hostname()
	Global.Instance = Config.GetStringOrDefault("responder.instance", Global.Hostname, "No instance specified; defaulting to hostname")
	Global.ResponseHandle = Config.GetStringOrDefault("responder.responsehandle", "responder", "")
	found, sChannels := Config.GetString("responder.autojoin")
	if found {
		Channels := strings.Split(sChannels, ",")
		for _, v := range Channels {
			v = strings.Trim(v, " ")
			Global.JoinChannels = append(Global.JoinChannels, v)
		}
	} else {
		log.Printf("No channels specified in responder.autojoin. This may be a problem in XMPP.\n")
	}
	Global.Scratchdir = Config.GetStringOrDie("responder.scratchdir", "You must specify a directory for temporary files used for IPC handoff.\n")
}

func InitAndConnect() {
	ApiArray = make(map[string]*shared.DirectClient)
	//api = EnsureChatConnection(Global.ResponseHandle)
	//log.Printf("API connection defined as '%s'\n", api.Identifier())
	Global.Chat = Config.NewChatHandle(Global.ResponseHandle)
	if Global.Chat.ErrorChannel == nil {
		log.Fatalf("Error channel must be defined for the responder.\n")
	}
	if Global.Chat.DirectClient == nil {
		log.Fatalf("Direct client must be defined for the responder.\n")
	}
	Global.Db = Config.ConnectDbBySection(Global.DbSection).OrDie()
	//directchat.NewXmpp(nil)
	Rx.StripLink = regexp.MustCompile(`(?ms)<.*\|(.*?)>`)
	Rx.CliStrip = regexp.MustCompile(`[^\$\.a-z\-_A-Z0-9]+`)
	T := sjson.NewJson()
	T["HOST"] = Global.Hostname
	T["INSTANCE"] = Global.Instance
	for _, dir := range Global.SearchBases {
		Global.SearchPaths = append(Global.SearchPaths, T.TemplateString(dir+"/${DIR}_priv/${USER}"))
		Global.SearchPaths = append(Global.SearchPaths, T.TemplateString(dir+"/${DIR}/${CHANNEL}"))
		Global.SearchPaths = append(Global.SearchPaths, T.TemplateString(dir+"/${DIR}/"))
	}
	Global.PollInterval = time.Second / 4
	log.Debugf("Templated base search paths are:\n%v\n", Global.SearchPaths)
}

func main() {
	LoadConfigValues("/data/config/responder.ini")
	InitAndConnect()
	//err := Global.Chat.SendErrorf("Responder '%s' booted up on %s", Global.Instance, Global.Hostname)
	//log.FatalIff(err, "Failed to send initial bootup message")
	log.Printf("Making connection to Slack endpoint '%s'\n", "(default)")

	api = EnsureChatConnection(Global.ResponseHandle)
	if api == nil {
		Global.Chat.SendErrorf("Couldn't establish connection for handle '%s'; can't read input through it.", Global.ResponseHandle)
		log.Fatalf("Unable to establish connection for handle '%s'\n", Global.ResponseHandle)
	}
	Source, err := Global.Chat.NewListener()
	log.FatalIff(err, "%s", Global.Chat.Identifier())
	for _, Join := range Global.JoinChannels {
		Target := api.ChatTargetChannel(Join)
		log.Printf("Join '%s': '%+v'\n", Join, Target)
		log.ErrorIff(api.Join(Target), "error joining '%s'\n", Target.Id)
	}
	//	go Source.ManageConnection()
	Global.Chat.SendErrorf("This is handle '%s'\n", Global.Chat.Identifier())
	for true {
		log.Printf("Actually starting loop through Source on %s.\n", api.Identifier())
		for msg := range Source.Incoming {
			//log.Printf("Message Received: %s\n", msg)
			switch ev := msg.(type) {
			case *shared.MessagerEvent:
				log.Printf("Event data: %+v\n", ev)
				Stimulus := ev.ToStimulus()
				log.Printf("Stimulus: %+v\n", Stimulus)
				go FormResponse(Stimulus)
			case *error:
				log.Printf("Error: %s\n", ev)

			case *shared.InvalidAuthEvent:
				log.Fatalf("Invalid credentials")
				return

			case *shared.ConnectingEvent:
				log.Printf("Attempting connection...")

			default:
				// Ignore other events..
				log.Printf("Unexpected: %+v\n", msg)
			}
		}
		time.Sleep(Global.PollInterval)
	}
}
