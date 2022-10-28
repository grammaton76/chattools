package directchat

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/grammaton76/g76golib/shared"
	"github.com/grammaton76/g76golib/sjson"
	"github.com/grammaton76/g76golib/slogger"
	"github.com/nlopes/slack"
	"regexp"
	"strings"
)

func manageConnection(dc *shared.DirectClient) {
	log.Fatalf("Define ManageConnection()")
}

func init() {
	rx.StripLink = regexp.MustCompile(`(?ms)<.*\|(.*?)>`)
}

type MsgOption struct {
	slack.MsgOption
}

type slackClient struct {
	*slack.Client
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

func formulateStimulus(dapi *shared.DirectClient, ev *shared.MessagerEvent) *shared.ResponseTo {
	var Stimulus shared.ResponseTo = shared.ResponseTo{
		MsgId:    ev.MsgId,
		InThread: ev.ParentMsgId,
	}
	Stimulus.Target = dapi.ChatTargetChannel(ev.ChannelId)
	if Stimulus.Target.IsDM {
		Stimulus.IsDM = true
	}
	Sender := dapi.UserById(ev.SenderUserId)
	if Sender != nil {
		Stimulus.Sender = Sender
		log.Debugf("Stimulus formulated: user '%s' was sender, admin flag %t.\n", Sender.Name, Stimulus.FromAdmin)
	} else {
		log.Debugf("Stimulus did not include a sender field.\n")
	}
	Stimulus.OrigMessage = ev.Text
	Stimulus.Message = ev.Text
	Stimulus.Message = rx.StripLink.ReplaceAllString(Stimulus.Message, "$1")
	Stimulus.Arguments = strings.Fields(Stimulus.Message)
	Stimulus.LcArguments = strings.Fields(strings.ToLower(Stimulus.Message))
	if len(Stimulus.LcArguments) == 0 {
		log.Debugf("No words were sent in message. Nothing we can do with that.\n")
		return nil
	}
	return &Stimulus
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

func gethandle(dapi *shared.DirectClient) *slackClient {
	return dapi.NativeClient.(*slackClient)
}

func (api *slackClient) postMessage(channelID string, options ...MsgOption) (string, string, error) {
	var PassOptions []slack.MsgOption
	for _, v := range options {
		PassOptions = append(PassOptions, v.MsgOption)
	}
	respChannel, respTimestamp, err := api.PostMessage(channelID, PassOptions...)
	return respChannel, respTimestamp, err
}

func getChannel(dapi *shared.DirectClient, channelid string) *shared.ChatTarget {
	if dapi.ChannelLookup[channelid] != nil {
		return dapi.ChannelLookup[channelid]
	}
	api := gethandle(dapi)
	sConversation, _ := api.GetConversationInfo(channelid, true)
	if sConversation == nil {
		return nil
	}
	var Conversation shared.ChatTarget
	Conversation.Name = sConversation.Name
	dapi.ChannelLookup[channelid] = &Conversation
	return &Conversation
}

func (api *slackClient) SendMessage(message *shared.ChatMessage) (*shared.ChatUpdateHandle, error) {
	if api == nil {
		return nil, errors.New("SendToSlack() called with null Client handle")
	}
	SendOptions, _ := compileMsgOptions(message.Options, message.Message)
	channelid, timestamp, err := api.PostMessage(message.Target.Name, SendOptions...)
	Update := shared.ChatUpdateHandle{
		ChannelId: channelid,
		Timestamp: timestamp,
		RowId:     0,
	}
	return &Update, err
}

func newListener(dapi *shared.DirectClient) *shared.ListenHandle {
	rtm := gethandle(dapi).NewRTM()
	Caw := shared.ListenHandle{
		Native:   &rtm,
		Incoming: nil,
	}
	return &Caw
}

func sendMessage(dapi *shared.DirectClient, channel string, message string) (channelID string, timestamp string, err error) {
	if dapi == nil {
		return "", "", errors.New("SendToSlack() called with null Client handle")
	}
	api := gethandle(dapi)
	var Options []slack.MsgOption
	Options = append(Options, slack.MsgOptionText(message, false))
	channelID, timestamp, err = api.PostMessage(channel, Options...)
	return channelID, timestamp, err
}

func userById(dapi *shared.DirectClient, ChatId string) *shared.UserInfo {
	if ChatId == "" {
		return nil
	}
	if val, ok := dapi.UserIndex.ById[ChatId]; ok {
		return val
	}
	api := gethandle(dapi)
	log.Debugf("Looking up user by slackid '%s'\n", ChatId)
	sUser, err := api.GetUserInfo(ChatId)
	if err != nil {
		log.Errorf("Error on GetUserInfo for '%s': %s\n", err)
		return nil
	}
	UDS := shared.UserInfo{
		Ignore:  false,
		IsAdmin: sUser.IsAdmin,
		Email:   sUser.Profile.Email,
		Name:    sUser.Profile.RealNameNormalized,
		ChatId:  sUser.Profile.DisplayNameNormalized,
		PermVal: make(sjson.JSON),
		TempVal: make(sjson.JSON),
	}
	UDS.Native = sUser
	// TODO: Set up an actual admin table.
	if UDS.Name == dapi.Owner {
		UDS.IsAdmin = true
		log.Debugf("User '%s' is admin for this instance.\n", UDS.Name)
	} else {
		log.Debugf("User '%s' is not admin for this instance.\n", UDS.Name)
	}
	dapi.UserIndex.ById[ChatId] = &UDS
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
		fmt.Printf("Options is blank; default options only.\n")
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
