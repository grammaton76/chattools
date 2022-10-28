package sc_xmpp

import (
	"fmt"
	"github.com/grammaton76/g76golib/shared"
	"github.com/grammaton76/g76golib/sjson"
	"github.com/grammaton76/g76golib/slogger"
	"gosrc.io/xmpp"
	"gosrc.io/xmpp/stanza"
	"os"
)

var log *slogger.Logger

func NewXmpp(Meta *sjson.JSON) *shared.ChatHandle {
	var Bob shared.ChatHandle
	Bob.ChatType = shared.ChatTypeXmppDirect
	Address := Meta.KeyString("address")
	Domain := Meta.KeyString("domain")
	User := Meta.KeyString("user")
	Password := Meta.KeyString("password")
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

	client, err := xmpp.NewClient(&config, router, errorHandler)
	if err != nil {
		log.Fatalf("%+v", err)
	}
	Bob.NativeClient = *client
	return &Bob
}

func handleMessage(s xmpp.Sender, p stanza.Packet) {
	msg, ok := p.(stanza.Message)
	if !ok {
		_, _ = fmt.Fprintf(os.Stdout, "Ignoring packet: %T\n", p)
		return
	}

	_, _ = fmt.Fprintf(os.Stdout, "Body = %s - from = %s\n", msg.Body, msg.From)
	reply := stanza.Message{Attrs: stanza.Attrs{To: msg.From}, Body: msg.Body}
	log.FatalIff(s.Send(reply), "error sending xmpp")
}

func errorHandler(err error) {
	fmt.Println(err.Error())
}
