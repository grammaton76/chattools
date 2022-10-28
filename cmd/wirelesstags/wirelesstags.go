package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	resty "github.com/go-resty/resty/v2"
	"math/big"
	"net/http"
	"newtonpub.com/okane/shared"
	"newtonpub.com/okane/shared/sjson"
	"newtonpub.com/okane/shared/slogger"
	"os"
	"strings"
	"time"
)

var wQ PreparedQueries_rw

type PreparedQueries_rw struct {
	InsertTag     *shared.Stmt
	UpdateTagData *shared.Stmt
	TagAsAlive    *shared.Stmt
}

var rQ PreparedQueries_ro

type PreparedQueries_ro struct {
	GetAllTags *shared.Stmt
}

type Tag struct {
	Uuid      string
	Enabled   bool
	Firstseen time.Time
	Lastlive  *time.Time
	IsAlive   bool
	Raw       *sjson.JSON
}

var DbTags map[string]Tag

type ArrayOfTagManagerTag struct {
	XMLName       xml.Name `xml:"ArrayOfTagManagerTag"`
	Text          string   `xml:",chardata"`
	Xsd           string   `xml:"xsd,attr"`
	Xsi           string   `xml:"xsi,attr"`
	Xmlns         string   `xml:"xmlns,attr"`
	TagManagerTag struct {
		Text string `xml:",chardata"`
		Mac  string `xml:"mac"`
		Tags struct {
			Text string `xml:",chardata"`
			Tag  []struct {
				Text                   string `xml:",chardata"`
				Dbid                   string `xml:"dbid"`
				NotificationJS         string `xml:"notificationJS"`
				Name                   string `xml:"name"`
				Uuid                   string `xml:"uuid"`
				Comment                string `xml:"comment"`
				SlaveId                string `xml:"slaveId"`
				TagType                string `xml:"tagType"`
				LastComm               string `xml:"lastComm"`
				Alive                  string `xml:"alive"`
				SignaldBm              string `xml:"signaldBm"`
				BatteryVolt            string `xml:"batteryVolt"`
				Beeping                string `xml:"beeping"`
				Lit                    string `xml:"lit"`
				MigrationPending       string `xml:"migrationPending"`
				BeepDurationDefault    string `xml:"beepDurationDefault"`
				EventState             string `xml:"eventState"`
				PendingEventState      string `xml:"pendingEventState"`
				TempEventState         string `xml:"tempEventState"`
				OutOfRange             string `xml:"OutOfRange"`
				PendingTempEventState  string `xml:"pendingTempEventState"`
				PendingCapEventState   string `xml:"pendingCapEventState"`
				PendingLightEventState string `xml:"pendingLightEventState"`
				AiMigrated             string `xml:"aiMigrated"`
				TempSpurTh             string `xml:"tempSpurTh"`
				OorPending             string `xml:"oorPending"`
				SetupTimeout           string `xml:"setupTimeout"`
				Sort                   string `xml:"sort"`
				Lux                    string `xml:"lux"`
				Temperature            string `xml:"temperature"`
				TempCalOffset          string `xml:"tempCalOffset"`
				CapCalOffset           string `xml:"capCalOffset"`
				Cap                    string `xml:"cap"`
				CapRaw                 string `xml:"capRaw"`
				Az2                    string `xml:"az2"`
				CapEventState          string `xml:"capEventState"`
				LightEventState        string `xml:"lightEventState"`
				Shorted                string `xml:"shorted"`
				PostBackInterval       string `xml:"postBackInterval"`
				Rev                    string `xml:"rev"`
				Version1               string `xml:"version1"`
				FreqOffset             string `xml:"freqOffset"`
				FreqCalApplied         string `xml:"freqCalApplied"`
				ReviveEvery            string `xml:"reviveEvery"`
				OorGrace               string `xml:"oorGrace"`
				LBTh                   string `xml:"LBTh"`
				EnLBN                  string `xml:"enLBN"`
				Txpwr                  string `xml:"txpwr"`
				RssiMode               string `xml:"rssiMode"`
				Ds18                   string `xml:"ds18"`
				V2flag                 string `xml:"v2flag"`
				BatteryRemaining       string `xml:"batteryRemaining"`
			} `xml:"Tag"`
		} `xml:"tags"`
	} `xml:"TagManagerTag"`
}

/*
http://mytaglist.com/apidoc.html

Init: http://mytaglist.com/ethAccount.asmx/SignIn - send { email, password } receive { d: string } and an http cookie to read

-- Postgresql
CREATE TABLE tagstatus (uuid varchar(40) primary key not null, enabled boolean not null default true, isalive boolean not null, firstseen timestamp not null default current_timestamp, lastlive timestamp, raw sjson);

-- MySQL
CREATE TABLE tagstatus (uuid varchar(40) primary key not null, enabled boolean not null default 1, isalive boolean not null, firstseen timestamp not null default current_timestamp, lastlive timestamp, raw sjson);

*/

var log slogger.Logger
var Config shared.Configuration

var Global Parameters

type Parameters struct {
	BaseUrl   string
	Username  string
	Password  string
	DbSection string
	Db        *shared.DbHandle
	ReadOnly  bool
	Debug     bool
	Chat      *shared.ChatHandle
}

var Runtime Runtimes

type Runtimes struct {
	AuthCookie *http.Cookie
}

func FiletimeStrToTime(sinput string) time.Time {
	input := big.NewInt(0)
	input.SetString(sinput, 10)
	//log.Debugf("Filetime input:\nstr %s\nint %s\n", sinput, input.String())
	return FiletimeToTime(input)
}

func FiletimeToTime(input *big.Int) time.Time {
	// This is because filetime is '100-nanoseconds since Jan 1 1601'.
	// We add enough years to get to 1970 - see https://www.silisoftware.com/tools/date.php - this is a fancy offset number below

	// Here is going full silliness - note filetime is in 100-nanosecond chunks so we must multiply *100 to get nanoseconds
	input = input.Mul(input, big.NewInt(100))
	// Here we must get nanoseconds of offset
	Offset := big.NewInt(0)
	Offset.SetString("11644473600000000000", 10)
	input = input.Sub(input, Offset)
	Time := time.Unix(0, input.Int64())
	log.Debugf("Actual-time output: %s\n", Time.String())
	return Time
}

func InitAndConnect() {
	DbTags = make(map[string]Tag)
	Global.Db = Config.ConnectDbBySection(Global.DbSection).OrDie()
	switch Global.Db.DbType() {
	case shared.DbTypePostgres:
		rQ.GetAllTags = shared.PrepareOrDie(Global.Db,
			`SELECT uuid,enabled,isalive,firstseen,lastlive,raw FROM tagstatus WHERE enabled=true;`)
	case shared.DbTypeMysql:
		rQ.GetAllTags = shared.PrepareOrDie(Global.Db,
			`SELECT uuid,enabled,isalive,firstseen,lastlive,raw FROM tagstatus WHERE enabled=1;`)

	}
	if !Global.ReadOnly {
		switch Global.Db.DbType() {
		case shared.DbTypePostgres:
			wQ.InsertTag = Global.Db.PrepareOrDie(
				`INSERT INTO tagstatus (uuid,enabled,isalive,firstseen,lastlive,raw) VALUES ($1,true,$2,'now',$3,$4);`)
			wQ.UpdateTagData = Global.Db.PrepareOrDie(
				`UPDATE tagstatus SET raw=$1,isalive=$2,lastlive=$3 WHERE uuid=$4;`)
			wQ.TagAsAlive = Global.Db.PrepareOrDie(
				`UPDATE tagstatus SET isalive=true,lastlive='now' WHERE uuid=$1;`)
		case shared.DbTypeMysql:
			wQ.InsertTag = Global.Db.PrepareOrDie(
				`INSERT INTO tagstatus (uuid,enabled,isalive,firstseen,lastlive,raw) VALUES (?,1,?,CURRENT_TIME(),?,?);`)
			wQ.UpdateTagData = Global.Db.PrepareOrDie(
				`UPDATE tagstatus SET raw=?,isalive=?,lastlive=? WHERE uuid=?;`)
			wQ.TagAsAlive = Global.Db.PrepareOrDie(
				`UPDATE tagstatus SET isalive=1,lastlive=CURRENT_TIME() WHERE uuid=?;`)
		}
		//shared.ValidatePreparedQueriesOrDie(wQ)
	}
}

func LoadConfigValues(Ini string) {
	slogger.SetLogger(&log)
	OtherIni := flag.String("inifile", "", "Specify an INI file for settings")
	ReadOnly := flag.Bool("readonly", false, "No writes to database or filesystem.")
	//CacheMode := flag.Bool("cachemode", false, "Use the cache files; don't fetch live.")
	Debug := flag.Bool("debug", false, "Enable verbose debugging.")
	flag.Parse()
	Global.ReadOnly = *ReadOnly
	if *OtherIni != "" {
		Ini = *OtherIni
	}
	if *Debug {
		log.SetThreshold(slogger.DEBUG)
		Global.Debug = true
	}
	Config.LoadAnIni(Ini)
	Config.KeyPrefix("kumo.")
	Global.BaseUrl = Config.GetStringOrDie("baseurl", "no defined kumo base url")
	Global.Username = Config.GetStringOrDie("username", "no defined kumo username")
	Global.Password = Config.GetStringOrDie("password", "no defined kumo password")
	Config.KeyPrefix("")

	Global.DbSection = Config.GetStringOrDie("wirelesstags.dbhandle", "no database handle defined for fetching events db")
}

func LoadTagsFromDb() {
	selDB, err := rQ.GetAllTags.Query()
	if err != nil {
		panic(err.Error())
	}
	for selDB.Next() {
		var (
			Uuid       string
			Enabled    bool
			sFirstseen string
			sLastlive  *string
			sRaw       *string
			IsAlive    bool
			Lastlive   *time.Time
		)
		err = selDB.Scan(&Uuid, &Enabled, &IsAlive, &sFirstseen, &sLastlive, &sRaw)
		if err != nil {
			panic(err.Error())
		}
		var RawRec *sjson.JSON
		if sRaw != nil {
			RawRec.IngestFromString(*sRaw)
		}
		Firstseen, err := shared.ParseMysqlTime(sFirstseen)
		log.FatalIff(err, "first seen failed to parse")
		if sLastlive != nil {
			Lastlive, err = shared.ParseMysqlTime(*sLastlive)
			log.FatalIff(err, "last seen failed to parse")
		}
		var Caw Tag = Tag{
			Uuid:      Uuid,
			Enabled:   Enabled,
			Firstseen: *Firstseen,
			IsAlive:   IsAlive,
			Lastlive:  Lastlive,
			Raw:       RawRec,
		}
		DbTags[Uuid] = Caw
	}
}

func main() {
	LoadConfigValues("/data/baytor/home/kumo.ini")
	InitAndConnect()
	// Create a Resty Client
	client := resty.New()

	var Url string

	Url = Global.BaseUrl + "/ethAccount.asmx/SignIn"
	log.Printf("URL: %s\n", Url)
	resp, err := client.R().EnableTrace().
		ExpectContentType("application/sjson").
		SetQueryParam("email", Global.Username).
		SetQueryParam("password", Global.Password).
		Get(Url)
	log.FatalIff(err, "Error on signin")
	for _, v := range resp.Cookies() {
		if v.Name == "WTAG" {
			Runtime.AuthCookie = v
		} else {
			log.Printf("Strange cookie found: '%s': %s\n", v.Name, v)
		}
	}
	if resp.RawResponse.StatusCode != 200 {
		log.Printf("Result code %d\n%s\n", resp.RawResponse.StatusCode, resp.String())
		os.Exit(1)
	} else {
		log.Printf("Got a 200 response! Moving on.\n")
	}

	Url = Global.BaseUrl + "/ethAccount.asmx/GetTagManagers"
	log.Printf("URL: %s\n", Url)
	resp, err = client.R().EnableTrace().
		SetCookie(Runtime.AuthCookie).
		ExpectContentType("application/sjson").
		Get(Url)
	log.FatalIff(err, fmt.Sprintf("Error on '%s' get tag managers", Url))
	//log.Printf("Resp: %s\n", resp)

	Url = Global.BaseUrl + "/ethClient.asmx/GetTagManagerTagList"
	log.Printf("URL: %s\n", Url)
	resp, err = client.R().EnableTrace().
		SetCookie(Runtime.AuthCookie).
		ExpectContentType("application/sjson").
		Get(Url)
	log.FatalIff(err, fmt.Sprintf("Error on '%s' get tag manager taglists", Url))

	LoadTagsFromDb()
	var Tags ArrayOfTagManagerTag
	err = xml.Unmarshal(resp.Body(), &Tags)
	log.FatalIff(err, "Error decoding XML!")
	//Body=string(resp.Body())
	//log.Printf("Body: %v\n", Tags)
	AwolThreshold := time.Now().Add(-time.Duration(time.Hour * 2))
	log.Debugf("AWOL threshold is any tag that hasn't reported since '%s'\n", AwolThreshold.String())
	for _, Tag := range Tags.TagManagerTag.Tags.Tag {
		var Lastlive *string
		tLastLive := FiletimeStrToTime(Tag.LastComm)
		sLastlive := shared.FormatMysqlTime(tLastLive)
		Lastlive = &sLastlive
		Uuid := Tag.Uuid
		IsAWOL := tLastLive.After(AwolThreshold)
		if !IsAWOL {
			log.Printf("%s (%s) is AWOL! Last battery indicator %s at %s\n",
				Tag.Uuid, Tag.Name, Tag.BatteryVolt, tLastLive.String())
		}
		Tag.Text = strings.ReplaceAll(Tag.Text, "  ", " ")
		TagRaw, err := json.Marshal(&Tag)
		log.FatalIff(err, "Tag %s failed to render as JSON: '%s'\n", Uuid, TagRaw)
		_, Found := DbTags[Uuid]
		log.Debugf("Tag %s: last seen %s\n", Tag.Name, tLastLive)
		if !Found {
			_, err := wQ.InsertTag.Exec(Uuid, IsAWOL, Lastlive, TagRaw)
			log.FatalIff(err, "failed to insert tag uuid '%s'\n", Uuid)
		} else {
			_, err := wQ.UpdateTagData.Exec(TagRaw, IsAWOL, Lastlive, Uuid)
			log.FatalIff(err, "failed to update tag data for uuid '%s'\n", Uuid)
		}
	}
}
