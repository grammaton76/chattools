package sc_slack

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/grammaton76/g76golib/shared"
	"github.com/grammaton76/g76golib/sjson"
	"github.com/grammaton76/g76golib/slogger"
	"github.com/slack-go/slack"
	reallog "log"
	"os"
	"regexp"
	"strings"
)

type SlackClient struct {
	*slack.Client
	hasconnected bool
	UserIndex    struct {
		BySlackid map[string]*UserDataStore
		UsersById map[string]*UserDataStore
	}
	ChannelIndex struct {
		ChannelsById   map[string]*slack.Channel
		ChannelsByName map[string]*slack.Channel
	}
}

type MsgOption struct {
	slack.MsgOption
}

type UserDataStore struct {
	Ignore    bool
	IsAdmin   bool
	SlackUser *slack.User
	Email     string
	PermVal   sjson.JSON
	TempVal   sjson.JSON
}

var rx struct {
	StripLink *regexp.Regexp
	CliStrip  *regexp.Regexp
}

var log *slogger.Logger

func SetLogger(l *slogger.Logger) *slogger.Logger {
	log = l
	return l
}

func init() {
	log = shared.GetLogger()
	rx.StripLink = regexp.MustCompile(`(?ms)<.*\|(.*?)>`)
	CTC := shared.ChatTypeConfig{
		BindFunc: BindToHandle,
	}
	shared.ChatHandleConfigs["slack"] = &CTC
}

func evFormulateStimulus(ev *shared.MessagerEvent) *shared.ResponseTo {
	cth := ev.Connection.(*shared.ChatHandle)
	var Stimulus shared.ResponseTo = shared.ResponseTo{
		MsgId:         ev.MsgId,
		InThread:      ev.ParentMsgId,
		NativeMessage: ev,
		OrigMessage:   ev.Text,
		Message:       rx.StripLink.ReplaceAllString(ev.Text, "$1"),
		Arguments:     strings.Fields(ev.Text),
		LcArguments:   strings.Fields(strings.ToLower(ev.Text)),
	}
	Sender := cth.UserById(ev.SenderUserId)
	if Sender != nil {
		Stimulus.Sender = Sender
		log.Debugf("Stimulus formulated: user '%s' was sender, admin flag %t.\n", Sender.Name, Stimulus.FromAdmin)
	} else {
		log.Debugf("Stimulus did not include a sender field.\n")
	}
	if ev.MsgType == shared.MsgTypeDM {
		Stimulus.IsDM = true
	}
	if Stimulus.IsDM {
		Stimulus.Target = cth.ChatTargetChannel(ev.ChannelId)
	} else {
		Stimulus.Target = cth.ChatTargetUser(ev.SenderUserId)
	}

	if len(Stimulus.LcArguments) == 0 {
		log.Debugf("No words were sent in message. Nothing we can do with that.\n")
		return nil
	}
	return &Stimulus
}

func BindToHandle(cth *shared.ChatHandle, cfg *shared.Configuration, Section string) error {
	log.Printf("Performing bind for native slack functions on '%s'.\n", cth.Identifier())
	cth.ChatType = shared.ChatTypeSlackDirect
	var TokenKey string
	var found bool
	if found, _ = cfg.ListedKeysPresent(Section + ".token"); found {
		TokenKey = Section + ".token"
	} else {
		found, value := cfg.GetString(Section + ".tokenkey")
		if found {
			TokenKey = value
		} else {
			TokenKey = "tokens." + Section
		}
	}
	Token := cfg.GetStringOrDie(TokenKey,
		"No slack token provided at config section '%s'", Section)
	//log.Printf("Token: %s\n", Token)
	var sClient *slack.Client
	if log.MinLevel == slogger.DEBUG {
		log.Printf("Debugging mode.\n")
		sClient = slack.New(Token,
			slack.OptionAPIURL("https://api.slack.com/api/"),
			slack.OptionDebug(true),
			slack.OptionLog(reallog.New(os.Stdout, "slack", reallog.LstdFlags|reallog.Lshortfile)))
	} else {
		log.Printf("Not debugging mode.\n")
		sClient = slack.New(Token,
			slack.OptionAPIURL("https://api.slack.com/api/"),
			slack.OptionDebug(false),
			slack.OptionLog(reallog.New(os.Stdout, "slack", reallog.LstdFlags|reallog.Lshortfile)))
	}
	log.Printf("Established direct API connection to %s\n", cth.Identifier())

	dClient := shared.DirectClient{
		ChatType:      shared.ChatTypeSlackDirect,
		Owner:         "",
		UserIndex:     shared.UserIndexType{},
		ChannelLookup: make(map[string]*shared.ChatTarget),
		NativeClient:  nil,
	}
	cth.DirectClient = &dClient
	dClient.UserIndex.ById = make(map[string]*shared.UserInfo)
	dClient.NativeClient = *sClient
	//rtm := sClient.NewRTM()
	//go rtm.ManageConnection()
	//time.Sleep(5*time.Minute)

	cth.NativeClient = &SlackClient{
		Client:       sClient,
		hasconnected: true,
		ChannelIndex: struct {
			ChannelsById   map[string]*slack.Channel
			ChannelsByName map[string]*slack.Channel
		}{
			ChannelsById:   make(map[string]*slack.Channel),
			ChannelsByName: make(map[string]*slack.Channel),
		},
		UserIndex: struct {
			BySlackid map[string]*UserDataStore
			UsersById map[string]*UserDataStore
		}{BySlackid: make(map[string]*UserDataStore),
			UsersById: make(map[string]*UserDataStore)},
	}
	cth.FpSendMessage = fpSendMessage
	cth.FpSendImageTarget = fpSendImageTarget
	cth.FpChatTargetChannel = fpChatTargetChannel
	cth.FpSendSimple = fpSendSimple
	cth.FpNewListener = fpNewListener
	cth.FpUserById = fpUserById
	return nil
}

func MsgOptionText(Text string, Escape bool) MsgOption {
	var Bob MsgOption
	Bob.MsgOption = slack.MsgOptionText(Text, Escape)
	return Bob
}

func MsgOptionAsUser(b bool) MsgOption {
	var Bob MsgOption
	Bob.MsgOption = slack.MsgOptionAsUser(b)
	return Bob
}

func gethandle(dapi *shared.ChatHandle) *SlackClient {
	return dapi.NativeClient.(*SlackClient)
}

func (api *SlackClient) postMessage(channelID string, options ...MsgOption) (string, string, error) {
	var PassOptions []slack.MsgOption
	for _, v := range options {
		PassOptions = append(PassOptions, v.MsgOption)
	}
	respChannel, respTimestamp, err := api.PostMessage(channelID, PassOptions...)
	return respChannel, respTimestamp, err
}

func fpChatTargetChannel(cth *shared.ChatHandle, channelid string) *shared.ChatTarget {
	if channelid == "" {
		log.Errorf("ChatTargetChannel() was passed a blank channelid!\n")
		return nil
	}
	//api := cth.NativeClient.(*SlackClient)
	if cth.ChannelLookup[channelid] != nil {
		return cth.ChannelLookup[channelid]
	}
	Ret := &shared.ChatTarget{Handle: cth}
	cth.ChannelLookup[channelid] = Ret
	Ret.Name = channelid
	Ret.Id = channelid
	//Ret.SetError(fmt.Errorf("no match for conversation"))
	return Ret
}

func fpSendSimple(cth *shared.ChatHandle, tgt *shared.ChatTarget, text string) (*shared.ChatUpdateHandle, error) {
	if tgt == nil {
		return nil, fmt.Errorf("cannot SendSimple with a nil ChatTarget")
	}
	msg := &shared.ChatMessage{
		MsgType: shared.MsgPostOrReply,
		Target:  tgt,
		Message: text,
	}
	return fpSendMessage(cth, msg)
}

func fpSendMessage(cth *shared.ChatHandle, message *shared.ChatMessage) (*shared.ChatUpdateHandle, error) {
	api := cth.NativeClient.(*SlackClient)
	if api == nil {
		return nil, errors.New("SendToSlack() called with null Client handle")
	}
	nativeSendOptions, SendOptions := compileMsgOptions(message.Options, message.Message)
	var retchannelid, timestamp string
	var err error
	switch message.MsgType {
	case shared.MsgNewPost, shared.MsgPostOrReply:
		if message.Target == nil {
			return nil, fmt.Errorf("failed to send on %s; message.Target was nil", cth.Identifier())
		}
		retchannelid, timestamp, err = api.PostMessage(message.Target.Id, nativeSendOptions...)
		log.Debugf("Slack returned channelid '%s'\n", retchannelid)
	case shared.MsgUpdate:
		if message.UpdateHandle.ChannelId == "" {
			log.Warnf("Channelid fed to fpSendMessage() from '%s' is blank on update.\n", cth.Identifier())
		}
		retchannelid, timestamp, _, err = api.UpdateMessage(message.UpdateHandle.ChannelId, message.UpdateHandle.Timestamp, nativeSendOptions...)
		log.Debugf("Slack returned channelid '%s'\n", retchannelid)
	}
	if SendOptions.PinPost || SendOptions.UnpinPost {
		Reference := slack.NewRefToMessage(retchannelid, timestamp)
		log.Printf("Reference: %+v\n", Reference)
		if SendOptions.PinPost {
			err = api.AddPin(retchannelid, Reference)
		} else {
			err = api.RemovePin(retchannelid, Reference)
		}
		if err != nil {
			log.Printf("Error on pin/unpin operation: '%s'!\n", err)
		}
	}
	Update := shared.ChatUpdateHandle{
		ChannelId: retchannelid,
		Timestamp: timestamp,
	}
	return &Update, err
}

func fpSendImageTarget(ct *shared.ChatTarget, Image *shared.Image) error {
	api := ct.Handle.NativeClient.(*SlackClient)
	log.Printf("Uploading image '%s' to '%s'\n", Image.Identifier(), ct.Identifier())
	var Upload slack.FileUploadParameters
	Content, err := Image.Content()
	if err != nil {
		return fmt.Errorf("couldn't get image content: %s", err)
	}
	Upload.Channels = []string{ct.Id}
	Upload.Content = string(Content)
	Upload.Title = "Image"
	bob, err := api.UploadFile(Upload)
	log.Printf("Image upload result: %v\nError: %s\n", bob, err)
	return err
}

func fpNewListener(cth *shared.ChatHandle) (*shared.ListenHandle, error) {
	log.Printf("Starting listener for %s\n", cth.Identifier())
	Handle := gethandle(cth)
	if !Handle.hasconnected {
		log.Fatalf("Handle hadn't already connected.\n")
	}
	rtm := Handle.NewRTM()
	if rtm == nil {
		log.Fatalf("Failed to get new RTM")
	}
	info, url, err := Handle.Client.ConnectRTM()
	log.Printf("Started RTM at '%s'; info is '%+v'\n", url, info)
	log.ErrorIff(err, "rtm error")
	Caw := shared.ListenHandle{
		Dapi:     cth,
		Native:   rtm,
		Incoming: make(chan interface{}),
	}
	if Caw.Dapi == nil {
		log.Fatalf("Nil Dapi")
	}
	if Caw.Dapi.NativeClient == nil {
		log.Fatalf("Nil Dapi.NativeClient")
	}
	sc := cth.NativeClient.(*SlackClient)
	go func(lh *shared.ListenHandle) {
		go rtm.ManageConnection()
		for true {
			log.Printf("Listening thread started on %+v\n", lh.Dapi.Identifier())
			for msg := range rtm.IncomingEvents {
				Buffer, _ := json.Marshal(msg)
				log.Printf("Message Received: %s\n", Buffer)
				var Msg *shared.MessagerEvent
				switch ev := msg.Data.(type) {
				case *slack.MessageEvent:
					var MsgType shared.MsgType
					//fmt.Printf("Event subtype is %s; data: %+v\n", ev.SubType, ev)
					Sender := sc.getUserCache(ev.User)
					if Sender == nil && ev.SubMessage != nil && ev.SubMessage.User != "" {
						Sender = sc.getUserCache(ev.SubMessage.User)
					}
					Conversation := sc.getConversationCache(ev.Channel)
					Label := Conversation.GroupConversation.Name
					if Sender == nil {
						if ev.Msg.Edited != nil && ev.SubMessage != nil && ev.SubMessage.Edited != nil {
							log.Printf("This event is an edit. Don't care.\n")
							continue
						}
						if ev.Msg.SubType == "message_changed" {
							log.Printf("Message_changed event in '%s'. Don't care.\n", Label)
							continue
						}
						log.Printf("Sender is nil.\n")
						continue
					}
					if Sender.IsBot {
						//log.Printf("Bot '%s' said something in '%s'; ignoring..\n", Sender.Name, Label)
						continue
					}
					if ev.Msg.SubType == "group_topic" {
						log.Printf("Topic for channel '%s' changed by '%s': '%s'\n", Label, Sender.Name, ev.Text)
						continue
					}
					if ev == nil {
						log.Printf("ev is nil.\n")
						continue
					}
					if Conversation.IsIM {
						MsgType = shared.MsgTypeDM
						log.Printf("In DM, '%s' said '%s'\n", Sender.Name, ev.Text)
					} else if Conversation.IsChannel {
						MsgType = shared.MsgTypeChannel
						log.Printf("In channel '%s', '%s' said '%s'\n", Label, Sender.Name, ev.Text)
					} else if Conversation.IsMpIM { // got private channel AND the multi-dm
						MsgType = shared.MsgTypeChannel
						log.Printf("In multi-DM '%s', '%s' said '%s'\n", Label, Sender.Name, ev.Text)
					} else if Conversation.IsPrivate {
						MsgType = shared.MsgTypeChannel
						log.Printf("In private channel '%s', '%s' said '%s'\n", Label, Sender.Name, ev.Text)
					}
					Msg = &shared.MessagerEvent{
						MsgId:        ev.ClientMsgID,
						ParentMsgId:  "",
						ChannelId:    ev.Channel,
						SenderUserId: Sender.ID,
						Text:         ev.Text,
						MsgType:      MsgType,
						Connection:   cth,
						NativeMsg:    ev,
						Timestamp:    nil,
						FpToStimulus: evFormulateStimulus,
					}
				}
				if Msg != nil {
					Caw.Incoming <- Msg
					Msg = nil
				}
			}
		}
	}(&Caw)
	return &Caw, nil
}

func sendMessage(dapi *shared.ChatHandle, channel string, message string) (channelID string, timestamp string, err error) {
	if dapi == nil {
		return "", "", errors.New("SendToSlack() called with null Client handle")
	}
	api := gethandle(dapi)
	var Options []slack.MsgOption
	Options = append(Options, slack.MsgOptionText(message, false))
	channelID, timestamp, err = api.PostMessage(channel, Options...)
	return channelID, timestamp, err
}

func fpUserById(cth *shared.ChatHandle, ChatId string) *shared.UserInfo {
	if ChatId == "" {
		return nil
	}
	if val, ok := cth.UserIndex.ById[ChatId]; ok {
		return val
	}
	api := gethandle(cth)
	log.Debugf("Looking up user by slackid '%s'\n", ChatId)
	sUser, err := api.GetUserInfo(ChatId)
	if err != nil {
		log.Errorf("Error on GetUserInfo for '%s': %s\n", err)
		return nil
	}
	UDS := shared.UserInfo{
		Ignore:   false,
		IsAdmin:  sUser.IsAdmin,
		Email:    sUser.Profile.Email,
		Name:     sUser.Profile.RealNameNormalized,
		ChatId:   sUser.Profile.DisplayNameNormalized,
		CorpId:   sUser.TeamID,
		Native:   sUser,
		NativeId: sUser.ID,
		PermVal:  make(sjson.JSON),
		TempVal:  make(sjson.JSON),
	}
	UDS.Native = sUser
	// TODO: Set up an actual admin table.
	if UDS.Name == cth.Owner {
		UDS.IsAdmin = true
		log.Debugf("User '%s' is admin for this instance.\n", UDS.Name)
	} else {
		log.Debugf("User '%s' is not admin for this instance.\n", UDS.Name)
	}
	cth.UserIndex.ById[ChatId] = &UDS
	return &UDS
}

func compileMsgOptions(options *sjson.JSON, message string) ([]slack.MsgOption, shared.ChatOptions) {
	var (
		OptionBlock shared.ChatOptions
		Options     []slack.MsgOption
	)
	Options = append(Options, slack.MsgOptionAsUser(true))
	Options = append(Options, slack.MsgOptionText(message, false))
	if options == nil {
		log.Debugf("Options is blank; default options only.\n")
		return Options, OptionBlock
	}
	err := json.Unmarshal(options.Bytes(), &OptionBlock)
	log.ErrorIff(err, "error unmarshaling options into an object!\n")

	if OptionBlock.Mediaunfurl == false {
		// This causes video/image links to unfurl; e.g. https://www.youtube.com/watch?v=-LXbKHo7Wd4
		Options = append(Options, slack.MsgOptionDisableMediaUnfurl())
	}
	if OptionBlock.Linkunfurl == true { // Defaults is to not unfurl URLs already, so we grant unfurls if the user asks for it.
		log.Debugf("Explicitly enabled linkunfurl, so setting it.\n")
		Options = append(Options, slack.MsgOptionEnableLinkUnfurl())
	} else {
		log.Debugf("Disabling Link Unfurl, as it was not requested specifically.\n")
		Options = append(Options, slack.MsgOptionDisableLinkUnfurl())
	}
	if OptionBlock.Parse == true {
		// This seems to actually DISABLE parsing!
		Options = append(Options, slack.MsgOptionParse(true))
	}
	if OptionBlock.IconUrl != "" {
		Options = append(Options, slack.MsgOptionIconURL(OptionBlock.IconUrl))
	}
	if OptionBlock.IconEmoji != "" {
		Options = append(Options, slack.MsgOptionIconEmoji(OptionBlock.IconEmoji))
	}
	if OptionBlock.Attachtext != "" {
		attachment := slack.Attachment{
			//Pretext: "Pretext", // This seems indistinguishable from the regular message parameter
			//Text:    "Attachment text", // This gets rendered as indented, quoted text under main message
			// Uncomment the following part to send a field too
			//	Fields: []slack.AttachmentField{
			//		slack.AttachmentField{
			//			Title: "a",  // This renders as bold under the attachment, also indented and quoted
			//			Value: "no", // This renders as plain under the attachment, but indented and quoted
			//		},
			//	},
		}
		attachment.Text = OptionBlock.Attachtext
		Options = append(Options, slack.MsgOptionAttachments(attachment))
	}
	if len(OptionBlock.Columns) != 0 {
		log.Printf("Table is detected.\n")
		Fields := make([]*slack.TextBlockObject, 0)
		for _, Rows := range OptionBlock.Columns {
			for _, field := range Rows {
				//log.Printf("Doing a field: %s\n", field)
				Fields = append(Fields, slack.NewTextBlockObject(
					"mrkdwn",
					field,
					false,
					false))
			}
		}
		var Sections []slack.Block
		var MsgObj *slack.TextBlockObject
		if message != "" {
			MsgObj = slack.NewTextBlockObject("mrkdwn", message, false, false)
		}
		Sections = append(Sections, slack.NewSectionBlock(MsgObj, Fields, nil))
		Options = append(Options, slack.MsgOptionBlocks(Sections...))
	}

	//	if OptionBlock.Blocks != "" {
	//		Options=append(Options, slack.Blocks(OptionBlock.Blocks))
	//	}
	if OptionBlock.Attachtext != "" {
		attachment := slack.Attachment{
			//Pretext: "Pretext", // This seems indistinguishable from the regular message parameter
			//Text:    "Attachment text", // This gets rendered as indented, quoted text under main message
			// Uncomment the following part to send a field too
			//	Fields: []slack.AttachmentField{
			//		slack.AttachmentField{
			//			Title: "a",  // This renders as bold under the attachment, also indented and quoted
			//			Value: "no", // This renders as plain under the attachment, but indented and quoted
			//		},
			//	},
		}
		attachment.Text = OptionBlock.Attachtext
		Options = append(Options, slack.MsgOptionAttachments(attachment))
	}

	log.Debugf("Compiling message options: string '%s'\nBecame '+%v'\n", *options, OptionBlock)
	return Options, OptionBlock
}

func (sc *SlackClient) getUserCache(user string) *slack.User {
	Val := sc.userBySlackId(user)
	if Val == nil {
		return nil
	}
	return Val.SlackUser
}

func (sc *SlackClient) userBySlackId(Slackid string) *UserDataStore {
	if Slackid == "" {
		return nil
	}
	if val, ok := sc.UserIndex.BySlackid[Slackid]; ok {
		return val
	}
	log.Debugf("Looking up user by slackid '%s'\n", Slackid)
	UserObj, err := sc.GetUserInfo(Slackid)
	if err != nil {
		log.Errorf("Error on GetUserInfo for '%s': %s\n", err)
		return nil
	}
	UDS := UserDataStore{}
	UDS.TempVal = make(sjson.JSON)
	UDS.PermVal = make(sjson.JSON)
	UDS.SlackUser = UserObj
	sc.UserIndex.BySlackid[Slackid] = &UDS
	return &UDS
}

func (sc *SlackClient) getConversationCache(channel string) *slack.Channel {
	if sc.ChannelIndex.ChannelsById[channel] != nil {
		return sc.ChannelIndex.ChannelsById[channel]
	}
	if sc.ChannelIndex.ChannelsByName[channel] != nil {
		return sc.ChannelIndex.ChannelsByName[channel]
	}
	log.Debugf("Conversation lookup: searching for '%s'\n", channel)
	Conversation, _ := sc.GetConversationInfo(channel, true)
	if Conversation != nil {
		sc.ChannelIndex.ChannelsById[channel] = Conversation
		sc.ChannelIndex.ChannelsByName[Conversation.Name] = Conversation
		return Conversation
	}
	log.Printf("Total failure to resolve '%s'\n", channel)
	//fmt.Printf("Bob: %v\n", Bob)
	return nil
}
