package directchat

import (
	"context"
	"encoding/xml"
	"fmt"
	"github.com/grammaton76/g76golib/shared"
	"github.com/grammaton76/g76golib/sjson"
	"github.com/kballard/go-shellquote"
	"gosrc.io/xmpp"
	"gosrc.io/xmpp/stanza"
	"os"
	"reflect"
	"strings"
	"time"
)

var xmppClients map[string]*shared.DirectClient

type xmppClient struct {
	*xmpp.Client
	listener          *shared.ListenHandle
	router            *xmpp.Router
	Username          string
	BaseUserDomain    string
	BaseChannelDomain string
	HandleXmppId      string
	streamId          string
	jId               string
	reqprefix         string
	reqcounter        int
	roomnickcache     map[string]string
}

type XMsgSubject struct {
	stanza.MsgExtension
	XMLName xml.Name `xml:"subject,omitempty"`
}

type XMsgDelay struct {
	stanza.MsgExtension
	XMLName xml.Name `xml:"delay,omitempty"`
	Stamp   string   `xml:"stamp,attr"`
	From    string   `xml:"from,attr"`
}

type XMsgAddress struct {
	stanza.MsgExtension
	XMLName xml.Name `xml:"addresses,omitempty"`
	Address struct {
		Jid  string `xml:"jid,attr"`
		Type string `xml:"type,attr"`
	} `xml:"address"`
}

type XPresenceVcard struct {
	stanza.PresExtension
	XMLName xml.Name `xml:"x"`
	Item    struct {
		Text        string `xml:",chardata"`
		Jid         string `xml:"jid,attr"`
		Role        string `xml:"role,attr"`
		Affiliation string `xml:"affiliation,attr"`
	} `xml:"item"`
	Status []struct {
		Text string `xml:",chardata"`
		Code string `xml:"code,attr"`
	} `xml:"status"`
}

func init() {
	xmppClients = make(map[string]*shared.DirectClient)

	xAddress := xml.Name{Space: "http://jabber.org/protocol/address", Local: "*"}
	xDelay := xml.Name{Space: "urn:xmpp:delay", Local: "*"}
	xMucUser := xml.Name{
		Space: "http://jabber.org/protocol/muc#user",
		Local: "*",
	}
	xSubject := xml.Name{
		Space: "subject",
		Local: "*",
	}
	stanza.TypeRegistry.MapExtension(stanza.PKTMessage, xDelay, XMsgDelay{})
	stanza.TypeRegistry.MapExtension(stanza.PKTMessage, xAddress, XMsgAddress{})
	stanza.TypeRegistry.MapExtension(stanza.PKTMessage, xSubject, XMsgSubject{})
	stanza.TypeRegistry.MapExtension(stanza.PKTPresence, xDelay, XMsgDelay{})
	stanza.TypeRegistry.MapExtension(stanza.PKTPresence, xMucUser, XPresenceVcard{})
}

func getChatTargetUser(dc *shared.DirectClient, User string) *shared.ChatTarget {
	Xmpp := dc.NativeClient.(*xmppClient)
	Target := shared.ChatTarget{
		Id:        Xmpp.QualifyUser(User),
		Name:      User,
		IsDM:      true,
		IsMpDm:    false,
		IsAdmin:   false,
		IsPrivate: false,
		IsPublic:  false,
	}
	log.Printf("Qualified user '%s' as '%s'\n", User, Target.Id)
	return &Target
}

func getChatTargetChannel(dc *shared.DirectClient, Channel string) *shared.ChatTarget {
	Xmpp := dc.NativeClient.(*xmppClient)
	iSlash := strings.Index(Channel, "/")
	if iSlash != -1 {
		Channel = Channel[:iSlash]
	}
	Target := shared.ChatTarget{
		Id:        Xmpp.QualifyChannel(Channel),
		Name:      Channel,
		IsDM:      false,
		IsMpDm:    false,
		IsAdmin:   false,
		IsPrivate: false,
		IsPublic:  true,
	}
	return &Target
}

func identifyXmppClient(dc *shared.DirectClient) string {
	Xmpp := dc.NativeClient.(*xmppClient)
	return fmt.Sprintf("xmpp '%s' jid %s", Xmpp.HandleXmppId, Xmpp.jId)
}

func newXmppListener(dc *shared.DirectClient) *shared.ListenHandle {
	Xmpp := dc.NativeClient.(*xmppClient)
	return Xmpp.listener
}

func (Xmpp *xmppClient) QualifyChannel(User string) string {
	iAt := strings.Index(User, "@")
	log.Printf("Received '%s' into QualifyChannel; locators %d.\n", User, iAt)
	if iAt != -1 {
		return User
	}
	if iAt == -1 {
		return fmt.Sprintf("%s@%s", User, Xmpp.BaseChannelDomain)
	}
	return fmt.Sprintf("%s", User)
}

func (Xmpp *xmppClient) QualifyUser(User string) string {
	iAt := strings.Index(User, "@")
	log.Printf("Received '%s' into QualifyUser; locators %d.\n", User, iAt)
	if iAt != -1 {
		return User
	}
	if iAt == -1 {
		return fmt.Sprintf("%s@%s", User, Xmpp.BaseUserDomain)
	}
	return fmt.Sprintf("%s", User)
}

func openXmppComms(dc *shared.DirectClient, Target *shared.ChatTarget) error {
	var err error
	Xmpp := dc.NativeClient.(*xmppClient)
	XmId := Target.Id
	if !Target.IsDM {
		XmId += "/" + Xmpp.Username
	}
	log.Printf("Now opening comms to to '%s'\n", XmId)
	err = Xmpp.SendRaw(fmt.Sprintf(`<presence to='%s'/>`, XmId))
	log.PrintIff(err, "Error on join: '%s'\n", err)
	return err
}

func NewXmpp(Meta *sjson.JSON) *shared.DirectClient {
	Bob := shared.DirectClient{
		ChatType: shared.ChatTypeXmppDirect,
		Owner:    "",
		UserIndex: shared.UserIndexType{
			RoomUserId: make(map[string]*shared.UserInfo),
			ById:       make(map[string]*shared.UserInfo),
		},
		ChannelLookup:       nil,
		NativeClient:        nil,
		RefuseMessagesAfter: time.Second * time.Duration(5),
		FpSendSimple:        sendXmppSimpleMessage,
		//FpNewListener:       newXmppListener,
		FpChatTargetUser:    getChatTargetUser,
		FpChatTargetChannel: getChatTargetChannel,
		FpIdentifier:        identifyXmppClient,
		FpUserById:          getXmppUserById,
		FpJoinChannel:       openXmppComms,
	}
	//log.Secretf("We were passed the following json: %s\n", Meta.String())
	Address := Meta.KeyString("address")
	Domain := Meta.KeyString("domain")
	User := Meta.KeyString("user")
	Password := Meta.KeyString("password")
	if Address == "" {
		log.Fatalf("Cannot establish an XMPP direct chat with a blank 'address' field in its chat handle.\n")
	}
	if Domain == "" {
		log.Fatalf("Cannot establish an XMPP direct chat with a blank 'domain' field in its chat handle.\n")
	}
	if User == "" {
		log.Fatalf("Cannot establish an XMPP direct chat with a blank 'user' field in its chat handle.\n")
	}
	if Password == "" {
		log.Fatalf("Cannot establish an XMPP direct chat with a blank 'password' field in its chat handle.\n")
	}
	log.Printf("Here goes nothing: %s\n", Meta.String())
	config := xmpp.Config{
		TransportConfiguration: xmpp.TransportConfiguration{
			Address: Address,
			Domain:  Domain,
		},
		Jid:          User,
		Credential:   xmpp.Password(Password),
		StreamLogger: os.Stdout,
		Insecure:     true,
		// TLSConfig: tls.Config{InsecureSkipVerify: true},
	}
	router := xmpp.NewRouter()
	router.HandleFunc("message", handleMessage)
	router.HandleFunc("presence", handlePresence)
	router.HandleFunc("", handleAnything)
	Xmpp := xmppClient{
		Client: nil,
		listener: &shared.ListenHandle{
			Dapi:               nil,
			Native:             nil,
			Incoming:           make(chan interface{}),
			FpManageConnection: nil,
		},
		Username:          "",
		BaseUserDomain:    Meta.KeyString("baseuserdomain"),
		BaseChannelDomain: Meta.KeyString("basechanneldomain"),
		router:            router,
		HandleXmppId:      "",
		streamId:          "",
	}
	client, err := xmpp.NewClient(&config, router, errorHandler)
	if err != nil {
		log.Fatalf("%+v", err)
	}
	log.FatalIff(client.Connect(), "failed to connect to xmpp\n")
	{
		DisplayUserPos := strings.Index(User, "@")
		if DisplayUserPos != -1 {
			Xmpp.Username = User[:DisplayUserPos]
		} else {
			Xmpp.Username = User
		}
	}
	MyJid := client.Session.BindJid
	xmppClients[MyJid] = &Bob
	Xmpp.Client = client
	Xmpp.HandleXmppId = User
	Xmpp.streamId = client.Session.StreamId
	Xmpp.jId = MyJid
	Bob.NativeClient = &Xmpp
	///////// EXPERIMENTAL STUFF BELOW
	//UserData := getXmppUserById(&Bob, "user@xmpp.domain")
	//log.Printf("Returned user data: %s\n", UserData)
	return &Bob
}

type Delay struct {
	XMLName xml.Name      `xml:"http://jabber.org/protocol/muc#admin query"`
	Items   []stanza.Item `xml:"item"`
	Stamp   string        `xml:"stamp"`
}

func (q Delay) Namespace() string {
	return "http://jabber.org/protocol/muc#admin"
}

func (Xmpp *xmppClient) newXmppRouteId() string {
	Ret := fmt.Sprintf("%s:%05d", Xmpp.reqprefix, Xmpp.reqcounter)
	Xmpp.reqcounter++
	return Ret
}

func getXmppUserByRoomNick(dc *shared.DirectClient, UserId string) *shared.UserInfo {
	if dc.UserIndex.RoomUserId[UserId] != nil {
		return dc.UserIndex.RoomUserId[UserId]
	}
	if UserId == "" {
		return nil
	}
	Xmpp := dc.NativeClient.(*xmppClient)
	RouteId := Xmpp.newXmppRouteId()
	xQuery := fmt.Sprintf(`<iq type='get' id='%s' to='%s'><vCard xmlns='vcard-temp'/></iq>`, RouteId, UserId)
	log.Printf("Sending XMPP IQ query '%s' in getXmppUserByRoomNick\n", xQuery)
	err := Xmpp.SendRaw(xQuery)
	log.FatalIff(err, "Failed to send upstream in getXmppUserById")
	Ctx, _ := context.WithTimeout(context.Background(), time.Minute)
	RouteResult := <-Xmpp.router.NewIQResultRoute(Ctx, RouteId)
	log.Printf("RouteResult: %s\n", RouteResult)
	User := shared.UserInfo{}
	User.Native = RouteResult
	for _, v := range RouteResult.Any.Nodes {
		Name := v.XMLName.Local
		Val := v.Content
		switch Name {
		case "EMAIL":
			for _, w := range v.Nodes {
				if w.XMLName.Local == "USERID" {
					User.Email = w.Content
				}
			}
		case "FN":
			if User.Name == "" {
				User.Name = Val
			}
		case "N":
			User.Name = xmppAssembleRealNameFromName(v)
		case "USERID":
			User.Email = v.Content
		case "NICKNAME":
			User.CorpId = Val
		case "BDAY":
		default:
			log.Printf("Unmatched variable named '%s'; value '%s'\n", Name, Val)
		}
	}
	//log.Printf("Extracted data: %+v\n", User)
	if User.CorpId == "" {
		User.CorpId = User.Email
	}
	dc.UserIndex.RoomUserId[UserId] = &User
	return &User
}

func getXmppUserById(dc *shared.DirectClient, UserId string) *shared.UserInfo {
	if dc.UserIndex.ById[UserId] != nil {
		return dc.UserIndex.ById[UserId]
	}
	if UserId == "" {
		return nil
	}
	Xmpp := dc.NativeClient.(*xmppClient)
	RouteId := Xmpp.newXmppRouteId()
	UserId = strings.Split(UserId, "/")[0]
	xQuery := fmt.Sprintf(`<iq type='get' id='%s' to='%s'><vCard xmlns='vcard-temp'/></iq>`, RouteId, UserId)
	log.Printf("Sending XMPP IQ query '%s'\n", xQuery)
	err := Xmpp.SendRaw(xQuery)
	log.FatalIff(err, "Failed to send upstream in getXmppUserById")
	Ctx, _ := context.WithTimeout(context.Background(), time.Minute)
	RouteResult := <-Xmpp.router.NewIQResultRoute(Ctx, RouteId)
	log.Printf("RouteResult: %s\n", RouteResult)
	User := shared.UserInfo{}
	User.Native = RouteResult
	for _, v := range RouteResult.Any.Nodes {
		Name := v.XMLName.Local
		Val := v.Content
		switch Name {
		case "EMAIL":
			for _, w := range v.Nodes {
				if w.XMLName.Local == "USERID" {
					User.Email = w.Content
				}
			}
		case "FN":
			if User.Name == "" {
				User.Name = Val
			}
		case "N":
			User.Name = xmppAssembleRealNameFromName(v)
		case "USERID":
			User.Email = v.Content
		default:
			log.Printf("Unmatched variable named '%s'; value '%s'\n", Name, Val)
		}
	}
	log.Printf("Extracted data: %+v\n", User)
	if User.CorpId == "" {
		User.CorpId = User.Email
	}
	dc.UserIndex.ById[UserId] = &User
	return &User
}

func xmppAssembleRealNameFromName(v stanza.Node) string {
	var Name struct {
		First string
		Last  string
	}
	for _, v := range v.Nodes {
		xName := v.XMLName.Local
		switch xName {
		case "GIVEN":
			Name.First = v.Content
		case "FAMILY":
			Name.Last = v.Content
		default:
			log.Printf("Found unknown name component '%s'!\n", xName)
		}
	}
	return fmt.Sprintf("%s %s", Name.First, Name.Last)
}

func sendXmppSimpleMessage(dc *shared.DirectClient, Target *shared.ChatTarget, Message string) (string, string, error) {
	xmpp := dc.NativeClient.(*xmppClient)
	var xTo string
	var xChatType stanza.StanzaType
	openXmppComms(dc, Target)
	if Target.IsDM {
		xChatType = "chat"
		xTo = xmpp.QualifyUser(Target.Id)
	} else {
		xChatType = "groupchat"
		xTo = xmpp.QualifyChannel(Target.Id)
		//xTo="channelname@xmpp.server.name"
	}
	log.Printf("Simple Message being sent to: '%s'; type '%s'\n", xTo, xChatType)
	var Msg stanza.Message
	if Target.IsDM {
		Msg = stanza.Message{Attrs: stanza.Attrs{To: xTo, From: xmpp.HandleXmppId, Type: xChatType}, Body: Message}
	} else {
		Msg = stanza.Message{Attrs: stanza.Attrs{To: xTo, Type: xChatType}, Body: Message}
	}
	log.Printf("Sending message: %+v\n", Msg)
	err := xmpp.Send(Msg)
	log.FatalIff(err, "error sending xmpp")
	return "", "", err
}

func xmppToStimulus(ev *shared.MessagerEvent) *shared.ResponseTo {
	dc := ev.Connection.(*shared.DirectClient)
	Response := shared.ResponseTo{
		FromAdmin:   false,
		InThread:    "",
		Arguments:   nil,
		LcArguments: nil,
		OrigMessage: ev.Text,
		Message:     "",
		MsgId:       "",
	}
	SenderRoomId := ev.NativeMsg.(stanza.Message).Attrs.From
	switch ev.MsgType {
	case shared.MsgTypeChannel:
		Response.IsDM = false
		Response.Sender = getXmppUserByRoomNick(dc, SenderRoomId)
		Response.Target = getChatTargetChannel(dc, ev.ChannelId)
	case shared.MsgTypeDM:
		Response.IsDM = true
		Response.Sender = getXmppUserById(dc, SenderRoomId)
		Response.Target = nil
	}
	sArgs, err := shellquote.Split(ev.Text)
	log.FatalIff(err, "Quoting broken on input '%s'!\n", ev.Text)

	Response.Arguments = sArgs
	for _, v := range sArgs {
		Response.LcArguments = append(Response.LcArguments, strings.ToLower(v))
	}
	return &Response
}

func handleAnything(s xmpp.Sender, p stanza.Packet) {
	log.Fatalf("Received an event on handleAnything!\n")
}

func handlePresence(s xmpp.Sender, p stanza.Packet) {
	var Dc *shared.DirectClient
	//Xmpp := Dc.NativeClient.(*xmppClient)
	switch s.(type) {
	case *xmpp.Client:
		//log.Printf("Yes, s is an xmpp.Client object.\n")
		Xmpp := s.(*xmpp.Client)
		MyJid := Xmpp.Session.BindJid
		Dc = xmppClients[MyJid]
	default:
		log.Fatalf("Non-xmpp.Client type '%s' passed to handleMessage(): '%s'\nValues: %+v\n", reflect.TypeOf(s), p)
		return
	}
	msg, ok := p.(stanza.Presence)
	if !ok {
		log.Printf("Ignoring presence packet: %T\n", p)
		return
	}
	if msg.To == msg.From {
		log.Printf("Received my own presence notification... this is not very relevant to me.\n")
		return
	}
	if Dc.UserIndex.ById[msg.To] != nil {
		log.Printf("We have seen '%s' before and already cached it.\n", msg.To)
		return
	}
	if msg.XMLName.Space != "jabber:client" || msg.XMLName.Local != "presence" {
		log.Printf("Message '%+v' has presence message with unexpected XMLname!\n", msg)
		return
	}
	var ActualUser string
	if msg.Attrs.Type == "unavailable" {
		//Xmpp.roomrosters[msg.Room][msg.From]=nil
	}
	if len(msg.Extensions) > 0 {
		for _, xT := range msg.Extensions {
			switch xT.(type) {
			default:
				log.Printf("Unknown presence notification on '%s' type: %s\n", Dc.Identifier(), reflect.TypeOf(msg))
			case *XMsgDelay:
				Rec := xT.(*XMsgDelay)
				ActualUser = Rec.From
			case *XPresenceVcard:
				Rec := xT.(*XPresenceVcard)
				ActualUser = Rec.Item.Jid
				log.Printf("Received a presence: '%s' is present and should be looked up.\n", Rec.Item.Jid)
				//Rec := xT.(*XPresenceVcard)
				//Rec.Item.Jid
			case *stanza.Presence:
				Rec := xT.(stanza.Presence)
				log.Printf("Extension indicates that the user '%s' has an underlying id of '%s'\n", msg.To, Rec.From)
			}
		}
	}
	log.Printf("Presence of '%s' on '%s' type: %s\n", ActualUser, Dc.Identifier(), msg.Type)
	getXmppUserById(Dc, ActualUser)
}

func handleMessage(s xmpp.Sender, p stanza.Packet) {
	var Dc *shared.DirectClient
	//Xmpp := Dc.NativeClient.(*xmppClient)
	switch s.(type) {
	case *xmpp.Client:
		//log.Printf("Yes, s is an xmpp.Client object.\n")
		Xmpp := s.(*xmpp.Client)
		MyJid := Xmpp.Session.BindJid
		Dc = xmppClients[MyJid]
	default:
		log.Fatalf("Non-xmpp.Client type '%s' passed to handleMessage(): '%s'\nValues: %+v\n", reflect.TypeOf(s), p)
		return
	}
	Listener := Dc.NativeClient.(*xmppClient).listener
	msg, ok := p.(stanza.Message)
	if !ok {
		log.Printf("Ignoring packet: %T\n", p)
		return
	}
	//log.Printf("Handler '%s' received message packet\n", Dc.Identifier())
	switch msg.Type {
	case stanza.MessageTypeError:
		log.Fatalf("received: %s\n", msg)
	case stanza.MessageTypeChat:
		if msg.Body == "" {
			return
		}
		ChatTarget := getChatTargetUser(Dc, msg.Attrs.From)
		log.Printf("Got the following user: %s\n", ChatTarget.Name)
		Event := shared.MessagerEvent{
			Connection:   Dc,
			MsgId:        msg.Id,
			ParentMsgId:  "",
			ChannelId:    msg.Attrs.From,
			SenderUserId: msg.Attrs.From,
			Text:         msg.Body,
			NativeMsg:    msg,
			MsgType:      shared.MsgTypeDM,
			FpToStimulus: xmppToStimulus,
		}
		//		SenderChannelId := msg.Attrs.From
		//		SenderUserId :=
		Listener.Incoming <- Event
		log.Printf("Received DM: %s\n", msg)
	case stanza.MessageTypeGroupchat:
		if msg.Body == "" {
			return
		}
		SenderRoomNick := msg.Attrs.From
		SenderInfo := getXmppUserByRoomNick(Dc, SenderRoomNick)
		Event := shared.MessagerEvent{
			Connection:   Dc,
			MsgId:        msg.Attrs.From,
			ParentMsgId:  "",
			ChannelId:    msg.Attrs.From,
			SenderUserId: SenderInfo.CorpId,
			Text:         msg.Body,
			NativeMsg:    msg,
			MsgType:      shared.MsgTypeChannel,
			FpToStimulus: xmppToStimulus,
		}
		for _, xT := range msg.Extensions {
			switch xT.(type) {
			case *stanza.Node:
				Rec := xT.(*stanza.Node)
				if Rec.XMLName.Space == "http://jabber.org/protocol/address" && Rec.XMLName.Local == "addresses" {
					Node := Rec.Nodes[0]
					for _, v := range Node.Attrs {
						switch v.Name.Local {
						case "jid":
							Event.SenderUserId = v.Value
							iSlash := strings.Index(Event.SenderUserId, "/")
							if iSlash != -1 {
								Event.SenderUserId = Event.SenderUserId[:iSlash]
							}
							log.Printf("Normalized sender id to '%s'\n", Event.SenderUserId)
						}
					}
				}
			case *XMsgDelay:
				Rec := xT.(*XMsgDelay)
				TheTime, err := time.Parse("2006-01-02T15:04:05.999999999Z07", Rec.Stamp)
				log.ErrorIff(err, "Failed to parse time value '%s'\n", Rec.Stamp)
				Event.Timestamp = &TheTime
				if Rec.From != "" {
					Event.SenderUserId = Rec.From
				}
			case *XMsgAddress:
				Rec := xT.(*XMsgAddress)
				if Rec.Address.Type != "ofrom" {
					log.Errorf("Unknown address type; not 'ofrom', but '%s'!\n", Rec.Address.Type)
					continue
				}
				if Rec.Address.Jid != "" {
					Event.SenderUserId = Rec.Address.Jid
				}
				log.Printf("Originally-From address (sender of group chat): %+v\n", Rec)
			default:
				Type := reflect.TypeOf(xT).String()
				log.Errorf("Received unknown type '%s' in message!\n", Type)
			}
		}
		if Event.Timestamp != nil {
			if Event.Timestamp.Before(time.Now().Add(-Dc.RefuseMessagesAfter)) {
				//log.Printf("Rejecting message due to excessive age: %s.\n", Event)
				return
			}
		} else {
			//log.Printf("NO DELAY TIME!\n")
		}
		ChatTarget := getChatTargetChannel(Dc, Event.SenderUserId)
		log.Printf("Got the following target: %s\n", ChatTarget.Identifier())
		Listener.Incoming <- Event
		log.Printf("Received groupchat and placed it into incoming: %s\n", msg)
	default:
		log.Fatalf("Found unknown type: %d on message '%+v'\n", msg.Type, msg)
	}
	//	_, _ = fmt.Fprintf(os.Stdout, "Body = %s - from = %s\n", msg.Body, msg.From)
	//reply := stanza.Message{Attrs: stanza.Attrs{To: msg.From}, Body: msg.Body}
	//log.FatalIff(s.Send(reply), "error sending xmpp")
}

func errorHandler(err error) {
	fmt.Println(err.Error())
}
