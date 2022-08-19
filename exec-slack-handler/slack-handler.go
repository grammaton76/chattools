package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/grammaton76/g76golib/chatoutput/sc_slack"
	"github.com/grammaton76/g76golib/shared"
	"github.com/grammaton76/g76golib/sjson"
	"github.com/grammaton76/g76golib/slogger"
	"github.com/slack-go/slack"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"time"
)

/*
-- Postgres table schema
CREATE TYPE chatstatus AS ENUM ('PENDING', 'SENT', 'REJECTED', 'SKIPPED');

DROP TABLE chat_messages;
DROP TABLE chat_updates;

CREATE TABLE chat_messages (id SERIAL PRIMARY KEY, status chatstatus NOT NULL, written timestamp with time zone NOT NULL, posted timestamp with time zone, handle varchar(15) NOT NULL, channel varchar(30) NOT NULL, channelid VARCHAR(12), msgid VARCHAR(30), label VARCHAR(80), options json, message text NOT NULL);
CREATE INDEX msgid ON chat_messages(msgid);

CREATE TABLE chat_updates (id SERIAL PRIMARY KEY, status chatstatus NOT NULL, chatid integer NOT NULL, written timestamp with time zone, message text not null, options json);
grant insert on chat_updates to baytor;
grant usage, select on chat_updates_id_seq to baytor;

-- Mysql table schema
CREATE TABLE chat_messages (id SERIAL PRIMARY KEY, status enum('PENDING', 'SENT', 'REJECTED', 'SKIPPED') NOT NULL, written timestamp NOT NULL, posted timestamp, handle varchar(15) NOT NULL, channel varchar(30) NOT NULL, channelid VARCHAR(12), msgid VARCHAR(30), label varchar(80), options json, message text NOT NULL);
CREATE INDEX msgid ON chat_messages(msgid);

CREATE TABLE chat_updates (id SERIAL PRIMARY KEY, status enum('PENDING', 'SENT', 'REJECTED', 'SKIPPED') NOT NULL, chatid integer NOT NULL, written timestamp, options json, message text not null);

*/

var ApiArray map[string]*shared.ChatHandle
var apiPort int
var Config shared.Configuration

var log slogger.Logger

type ApiMessage struct {
	Handle  string
	Channel string
	Options string
	Message string
}

var wQ struct {
	InsertMessage           *shared.Stmt
	MarkUpdateStatus        *shared.Stmt
	MarkStatus              *shared.Stmt
	MarkAllPendingForHandle *shared.Stmt
}

var rQ struct {
	GetChatForUpdate     *shared.Stmt
	SelectPendingChats   *shared.Stmt
	SelectPendingUpdates *shared.Stmt
}

var Global struct {
	ReadOnly bool
	Debug    bool
	db       *shared.DbHandle
	CoreChat *shared.ChatHandle
	PidFile  string
}

func EnsureSlackConnection(handle string) *shared.ChatHandle {
	if handle == "" {
		log.Printf("ERROR: Asked to connect with empty handle '%s'\n", handle)
		return nil
	}
	_, present := ApiArray[handle]
	if present == true {
		return ApiArray[handle]
	}
	log.Printf("Attempting to connect as handle '%s' via token.\n", handle)
	ApiArray[handle] = Config.NewChatHandle(handle)
	return ApiArray[handle]
}

func UpdateSlackChat(handle string, nativechannelid string, MsgId string, message string, optionsjson *string) (retchannelID string, timestamp string, err error) {
	log.Printf("Update for '%s' got channel '%s'\n", MsgId, nativechannelid)
	var MsgOptions shared.ChatOptions
	api := EnsureSlackConnection(handle)
	sapi := api.NativeClient.(*sc_slack.SlackClient)
	if api == nil {
		return "", "", errors.New("failed to connect on update")
	}
	if message != "" {
		Msg := shared.NewChatMessage()
		Msg.MsgType = shared.MsgUpdate
		Msg.UpdateHandle = &shared.ChatUpdateHandle{
			ChannelId: nativechannelid,
		}
		Msg.UpdateHandle.MsgId, _ = strconv.ParseInt(MsgId, 10, 64)
		Msg.Message = message
		_, err = api.Send(Msg)
		log.ErrorIff(err, "Failed to update message via handle '%s'\n", api.Identifier())
	}
	if MsgOptions.PinPost || MsgOptions.UnpinPost {
		Reference := slack.NewRefToMessage(nativechannelid, MsgId)
		log.Printf("Reference: %+v\n", Reference)
		if MsgOptions.PinPost {
			err = sapi.AddPin(nativechannelid, Reference)
		} else {
			err = sapi.RemovePin(nativechannelid, Reference)
		}
		if err != nil {
			log.Printf("Error on pin/unpin operation: '%s'!\n", err)
		}
	}
	if MsgOptions.SetTopic != "" {
		Result, err := sapi.SetTopicOfConversation(retchannelID, MsgOptions.SetTopic)
		if err != nil {
			log.Printf("ERROR on setting topic: '%s'\n", err)
		}
		log.Printf("Result: Attempting to set channel '%s' topic to '%s'\n", retchannelID, Result)
	}
	return retchannelID, timestamp, err
}

func SendToSlackHandle(handle string, channel string, message string, OptionString *string) (string, string, error) {
	api := EnsureSlackConnection(handle)
	if api == nil {
		return "", "", errors.New("failed to connect on send")
	}
	Msg := shared.NewChatMessage()
	Msg.MsgType = shared.MsgNewPost
	Msg.Sender = handle
	Msg.Target = api.ChatTargetChannel(channel)
	Msg.Message = message
	if OptionString != nil {
		Bob := sjson.NewJsonFromString(*OptionString)
		Msg.Options = &Bob
	}
	Update, err := api.SendMessage(Msg)
	log.ErrorIff(err, "failed to send message to '%s'", handle)

	return Update.ChannelId, Update.Timestamp, err
}

func SendErrorChat(msg string, options *string) bool {
	var errstr string
	var err error
	log.Errorf("%s\n", msg)
	err = Global.CoreChat.SendErrorf(msg, options)
	if err != nil {
		errstr = err.Error()
		if errstr != "" {
			log.Errorf("FAILED SENDING ERROR TO '%s': %s\n", Global.CoreChat.ErrorChannel, errstr)
			return false
		}
	}
	return true
}

func RejectRequest(w http.ResponseWriter, result string) {
	w.WriteHeader(401)
	w.Write([]byte(fmt.Sprintf("{\"status\": \"DENIED\", \"reason\": \"%s\"}", result)))
}

func AcceptRequest(w http.ResponseWriter, json string) {
	w.WriteHeader(200)
	w.Write([]byte(json))
}

func ValidateToken(r *http.Request) string {
	IsToken := r.Header.Get("Auth-Source")
	if IsToken == "token" {
		TokenName := r.Header.Get("Remote-Token")
		if TokenName != "" {
			return TokenName
		}
		log.Printf("API: No name associated to the remote token.\n")
		return ""
	}
	log.Printf("API: No valid application token sent; ignoring.\n")
	return ""
}

func ApiSend(w http.ResponseWriter, r *http.Request) {
	log.Printf("API: Received request\n")
	w.Header().Set("Content-Type", "application/sjson")
	TokenName := ValidateToken(r)
	if TokenName == "" {
		RejectRequest(w, "Auth failure")
		return
	}
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading body: %v", err)
		http.Error(w, "can't read body", http.StatusBadRequest)
		return
	}
	var Input ApiMessage
	CawErr := json.Unmarshal([]byte(body), &Input)
	if CawErr != nil {
		log.Printf("Error decoding posted sjson (but auth successful as '%s'). err = %s\n", TokenName, CawErr)
	}
	api := EnsureSlackConnection(Input.Handle)
	if api == nil {
		RejectRequest(w, fmt.Sprintf("No connection for handle %s", Input.Handle))
		return
	}
	if Input.Channel == "" {
		RejectRequest(w, "No channel; can't send to nothing.")
	}
	if Input.Message == "" {
		RejectRequest(w, "No message; can't post an empty message.")
	}
	wQ.InsertMessage.Exec(Input.Handle, Input.Channel, Input.Message, Input.Options)
}

func ConfigAndConnect(inifile string) {
	log.Init()
	slogger.SetLogger(&log)
	shared.SetLogger(&log)
	OtherIni := flag.String("inifile", "", "Specify an INI file for settings")
	ReadOnly := flag.Bool("readonly", false, "No writes to database or filesystem.")
	Debug := flag.Bool("debug", false, "Enable verbose debugging.")
	flag.Parse()
	Global.ReadOnly = *ReadOnly
	Global.Debug = *Debug
	if Global.Debug {
		log.SetThreshold(slogger.DEBUG)
	}
	if *OtherIni != "" {
		inifile = *OtherIni
	}
	log.Debugf("Loading INI file from '%s'\n", inifile)
	Config.SetDefaultIni(inifile).OrDie(fmt.Sprintf("Could not access ini '%s'\n", inifile))

	Global.PidFile = Config.GetStringOrDefault("gateway.pidfile", "gateway.pid", "No PID file specified")
	DbSection := Config.GetStringOrDefault("gateway.dbhandle", "database", "No DB handle specified")
	shared.ExitIfPidActive(Global.PidFile)

	ApiArray = make(map[string]*shared.ChatHandle)
	Global.db = Config.ConnectDbBySection(DbSection).OrDie()

	Global.CoreChat = Config.NewChatHandle("gateway")

	Hostname, _ := os.Hostname()
	err := Global.CoreChat.SendErrorf("Slack handler booted up on %s", Hostname)
	if err != nil {
		log.Printf("...well, that's that. Failed bootup on config '%s'.\nExiting chat startup on '%s' due to '%s'.\nWarnings:\n", Config.Identifier(), Global.CoreChat.Identifier(), err)
		for _, v := range Global.CoreChat.Warnings() {
			log.Printf("%s\n", v)
		}
		os.Exit(3)
	}

	switch Global.db.DbType() {
	case shared.DbTypePostgres:
		wQ.InsertMessage = Global.db.Prepare("INSERT INTO chat_messages(status,written,handle,channel,message,options) VALUES ('PENDING', 'now', $1, $2, $3, $4);").OrDie()
		wQ.MarkUpdateStatus = Global.db.Prepare("UPDATE chat_updates SET status=$1 WHERE id=$2;").OrDie()
		wQ.MarkStatus = Global.db.Prepare("UPDATE chat_messages SET status=$1, posted=NOW(), msgid=$2, channelid=$3 WHERE id=$4;").OrDie()
		wQ.MarkAllPendingForHandle = Global.db.Prepare("UPDATE chat_messages SET status=$3 WHERE id>=$1 AND status='PENDING' AND handle=$2;").OrDie()
		rQ.GetChatForUpdate = Global.db.Prepare("SELECT status, channel, msgid, handle, channelid, options FROM chat_messages WHERE id=$1").OrDie()
		rQ.SelectPendingChats = Global.db.Prepare("SELECT id,handle,channel,message,options FROM chat_messages WHERE status='PENDING' ORDER BY id LIMIT 5;")
		rQ.SelectPendingUpdates = Global.db.Prepare("SELECT id,chatid,message FROM chat_updates WHERE status='PENDING' ORDER BY id;")
	case shared.DbTypeMysql:
		wQ.InsertMessage = Global.db.Prepare("INSERT INTO chat_messages(status,written,handle,channel,message,options) VALUES ('PENDING', 'now', ?, ?, ?, ?);").OrDie()
		wQ.MarkUpdateStatus = Global.db.Prepare("UPDATE chat_updates SET status=? WHERE id=?;").OrDie()
		wQ.MarkStatus = Global.db.Prepare("UPDATE chat_messages SET status=?, posted=NOW(), msgid=?, channelid=? WHERE id=?;").OrDie()
		wQ.MarkAllPendingForHandle = Global.db.Prepare("UPDATE chat_messages SET status=? WHERE id>=? AND status='PENDING' AND handle=?;").OrDie()
		rQ.GetChatForUpdate = Global.db.Prepare("SELECT status, channel, msgid, handle, channelid, options FROM chat_messages WHERE id=?").OrDie()
		rQ.SelectPendingChats = Global.db.Prepare("SELECT id,handle,channel,message,options FROM chat_messages WHERE status='PENDING' ORDER BY id LIMIT 5;")
		rQ.SelectPendingUpdates = Global.db.Prepare("SELECT id,chatid,message FROM chat_updates WHERE status='PENDING' ORDER BY id;")
	default:
		log.Fatalf("Unknown database type '%d'!\n", Global.db.DbType())
	}
	log.Printf("Making connection to Slack endpoint '%s'\n", "(default)")
}

func main() {
	ConfigAndConnect("/data/config/slack-handler.ini")
	WaitTime := time.Second

	if apiPort != 0 {
		log.Printf("Opened API on 127.0.0.1:%d per configuration.\n", apiPort)
		http.HandleFunc("/msghandler/v1/send", ApiSend)
		go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", apiPort), nil)
	} else {
		log.Printf("application.apiport not configured; not opening API connection.\n")
	}

	for true {
		selDB, err := rQ.SelectPendingChats.Query()
		if err != nil {
			panic(err.Error())
		}
		for selDB.Next() {
			var (
				MsgId   int
				handle  string
				channel string
				message string
				options *string
			)
			err = selDB.Scan(&MsgId, &handle, &channel, &message, &options)
			if err != nil {
				panic(err.Error())
			}
			api := EnsureSlackConnection(handle)
			if api == nil {
				SendErrorChat(fmt.Sprintf("Couldn't establish connection for handle '%s'; marking all messages rejected for it.", handle), nil)
				wQ.MarkAllPendingForHandle.Exec("REJECTED", MsgId, handle)
				continue
			}
			channelid, timestamp, err := SendToSlackHandle(handle, channel, message, options)
			var errstr string
			if err != nil {
				errstr = err.Error()
			}
			var res sql.Result
			switch errstr {
			case "":
				res, err = wQ.MarkStatus.Exec("SENT", timestamp, channelid, MsgId)
				if err != nil {
					log.Printf("Error marking message %d posted: %s\n", MsgId, err)
					return
				} else {
					Rows, err := res.RowsAffected()
					log.ErrorIff(err, "failed to get rows affected marking chat_message %d",
						MsgId)
					if Rows == 0 {
						log.Errorf("No rows were affected marking chat_message %d.\n", MsgId)
					} else {
						log.Debugf("%d rows were affected marking chat_message %d.\n", Rows, MsgId)
					}
				}
				log.Printf("Message %d sent by '%s' to channel '%s'.\n", MsgId, handle, channel)
			case "account_inactive":
				_, err = wQ.MarkStatus.Exec("REJECTED", timestamp, nil, MsgId)
				if err != nil {
					log.Printf("Error marking message %d rejected: %s\n", MsgId, err)
					return
				}
				SendErrorChat(fmt.Sprintf("Inactive handle '%s' tried to send message id %d to channel %s:", handle, MsgId, channel), nil)
			case "channel_not_found":
				_, err = wQ.MarkStatus.Exec("REJECTED", timestamp, nil, MsgId)
				if err != nil {
					log.Printf("Error marking message %d rejected: %s\n", MsgId, err)
					return
				}
				SendErrorChat(fmt.Sprintf("'%s' sent chat %d sent to non-existent channel '%s':", handle, MsgId, channel), nil)
			case "message_too_long":
				log.Printf("...excessively long message at %d characters has beeen truncated.\n", len(message))
				return
			default:
				_, MarkErr := wQ.MarkStatus.Exec("REJECTED", timestamp, nil, MsgId)
				if MarkErr != nil {
					log.Printf("Error marking message %d rejected: %s\n", MsgId, err)
					return
				}
				log.Printf("Error posting message: %s\n", err)
				SendErrorChat(fmt.Sprintf("Undefined error '%s' in writing message %d to '%s' for '%s'; restarting handler:", err.Error(), MsgId, channel, handle), nil)
				return
			}
		}
		updateChat, err := rQ.SelectPendingUpdates.Query()
		if err != nil {
			panic(err.Error())
		}
		for updateChat.Next() {
			var chatid, updateid int
			var message, handle, channelid string
			var n_msgts sql.NullString
			err = updateChat.Scan(&updateid, &chatid, &message)
			if err != nil {
				panic(err.Error())
			}
			var ChatStatus, channel string
			var msgoptions *string
			row := rQ.GetChatForUpdate.QueryRow(chatid)
			err := row.Scan(&ChatStatus, &channel, &n_msgts, &handle, &channelid, &msgoptions)
			msgts := n_msgts.String
			if err == sql.ErrNoRows {
				log.Errorf("Message id '%d' not found in db for update; rejecting update.\n", chatid)
				_, err = wQ.MarkUpdateStatus.Exec("REJECTED", updateid)
				log.ErrorIff(err, "error marking chat_update for message '%d' as REJECTED.\n", chatid)
				continue
			}
			if err != nil {
				log.Fatalf("SQL error on GetChatForUpdate(%d): %s\n", chatid, err.Error())
			}
			log.Printf("Record %d found, mapped to message '%s' in '%s': '%s'\n", chatid, msgts, channelid, message)
			if ChatStatus == "PENDING" {
				log.Infof("Update %d received for PENDING chat message %d; skipping.\n", updateid, chatid)
				continue
			}
			_, _, err = UpdateSlackChat(handle, channel, msgts, message, msgoptions)
			var errstr string
			if err != nil {
				errstr = err.Error()
			}
			var res sql.Result
			switch errstr {
			case "":
				log.Printf("No errors. Marking update %d as done.\n", updateid)
				res, err = wQ.MarkUpdateStatus.Exec("SENT", updateid)
			case "channel_not_found":
				log.Printf("Channel '%s' not found by handle '%s'.\n", channelid, handle)
				res, err = wQ.MarkUpdateStatus.Exec("REJECTED", updateid)
			default:
				log.Printf("Undefined error '%s' in writing update\n", err.Error())
				res, err = wQ.MarkUpdateStatus.Exec("REJECTED", updateid)
			}
			log.ErrorIff(err, "failed to update %s db on chat_update %d",
				Global.db.Identifier(), updateid)
			Rows, err := res.RowsAffected()
			log.ErrorIff(err, "failed to get rows affected for chat_update on %s",
				Global.db.Identifier())
			if Rows == 0 {
				log.Errorf("No rows were affected by the status update.\n")
			} else {
				log.Debugf("%d rows were affected by the status update.\n", Rows)
			}
		}
		//log.Debugf("Messages scanned; sleeping for '%s'.\n", WaitTime.String())
		time.Sleep(WaitTime)
	}

}
