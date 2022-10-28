package sc_dbtable

import (
	"database/sql"
	"fmt"
	"github.com/grammaton76/g76golib/shared"
	"github.com/grammaton76/g76golib/slogger"
	"time"
)

/*
Expects a db table of the following form (postgres here):

CREATE TYPE public.chatstatus AS ENUM (
    'PENDING',
    'SENT',
    'REJECTED'
);

CREATE TABLE public.chat_messages (
    id integer NOT NULL,
    status public.chatstatus NOT NULL,
    written timestamp with time zone NOT NULL,
    posted timestamp with time zone,
    handle character varying(15) NOT NULL,
    channel character varying(30) NOT NULL,
    msgid character varying(30),
    message text NOT NULL,
    channelid character varying(12),
    options text,
    label character varying(80)
);

CREATE SEQUENCE public.chat_messages_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.chat_messages_id_seq OWNED BY public.chat_messages.id;

ALTER TABLE ONLY public.chat_messages ALTER COLUMN id SET DEFAULT nextval('public.chat_messages_id_seq'::regclass);

ALTER TABLE ONLY public.chat_messages
    ADD CONSTRAINT chat_messages_pkey PRIMARY KEY (id);

CREATE INDEX msgid ON public.chat_messages USING btree (msgid);

CREATE INDEX msg_status ON public.chat_messages USING btree (status);

CREATE TABLE public.chat_updates (
    id integer NOT NULL,
    status public.chatstatus NOT NULL,
    chatid integer NOT NULL,
    written timestamp with time zone,
    message text NOT NULL,
    options text
);

CREATE SEQUENCE public.chat_updates_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE public.chat_updates_id_seq OWNED BY public.chat_updates.id;

ALTER TABLE ONLY public.chat_updates ALTER COLUMN id SET DEFAULT nextval('public.chat_updates_id_seq'::regclass);

*/

var log *slogger.Logger

type chatDbtable struct {
	db                   *shared.DbHandle // We technically shouldn't need a database handle but might as well cache
	chatMessageInsert    *shared.Stmt     // Private prepared query
	chatMessageUpdate    *shared.Stmt     // Private prepared query
	chatMessageGetLabel  *shared.Stmt
	chatMessageGetUpdate *shared.Stmt
}

func SetLogger(l *slogger.Logger) *slogger.Logger {
	log = l
	return l
}

func init() {
	log = shared.GetLogger()
	CTC := shared.ChatTypeConfig{
		BindFunc: BindToHandle,
	}
	shared.ChatHandleConfigs["dbtable"] = &CTC
}

/*
FpSendSimple        func(*ChatHandle, *ChatTarget, string) (string, string, error)
FpSendMessage       func(*ChatHandle, *ChatMessage) (*ChatUpdateHandle, error)
FpFormulateStimulus func(*ChatHandle, *MessagerEvent) *ResponseTo
FpChatTargetChannel func(*ChatHandle, string) *ChatTarget
FpChatTargetUser    func(*ChatHandle, string) *ChatTarget
FpJoinChannel       func(*ChatHandle, *ChatTarget) error
FpUserById          func(*ChatHandle, string) *UserInfo
FpNewListener       func(*ChatHandle) *ListenHandle
FpManageConnection  func(*ChatHandle)
FpIdentifier        func(*ChatHandle) string
FpGetMsgRefByLabel  func(*ChatHandle, *ChatTarget, string) *ChatUpdateHandle
*/

func BindToHandle(cth *shared.ChatHandle, cfg *shared.Configuration, Section string) error {
	cth.FpSendSimple = xFpSendSimple
	cth.FpGetMsgRefByLabel = xFpGetMsgRefByLabel
	cth.FpGetMsgByLabel = xFpGetMsgByLabel
	cth.FpChatTargetChannel = xFpChatTargetChannel
	if cth.PrintChatOnly == true {
		return nil
	}
	DbSection := cfg.GetStringOrDie(Section+".dbhandle", fmt.Sprintf("No database handle specified on chat handle '%s'\n", Section))
	Db := cfg.ConnectDbBySection(DbSection)
	DbChat := &chatDbtable{}
	DbChat.db = Db
	cth.NativeClient = DbChat
	switch Db.DbType() {
	case shared.DbTypePostgres:
		DbChat.chatMessageInsert = Db.PrepareOrDie("INSERT INTO chat_messages (handle, channel, label, status, message, options, written) VALUES ($1, $2, $3, 'PENDING', $4, $5, NOW()) RETURNING id;")
		DbChat.chatMessageUpdate = Db.PrepareOrDie("INSERT INTO chat_updates (chatid,status,written,message,options) values ($1, 'PENDING', CURRENT_TIMESTAMP, $2, $3) ON CONFLICT (chatid) DO UPDATE SET status=excluded.status, written=excluded.written, message=excluded.message, options=excluded.options;")
		DbChat.chatMessageGetLabel = Db.PrepareOrDie("SELECT id,written,message FROM chat_messages WHERE label=$1 AND channel=$2;")
		DbChat.chatMessageGetUpdate = Db.PrepareOrDie("SELECT id,written,message FROM chat_updates WHERE chatid=$1;")
	case shared.DbTypeMysql:
		DbChat.chatMessageInsert = Db.PrepareOrDie("INSERT INTO chat_messages (handle, channel, label, status, message, options, written) VALUES (?, ?, ?, 'PENDING', ?, ?, NOW());")
		DbChat.chatMessageUpdate = Db.PrepareOrDie("REPLACE INTO chat_updates (chatid,status,written,message,options) values (?, 'PENDING', CURRENT_TIME(), ?, ?);")
		DbChat.chatMessageGetLabel = Db.PrepareOrDie("SELECT id,written,message FROM chat_messages WHERE label=? AND channel=?;")
		DbChat.chatMessageGetUpdate = Db.PrepareOrDie("SELECT id,written,message FROM chat_updates WHERE chatid=?;")
	default:
		log.Fatalf("Can't prepare() chat messages for unknown database type on '%s'\n", Db.Identifier())
	}
	cth.Sender = cfg.GetStringOrDefault(Section+".chathandle", "", "Unspecified sender in chat section '%s'\n", Section)
	return nil
}

func xFpChatTargetChannel(cth *shared.ChatHandle, Channel string) *shared.ChatTarget {
	Caw := &shared.ChatTarget{
		Handle:    cth,
		Id:        Channel,
		Name:      Channel,
		IsDM:      false,
		IsMpDm:    false,
		IsAdmin:   false,
		IsPrivate: false,
		IsPublic:  false,
	}
	return Caw
}

func xFpGetMsgByLabel(cth *shared.ChatHandle, Channel *shared.ChatTarget, Label string) *shared.ChatMessage {
	var PostId, UpdateId int64
	var PostText, Written, UpdateText *string
	var posted *time.Time
	nco := cth.NativeClient.(*chatDbtable)
	err := nco.chatMessageGetLabel.QueryRow(Label, Channel).Scan(&PostId, &Written, &PostText)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("No existing label '%s' on '%s'.\n", Label, Channel)
			return nil
		} else {
			log.Printf("Db error checking label '%s' on channel '%s': '%s'!\n", Label, Channel, err)
			return nil
		}
	}
	if Written != nil {
		posted, _ = shared.ParseMysqlTime(*Written)
	}
	RetMsg := &shared.ChatMessage{
		MsgType:      0,
		Target:       Channel,
		Label:        Label,
		LabelIfReply: "",
		UpdateHandle: &shared.ChatUpdateHandle{
			NativeData: nil,
			ChannelId:  "",
			Timestamp:  posted.String(),
			MsgId:      PostId,
			UpdateId:   0,
		},
		Table:   nil,
		Options: nil,
	}
	if PostText != nil {
		RetMsg.Message = *PostText
	}
	err = nco.chatMessageGetUpdate.QueryRow(PostId).Scan(&UpdateId, &Written, &UpdateText)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("No update found for label '%s' on channel '%s'.\n", Label, Channel)
			return RetMsg
		} else {
			log.Printf("Db error checking for update of label '%s' on channel '%s': '%s'!\n", Label, Channel, err)
			RetMsg.UpdateHandle.UpdateId = UpdateId
			if Written != nil {
				RetMsg.History.Posted, _ = shared.ParseMysqlTime(*Written)
			}
			if UpdateText != nil {
				RetMsg.Message = *UpdateText
			}
			return RetMsg
		}
	}
	if Written != nil {
		posted, _ = shared.ParseMysqlTime(*Written)
	}
	log.Printf("Found chat id '%d' label '%s' on channel '%s', with update id '%d'.\n",
		PostId, Label, Channel, UpdateId)
	return RetMsg
}

func xFpSendSimple(cth *shared.ChatHandle, tgt *shared.ChatTarget, msg string) (*shared.ChatUpdateHandle, error) {
	if cth == nil {
		log.Fatalf("ERROR: Sending to null channel!\n")
	}
	var nco *chatDbtable
	if cth.NativeClient != nil {
		nco = cth.NativeClient.(*chatDbtable)
	}
	if tgt == nil {
		if cth.OutputChannel != nil {
			tgt = cth.OutputChannel
		} else {
			log.Fatalf("%s: output channel undefined, and no default set.\n", cth.Identifier())
		}
	}
	log.Debugf("SendOut: '%s' -> '%s'\n", tgt.Identifier(), msg)
	if tgt == nil {
		log.Infof("SUPPRESSED blank-channel chat message by '%s' to channel '%s': '%s'\n",
			cth.Sender, tgt.Identifier(), msg)
		return nil, fmt.Errorf("No channel specified for message sending text '%s'\n", msg)
	}
	if cth.PrintChatOnly {
		log.Infof("SUPPRESSED chat message by '%s' to channel '%s': '%s'\n",
			cth.Sender, tgt.Identifier(), msg)
		return nil, nil
	}
	if cth.Sender == "" {
		log.Infof("SUPPRESSED blank-sender chat message by '%s' to channel '%s': '%s'\n",
			cth.Sender, tgt.Identifier(), msg)
		return nil, fmt.Errorf("Blank sender '%s' on message '%s'\n", cth.Sender, msg)
	}
	if nco.chatMessageInsert == nil {
		log.Fatalf("Can't do it; there is a null chatMessageInsert.\n")
	}
	if cth.Sender == "" {
		log.Fatalf("Can't do it; there is a blank Sender.\n")
	}
	_, IdErr := shared.RunAndGetLastInsertId(nco.chatMessageInsert, cth.Sender, tgt.Identifier(), nil, msg, nil)
	if IdErr != nil {
		log.Fatalf("ERROR on chat.chatMessageInsert: %s\n", IdErr)
	}
	return nil, IdErr
}

func xFpGetMsgRefByLabel(cth *shared.ChatHandle, Channel *shared.ChatTarget, Label string) *shared.ChatUpdateHandle {
	var Id int64
	var text *string
	var written *string
	nco := cth.NativeClient.(*chatDbtable)
	err := nco.chatMessageGetLabel.QueryRow(Label, Channel).Scan(&Id, &written, &text)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("No existing label '%s' on '%s'.\n", Label, Channel)
			return nil
		} else {
			log.Printf("Db error checking label '%s' on channel '%s': '%s'!\n", Label, Channel, err)
			return nil
		}
	}
	log.Printf("Found existing chat id '%d' already holds label '%s' on channel '%s'.\n", Id, Label, Channel)
	Caw := shared.ChatUpdateHandle{
		NativeData: nil,
		ChannelId:  "",
		Timestamp:  "",
		MsgId:      Id,
		UpdateId:   0,
	}
	return &Caw
}
