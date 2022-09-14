package main

/*

create table postedmails (id serial primary key, seenat timestamp with time zone, label varchar(60) not null, mailid varchar(120) not null);
create table mailparsers (id serial not null primary key, label varchar(30), chathandle varchar(20), channel varchar(80), sender varchar(80), enabled bool default true, code text);

*/

import (
	"database/sql"
	"flag"
	"fmt"
	_ "github.com/grammaton76/g76golib/chatoutput/sc_dbtable"
	"github.com/grammaton76/g76golib/shared"
	"github.com/grammaton76/g76golib/simage"
	"github.com/grammaton76/g76golib/sjson"
	"github.com/grammaton76/g76golib/slogger"
	"github.com/microcosm-cc/bluemonday"
	"github.com/robertkrimen/otto"
	"github.com/veqryn/go-email/email"
	"github.com/wxdao/go-imap/imap"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"time"
)

var log slogger.Logger

var wQ struct {
	CreateMailScanpoint  *shared.Stmt
	UpdateMailScanpoint  *shared.Stmt
	MarkMailAsHandled    *shared.Stmt
	DeleteOldHandledRecs *shared.Stmt
}

var rQ struct {
	GetMailScanpoints *shared.Stmt
	CheckMailId       *shared.Stmt
	GetCodeForLabel   *shared.Stmt
}

var Rx struct {
	StripLink      *regexp.Regexp
	CliStrip       *regexp.Regexp
	InsideBrackets *regexp.Regexp
}

var Config shared.Configuration

var Cli struct {
	Inifile     string
	SkipPid     bool
	Debug       bool
	OfflineJson *sjson.JSON
}

var Parameter struct {
	jsondir string
}

type MailMessage struct {
	Account       string
	Uid           string
	Sender        string
	Recipient     string
	MessageId     string
	Subject       string
	ActionRequest string
	TextBody      string
	HtmlBody      string
	Body          *string
	Raw           string
	DateStr       string
	Date          time.Time
	Sanitizer     int
	Messages      []email.Message
}

type MailWatch struct {
	Label        string
	FromSubstr   string
	ParserPath   string
	Sender       string
	sChatHandle  string
	Channel      string
	TriggerAfter time.Time
	Target       *shared.ChatTarget
	AlreadySeen  map[string]bool
	Retained     map[string]string
	InDb         bool
}

type MailWatches []*MailWatch

type MailProtocol int

const (
	PROTO_UNDEF MailProtocol = 0
	PROTO_POP3  MailProtocol = 1
	PROTO_IMAPS MailProtocol = 2
)

type MailAccount struct {
	Id       int
	Protocol MailProtocol
	Hostname string
	Username string
	Password string
	Watches  MailWatches
}

var Global struct {
	Sanitizer     *bluemonday.Policy
	ExtractMailTo string
	ReadOnly      bool
	TestOnly      string
	MailAccounts  string
	MailDb        *shared.DbHandle
	Mailboxes     []*MailAccount
	SearchBases   []string
	SearchPaths   []string
	Hostname      string
	ScratchDir    string
	sChatHandle   string
	MinutesToRun  int
	Chat          *shared.ChatHandle
	Watches       map[string]*MailWatch
}

func LoadConfigValues(inifile string) {
	simage.SetLogger(&log)
	shared.SetLogger(&log)
	OtherIni := flag.String("inifile", "", "Specify an INI file for settings")
	SkipPid := flag.Bool("skippid", false, "Skip the PID file check")
	TestOnly := flag.String("testonly", "", "Designate a specific function to test")
	Debug := flag.Bool("debug", false, "Enable verbose debugging.")
	MailAccounts := flag.String("mailaccounts", "", "ini stanzas defining the accounts (comma).")
	ExtractMailTo := flag.String("extractmailto", "", "Extract the matched email as [prefix]")
	JsonMode := flag.Bool("jsonmode", false, "If set, we're doing offline tests against json from stdin.")
	flag.Parse()
	Cli.Inifile = *OtherIni
	Global.TestOnly = *TestOnly
	Cli.Debug = *Debug
	if *SkipPid {
		Cli.SkipPid = true
	}
	if Cli.Debug {
		log.SetThreshold(slogger.DEBUG)
	}
	if Cli.Inifile != "" {
		inifile = Cli.Inifile
	}
	if *JsonMode {
		Global.ReadOnly = true
		readText, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			log.Fatalf("failed to read stdin: %s", err)
		}
		Input := sjson.NewJson()
		err = Input.IngestFromBytes(readText)
		log.ErrorIff(err, "error ingesting json from stdin")
		Cli.OfflineJson = &Input
		return
	}
	Global.Hostname, _ = os.Hostname()
	Config.LoadAnIni(inifile).OrDie("Could not load ini file '%s'\n", inifile)
	_, Parameter.jsondir = Config.GetString("mailscan.jsondir")
	if *ExtractMailTo != "" {
		Global.ExtractMailTo = *ExtractMailTo
	} else {
		_, Global.ExtractMailTo = Config.GetString("mailscan.extractprefix")
	}
	_, Global.MinutesToRun = Config.GetInt("mailscan.minutestorun")
	Global.sChatHandle = Config.GetStringOrDie("mailscan.chathandle", "Must specify a chat handle for error and diags messages.\n")
	SearchPaths := Config.GetStringOrDie("mailscan.searchpaths", "Must specify a place to search for the mail handlers.\n")
	Global.SearchPaths = strings.Split(SearchPaths, ",")
	Global.ScratchDir = Config.GetStringOrDie("mailscan.scratchdir", "ScratchDir must be defined.\n")
	Global.MailAccounts = *MailAccounts
	if Global.MailAccounts == "" {
		Global.MailAccounts = Config.GetStringOrDie("mailscan.accounts", "If you do not specify an account on the command line, it must be in the config.\n")
	}
	for _, Base := range strings.Split(Global.MailAccounts, ",") {
		Proto := Config.GetStringOrDefault(Base+".protocol", "imap", "No protocol specified; defaulting to imap.\n")
		Watches := Config.GetStringOrDie(Base+".watches", "No watches specified on section '%s'\n", Base)

		Account := &MailAccount{
			Id:       0,
			Protocol: PROTO_IMAPS,
			Hostname: Config.GetStringOrDie(Base+".server", "Server is required.\n"),
			Username: Config.GetStringOrDie(Base+".username", "Username is required.\n"),
			Password: Config.GetStringOrDie(Base+".password", "Password is required.\n"),
		}
		switch Proto {
		case "pop3":
			Account.Protocol = PROTO_POP3
		}
		if Account.Protocol != PROTO_UNDEF {
			for _, WatchName := range strings.Split(Watches, ",") {
				Sender := Config.GetStringOrDie(WatchName+".sender", "")
				Parser := Config.GetStringOrDie(WatchName+".parser", "")
				ChatHandle := Config.GetStringOrDie(WatchName+".chathandle", "")
				Channel := Config.GetStringOrDie(WatchName+".channel", "")
				Caw := &MailWatch{
					Label:       WatchName,
					FromSubstr:  Sender,
					ParserPath:  Parser,
					sChatHandle: ChatHandle,
					Channel:     Channel,
					AlreadySeen: make(map[string]bool),
					Retained:    make(map[string]string),
				}
				Account.Watches = append(Account.Watches, Caw)
			}
			Global.Mailboxes = append(Global.Mailboxes, Account)
		} else {
			log.Printf("Undefined mail protocol '%s'\n", Proto)
		}
	}
}

func InitAndConnect() {
	shared.SetLogger(&log)
	Global.Sanitizer = bluemonday.StrictPolicy()
	ThreadPurpose := shared.NewThreadPurpose()
	go ThreadPurpose.SetDeadman(5 * time.Second)
	Rx.InsideBrackets = regexp.MustCompile(`(?ms)<(.*?)>`)
	Rx.StripLink = regexp.MustCompile(`(?ms)<.*\|(.*?)>`)
	Rx.CliStrip = regexp.MustCompile(`[^$.a-z@\-_A-Z0-9]+`)
	Global.Watches = make(map[string]*MailWatch)
	if Cli.OfflineJson != nil {
		Global.Chat = &shared.ChatHandle{PrintChatOnly: true}
		return
	}
	T := sjson.NewJson()
	T["HOST"] = Global.Hostname
	for _, dir := range Global.SearchBases {
		Global.SearchPaths = append(Global.SearchPaths, T.TemplateString(dir+"/${DIR}_priv/${SENDER}"))
	}
	ThreadPurpose.WgAdd(1)
	go func(fwg *shared.ThreadPurpose) {
		defer ThreadPurpose.Done()
		ThreadPurpose.Set("Database setup thread for mailscan.db")
		Global.MailDb = Config.ConnectDbKey("mailscan.db").OrDie()
		rQ.GetMailScanpoints = Global.MailDb.PrepareOrDie(
			`SELECT label, lastactive FROM mailwatch;`)
		rQ.GetCodeForLabel = Global.MailDb.PrepareOrDie(
			`SELECT id,code FROM mailparsers WHERE label=$1 AND enabled=true LIMIT 1;`)
		rQ.CheckMailId = Global.MailDb.PrepareOrDie(
			`SELECT 1 FROM postedmails WHERE label = $1 AND mailid = $2;`)
		if Global.ReadOnly == false {
			wQ.CreateMailScanpoint = Global.MailDb.PrepareOrDie(
				`INSERT INTO mailwatch (label, lastactive) VALUES ($1, $2);`)
			wQ.UpdateMailScanpoint = Global.MailDb.PrepareOrDie(
				`UPDATE mailwatch SET lastactive=$1 WHERE label=$2;`)
			wQ.MarkMailAsHandled = Global.MailDb.PrepareOrDie(
				`INSERT INTO postedmails (seenat,label,mailid, retained) VALUES (NOW(), $1, $2, $3);`)
			wQ.DeleteOldHandledRecs = Global.MailDb.PrepareOrDie(
				`DELETE FROM postedmails WHERE seenat<(NOW()-'1 WEEK'::interval);`)
		}
	}(ThreadPurpose)
	ThreadPurpose.WgWait()
	ThreadPurpose.DisarmDeadman()
	if !Global.ReadOnly {
		//shared.ValidatePreparedQueriesOrDie(wQ)
	}
	//shared.ValidatePreparedQueriesOrDie(rQ)

	Global.Chat = Config.NewChatHandle(Global.sChatHandle).OrDie("failed to connect")

	for _, Mbox := range Global.Mailboxes {
		for _, Watch := range Mbox.Watches {
			Global.Watches[Watch.Label] = Watch
			Handle := Config.NewChatHandle(Watch.sChatHandle)
			Watch.Target = Handle.ChatTarget(Watch.Channel).OrDie("")
		}
	}
}

func (Acct *MailAccount) Hash() string {
	return fmt.Sprintf("%s@%s", Acct.Username, Acct.Hostname)
}

func (Msg *MailMessage) Summary() string {
	return fmt.Sprintf("[%s] %s -> %s", Msg.Date, Msg.Sender, Msg.Subject)
}

func (Msg *MailMessage) Identifier() string {
	return fmt.Sprintf("%s", Msg.Uid)
}

func (Msg *MailMessage) WriteOut(Base string) (string, error) {
	Filename := fmt.Sprintf("%s%s.txt", Base, Msg.Uid)
	err := ioutil.WriteFile(Filename, []byte(Msg.Raw), 0600)
	return Filename, err
}

func ExtractFromBrackets(X string) string {
	if X == "" {
		return ""
	}
	Match := Rx.InsideBrackets.FindStringSubmatch(X)
	if len(Match) != 0 {
		return Match[1]
	}
	return X
}

func NewMessageSummary(Msg string) *MailMessage {
	var M MailMessage
	r := strings.NewReader(Msg)
	m, err := email.ParseMessage(r)
	if err != nil {
		log.Fatalf("%s", err)
	}
	h := m.Header
	M.Subject = h.Get("Subject")
	M.Sender = ExtractFromBrackets(h.Get("From"))
	M.Recipient = ExtractFromBrackets(h.Get("To"))
	M.Uid = ExtractFromBrackets(h.Get("Message-Id"))
	M.DateStr = h.Get("Date")
	if M.DateStr != "" {
		M.Date, err = ParseMailTime(M.DateStr)
		if err != nil {
			log.Errorf("msg %s from '%s' with subject '%s': %s\n",
				M.Uid, M.Sender, M.Subject, err)

		}
	}
	for _, part := range m.MessagesAll() {
		mediaType, params, err := part.Header.ContentType()
		if err != nil {
			log.Errorf("Mail parsing error: %s\n", err)
			continue
		}
		switch mediaType {
		case "text/plain":
			M.TextBody = string(part.Body)
			M.Body = &M.TextBody
		case "text/html":
			M.HtmlBody = string(part.Body)
			if M.Body == nil {
				M.Body = &M.HtmlBody
			}
		case "multipart/alternative":
			log.Debugf("multipart/alternative section identified. This mail likely has html+text.\n")
		default:
			log.Printf("Mediatype: %s\n", mediaType)
			log.Printf("Params:\n%+v\n\n", params)
		}
	}
	//bBody, err := ioutil.ReadAll(m.Body)
	//M.Body = string(bBody)
	//if err != nil {
	//	log.Fatalf("%s", err)
	//}
	M.Raw = Msg
	return &M
}

func ParseMailTime(sTime string) (time.Time, error) {
	var Date time.Time
	var err error
	for _, v := range []string{
		"Mon, 2 Jan 2006 15:04:05 -0700 (MST)",
		"2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 +0000",
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 MST",
	} {
		Date, err = time.Parse(v, sTime)
		if err == nil {
			return Date, nil
		}
	}
	log.Fatalf("failed to parse string time '%s'\n", sTime)
	return time.Time{}, fmt.Errorf("failed to parse string time '%s'\n", sTime)
}

func (Acct *MailAccount) Identifier() string {
	return fmt.Sprintf("%s@%s", Acct.Username, Acct.Hostname)
}

func (Acct *MailAccount) ImapScan() {
	Parsers := make(map[string]string)

	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, os.Kill)

	updated := make(chan int)

loop:
	for {
		client, err := imap.DialTLS(Acct.Hostname, nil)
		if err != nil {
			log.Fatalf("Couldn't TCP dial to '%s': %s\n", Acct.Hostname, err)
		}
		client.UpdateCallback = func() {
			updated <- 1
		}
		//err = client.StartTLS("imap.gmail.com")
		log.FatalIff(err, "StartTLS failure")
		err = client.Login(Acct.Username, Acct.Password)
		log.FatalIff(err, "Login failure")
		_, err = client.Select("INBOX")
		log.FatalIff(err, "Error selecting INBOX")
		log.Infof("Mailbox rescan for %s\n", Acct.Identifier())
		TwoDaysAgo := time.Now().Add(-2 * 24 * time.Hour).Format("2-Jan-2006")
		var seqs []int
		seqs, err = client.Search(
			fmt.Sprintf(`SINCE "%s"`, TwoDaysAgo))
		if err != nil {
			Global.Chat.SendErrorf("Couldn't issue search for '%s': %s\n", Acct.Username, err)
			log.Fatalf("Couldn't issue search for '%s': %s\n", Acct.Username, err)
		}
		log.Printf("There are %d mails for %s.\n", len(seqs), Acct.Username)
		if len(seqs) > 0 {
			var result map[int]*imap.FetchResult
			result, err = client.FetchRFC822(seqs, true)
			if err != nil {
				log.Fatalf("Couldn't fetch mails for '%s': %s\n", Acct.Username, err)
			}
			for _, msg := range result {
				Msg := NewMessageSummary(string(msg.Data))
				var Inspected bool
				for _, Watch := range Global.Watches {
					if Watch.AlreadySeen[Msg.Uid] {
						Inspected = true
						continue
					}
					if strings.Index(Msg.Sender, Watch.FromSubstr) == -1 {
						log.Infof("%s (wants '%s') not scanning %s from %s\n",
							Watch.Label, Watch.FromSubstr, Msg.Uid, Msg.Sender)
						// In case criteria is readjusted, we non-persistently flag this to ignore.
						Watch.AlreadySeen[Msg.Uid] = true
						Inspected = true
						continue
					}
					Inspected = true
					var Matches int64
					err = rQ.CheckMailId.QueryRow(Watch.Label, Msg.Uid).Scan(&Matches)
					if Matches == 1 {
						log.Printf("Db indicates '%s' already processed '%s'; skipping.\n",
							Watch.Label, Msg.Uid)
						Watch.AlreadySeen[Msg.Uid] = true
						continue
					}
					log.Printf("Watch '%s' inspecting email from %s: '%s' %+v\n", Watch.Label, Msg.Sender, Msg.Uid, Msg.Summary())
					if Global.ExtractMailTo != "" {
						File, err := Msg.WriteOut(Global.ExtractMailTo)
						Watch.Retained[Msg.Uid] = File
						log.FatalIff(err, "Failed to extract mail '%s'\n", Msg.Identifier())
					}
					if Parsers[Watch.Label] == "" {
						var R struct {
							id   int
							code string
						}
						err = rQ.GetCodeForLabel.QueryRow(Watch.Label).Scan(&R.id, &R.code)
						log.FatalIff(err, "Failed to get code for label '%s'\n", Watch.Label)
						Parsers[Watch.Label] = R.code
					}
					log.Infof("%s scanning %s from %s\n",
						Watch.Label, Msg.Uid, Msg.Sender)
					Watch.ExecJSForMail(Parsers[Watch.Label], Msg)

					var res sql.Result
					log.Printf("Updating mail scan point for '%s' to '%s'\n", Watch.Label, Msg.Date.String())
					res, err = wQ.UpdateMailScanpoint.Exec(shared.FormatMysqlTime(Msg.Date), Watch.Label)
					log.ErrorIff(err, "Error updating last-mail point on message '%s' for watch '%s'\n", Msg.Uid, Watch.Label)
					Rows, err := res.RowsAffected()
					log.ErrorIff(err, "Error fetching affected row count from update of mailscan table for watch '%s'\n", Watch.Label)
					if Rows != 1 {
						log.Fatalf("We affected %d rows (not 1) when updating mailwatch lastseen point for label '%s' \n", Rows, Watch.Label)
					}
					Watch.AlreadySeen[Msg.Uid] = true
					_, err = wQ.MarkMailAsHandled.Exec(Watch.Label, Msg.Uid, Watch.Retained[Msg.Uid])
					log.ErrorIff(err, "Error inserting postedmails record '%s' for watch '%s'\n", Msg.Uid, Watch.Label)
				}
				if !Inspected {
					log.Printf("No filters wanted email from %s: '%s' %+v\n", Msg.Sender, Msg.Uid, Msg.Summary())
				}
			}
		}
		go client.Idle()
		select {
		case <-updated:
			err = client.Done()
			log.ErrorIff(err, "%s: client.Done() updated")
		case <-time.After(time.Minute * 10):
			err = client.Done()
			log.ErrorIff(err, "%s: client.Done() timeout")
		case <-interrupted:
			log.Printf("Interrupt received; exiting.\n")
			break loop
		}
	}
	log.Printf("terminated thread checking %s\n", Acct.Identifier())
	os.Exit(0)
}

func (Watch *MailWatch) ExecJSForMail(Code string, Msg *MailMessage) {
	var err error
	log.Printf("Going to parse over the following message: %s\n", Msg.Summary())
	if Msg == nil {
		return
	}
	SafeSender := Rx.CliStrip.ReplaceAllString(Msg.Sender, "$1")
	T := sjson.NewJson()
	T["SENDER"] = SafeSender
	var FrontendPaths, DelegatePaths []string
	for _, Path := range Global.SearchPaths {
		T["DIR"] = "frontend"
		FrontendPaths = append(FrontendPaths, T.TemplateString(Path))
		T["DIR"] = "delegate"
		DelegatePaths = append(DelegatePaths, T.TemplateString(Path))
	}
	log.Debugf("Exec on email '%s' from: %s\nPaths:\n%v\n", Msg.Uid, SafeSender, FrontendPaths)

	var Response shared.SegmentedMsg
	switch SafeSender {
	default:
		vm := otto.New()
		var PacketFilename string
		Packet := sjson.NewJson()
		Packet["host"] = Global.Hostname
		err = vm.Set("env", Packet)
		log.ErrorIff(err, "setting Otto VM variable 'env'")

		err = vm.Set("msg", Msg)
		log.ErrorIff(err, "setting Otto VM variable 'msg'")

		err = vm.Set("retain", func(call otto.FunctionCall) otto.Value {
			if Global.ReadOnly {
				log.Printf("Read-only mode is engaged, but we would've written the file to %s\n", call.Argument(0).String())
			} else {
				File, err := Msg.WriteOut(call.Argument(0).String())
				Watch.Retained[Msg.Uid] = File
				log.FatalIff(err, "Failed to extract mail '%s'\n", Msg.Identifier())
			}
			return otto.Value{}
		})
		log.ErrorIff(err, "setting Otto VM function 'retain'")

		err = vm.Set("flagthis", func(call otto.FunctionCall) otto.Value {
			if Global.ReadOnly {
				log.Printf("Read-only mode is engaged, but we would've written the file to %s\n", call.Argument(0).String())
			} else {
				File, err := Msg.WriteOut(call.Argument(0).String())
				Watch.Retained[Msg.Uid] = File
				log.FatalIff(err, "Failed to extract mail '%s'\n", Msg.Identifier())
			}
			return otto.Value{}
		})
		log.ErrorIff(err, "setting Otto VM function 'retain'")

		err = vm.Set("genpacket", func(call otto.FunctionCall) otto.Value {
			if PacketFilename == "" {
				PacketFilename = fmt.Sprintf("%s/responder-%s.json", Global.ScratchDir, shared.GenerateRandomStringOrDie(16))
				log.Printf("Writing IPC packet in JSON to '%s'...\n", PacketFilename)
				err = os.Setenv("RESPONDER_PACKET", PacketFilename)
				log.ErrorIff(err, "Failed to set responder_packet env")
			}
			err = Packet.WriteToFile(PacketFilename)
			log.ErrorIff(err, "Failed to write to file '%s'", PacketFilename)
			val, err := otto.ToValue(string(PacketFilename))
			log.PrintIff(err, "error translating PacketFilename '%s' to value", PacketFilename)
			return val
		})
		log.ErrorIff(err, "setting Otto VM function 'genpacket'")

		err = vm.Set("chaterror", func(call otto.FunctionCall) otto.Value {
			Global.Chat.SendError(call.Argument(0).String())
			return otto.Value{}
		})
		log.ErrorIff(err, "setting Otto VM function 'chaterror'")

		err = vm.Set("chatlog", func(call otto.FunctionCall) otto.Value {
			Global.Chat.SendDefaultf("%s", call.Argument(0).String())
			return otto.Value{}
		})
		log.ErrorIff(err, "setting Otto VM function 'chatlog'")

		err = vm.Set("reply", func(call otto.FunctionCall) otto.Value {
			Global.Chat.SendDefaultf("%s", call.Argument(0).String())
			return otto.Value{}
		})
		log.ErrorIff(err, "setting Otto VM function 'reply'")

		err = vm.Set("exiteval", func(call otto.FunctionCall) otto.Value {
			return otto.Value{}
		})
		err = vm.Set("addresponse", func(call otto.FunctionCall) otto.Value {
			Type := call.Argument(0).String()
			Content := call.Argument(1).String()
			Seg := shared.MsgSegment{
				Text: Content,
			}
			switch Type {
			case "text":
				Seg.Itemtype = shared.SEGTYPE_TEXT
			case "imgurl":
				Seg.Itemtype = shared.SEGTYPE_IMGURL
			case "label":
				Seg.Itemtype = shared.SEGTYPE_LABEL
			default:
				log.Fatalf("Unknown type '%s' specified (value '%s').\n", Type, Content)
			}
			log.Printf("Response segment: %s\n", Seg.String())
			Response = append(Response, Seg)
			return otto.Value{}
		})
		log.ErrorIff(err, "setting Otto VM function 'addresponse'")

		err = vm.Set("delegate", func(call otto.FunctionCall) otto.Value {
			SafeCommand := Rx.CliStrip.ReplaceAllString(call.Argument(0).String(), "$1")
			log.Printf("Attempting delegation of '%s' (maps to '%s') in paths %v\n", call.Argument(0).String(), SafeCommand, DelegatePaths)
			Script := shared.SearchPath(SafeCommand, DelegatePaths)
			if Script == "" {
				log.Printf("Deferred sanitized command '%s' not found in provided path.\n", SafeCommand)
				log.Debugf("Sought-after file '%s' not found in provided path.\n", SafeCommand)
				return otto.NullValue()
			}
			var Params []string
			for k, v := range call.ArgumentList {
				Arg, err := v.ToString()
				//log.Printf("Caw: '%s'; '%s'\n", Arg, v.IsString())
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
			log.Printf("Passing the following parameters: %v\n", Params)
			out, err := exec.Command(Script, Params...).Output()
			if err != nil {
				log.PrintIff(err, "delegated command '%s' execution error when '%s' ran '%s'\n", Script, SafeSender, SafeCommand)
				err = Global.Chat.SendErrorf("Sender '%s' command '%s' failed with message '%s'\n", SafeSender, SafeCommand, err)
				log.ErrorIff(err, "Failed on Global.Chat.SendErrorf:")
			}
			val, err := otto.ToValue(string(out))
			log.PrintIff(err, "error translating output to value")
			return val
		})
		log.ErrorIff(err, "setting Otto VM function 'delegate'")

		res, err := vm.Run(Code)
		if err != nil {
			err = Global.Chat.SendErrorf("User '%s' ran '%s', and triggered error %s", Msg.Sender, Watch.ParserPath, err)
			log.ErrorIff(err, "Failed on Global.Chat.SendErrorf:")
			log.Printf("Partial results: %s\n", res)
			return
		}
		if len(Response) == 0 {
			log.Printf("Message '%s' resulted in no message segments.\n",
				Msg.Uid)
		} else {
			if Watch.Target == nil {
				Watch.Target = Global.Chat.ErrorChannel
			}
			ChatMsg := shared.NewChatMessage()
			ChatMsg.Segments = Response
			_, err = Watch.Target.Send(ChatMsg)
			log.FatalIff(err, "Sending final chat to '%s'", Watch.Target.Identifier())
		}
		//log.Printf("Result: %+v\n", res)
		//log.ErrorIff(os.Remove(PacketFilename), "unable to remove data packet file '%s' post-execution", PacketFilename)
	}
}

func main() {
	LoadConfigValues("/data/baytor/mailscan.ini")
	InitAndConnect()
	//	defer Global.MailDb.Close()
	//	defer Global.GsoDb.Close()
	if Cli.OfflineJson != nil {
		J := Cli.OfflineJson
		Js := J.KeyString("jscode")
		if Js == "" {
			log.Fatalf("No 'jscode' key present in incoming json.\n")
		}
		Mail := J.KeyString("email")
		if Mail == "" {
			log.Fatalf("No 'email' key present in incoming json.\n")
		}
		Msg := NewMessageSummary(Mail)
		Watch := &MailWatch{
			FromSubstr: "",
			Target:     nil,
		}
		Watch.ExecJSForMail(Js, Msg)
		log.Printf("Remember, a nil chat target is EXPECTED when running direct off of a file!\n")
		os.Exit(3)
	}
	if !Cli.SkipPid {
		shared.ExitIfPidActive("/data/statefiles/mailscan.pid")
	}
	res, err := rQ.GetMailScanpoints.Query()
	log.ErrorIff(err, "Failed to read mailscan")
	if Global.MinutesToRun != 0 {
		go func() {
			log.Printf("Started termination thread, waiting %d minutes per config.\n", Global.MinutesToRun)
			time.Sleep(time.Duration(Global.MinutesToRun) * time.Minute)
			log.Printf("*** Scheduled termination of mail scanner after max runtime of %d minutes (check ini if this is unexpected).\n",
				Global.MinutesToRun)
			os.Exit(0)
		}()
	}
	for res.Next() {
		var r struct {
			label       string
			slastactive string
			lastactive  *time.Time
		}
		err = res.Scan(&r.label, &r.slastactive)
		log.ErrorIff(err, "Failed to scan rows on mailscan.\n")
		r.lastactive, err = shared.ParseMysqlTime(r.slastactive)
		var Found bool
		for _, Watch := range Global.Watches {
			if Watch.Label == r.label {
				Found = true
				Watch.TriggerAfter = *r.lastactive
				Watch.InDb = true
				break
			}
		}
		if !Found {
			log.Debugf("No watch matches label '%s'\n", r.label)
		}
	}
	for _, v := range Global.Watches {
		if !Global.Watches[v.Label].InDb {
			_, err := wQ.CreateMailScanpoint.Exec(v.Label, "01-01-01")
			log.FatalIff(err, "No db-side watch for configured watch label '%s', and creation failed", v.Label)
			log.Printf("Created db-side watch for label '%s'\n", v.Label)
		} else {
			log.Debugf("We seem to have a valid watch for label '%s'\n", v.Label)
		}
	}
	for _, Mbox := range Global.Mailboxes {
		go Mbox.ImapScan()
	}
	var Retention sql.Result
	var Rows int64
	for true {
		Retention, err = wQ.DeleteOldHandledRecs.Exec()
		log.ErrorIff(err, "Error performing data retention on postedmails\n")
		if Retention != nil {
			Rows, err = Retention.RowsAffected()
			log.ErrorIff(err, "Error fetching affected row count from data retention pruning of \n")
			log.Infof("Data retention: %d rows deleted from postedmails.\n", Rows)
		}
		time.Sleep(time.Hour)
	}
}
