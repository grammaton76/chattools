package main

import (
	"flag"
	"fmt"
	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"
	"newtonpub.com/okane/shared"
	_ "newtonpub.com/okane/shared/schat/sc_dbtable"
	"newtonpub.com/okane/shared/slogger"
	"regexp"
	"time"
	"unicode/utf8"
)

var log slogger.Logger

var wQ struct {
	AddTweet         *shared.Stmt
	UpdateLastTweet  *shared.Stmt
	SetTweetAbstract *shared.Stmt
}

var rQ struct {
	GetRecentHistory *shared.Stmt
	TweetedByHandle  *shared.Stmt
	GetWatchlist     *shared.Stmt
}

var Config shared.Configuration

var Cli struct {
	Inifile  string
	ReadOnly bool
	Debug    bool
	ScanOnly string
}

var Re struct {
	ForceSpace *regexp.Regexp
}

var Global struct {
	db                *shared.DbHandle
	Chat              *shared.ChatHandle
	ReadOnly          bool
	HandlesById       map[int]*TweetHandle
	HandlesByName     map[string]*TweetHandle
	ConsumerKey       string
	ConsumerSecret    string
	AccessToken       string
	AccessTokenSecret string
}

type TweetHandle struct {
	WatchId, StatusId int
	Handle            string
}

func LoadConfigValues(inifile string) {
	shared.SetLogger(&log)
	if Cli.Debug {
		log.SetThreshold(slogger.DEBUG)
	}
	OtherIni := flag.String("inifile", "", "Specify an INI file for settings")
	ReadOnly := flag.Bool("readonly", false, "No writes to database or filesystem.")
	Debug := flag.Bool("debug", false, "Enable verbose debugging.")
	ScanOnly := flag.String("scanonly", "", "Give a specific handle to check")
	flag.Parse()
	Cli.Inifile = *OtherIni
	Cli.ReadOnly = *ReadOnly
	Cli.Debug = *Debug
	Cli.ScanOnly = *ScanOnly
	if Cli.Debug {
		log.SetThreshold(slogger.DEBUG)
	}
	Global.ReadOnly = Cli.ReadOnly
	if Cli.Inifile != "" {
		inifile = Cli.Inifile
	}
	Config.SetDefaultIni(inifile).OrDie("Could not load INI file '%s'", inifile)
	Global.Chat = Config.NewChatHandle("tweetmon")
	Global.ConsumerKey = Config.GetStringOrDie("api_twitter.consumer_key", "")
	Global.ConsumerSecret = Config.GetStringOrDie("api_twitter.consumer_secret", "")
	Global.AccessToken = Config.GetStringOrDie("api_twitter.access_token", "")
	Global.AccessTokenSecret = Config.GetStringOrDie("api_twitter.access_token_secret", "")
}

func InitAndConnect() {
	ThreadPurpose := shared.NewThreadPurpose()
	go ThreadPurpose.SetDeadman(5 * time.Second)
	// Block start
	ThreadPurpose.WgAdd(1)
	go func(fwg *shared.ThreadPurpose) {
		defer ThreadPurpose.Done()
		ThreadPurpose.Set("Database setup thread for db_chatdb_rw")
		Global.db = Config.ConnectDbBySectionOrDie("db_chatdb_rw")
		Global.Chat = Config.NewChatHandle("tweetmon")
		Global.Chat.PrintChatOnly = Global.ReadOnly
		rQ.GetRecentHistory = Global.db.PrepareOrDie(
			"SELECT handleid,statusid FROM tracked_tweets WHERE handleid=? ORDER BY id DESC;")
		rQ.GetWatchlist = Global.db.PrepareOrDie(
			"SELECT twitter_watches.id,handle,tracked_tweets.statusid FROM twitter_watches LEFT OUTER JOIN tracked_tweets ON tracked_tweets.id=twitter_watches.lastpost;")
		rQ.TweetedByHandle = Global.db.PrepareOrDie(
			`SELECT 1 FROM tracked_tweets WHERE handleid=? AND statusid=?;`)
		shared.ValidatePreparedQueriesOrDie(rQ)
		if !Global.ReadOnly {
			wQ.AddTweet = Global.db.PrepareOrDie(
				"INSERT INTO tracked_tweets (handleid,statusid,permalink,abstract,encountered) VALUES (?, ?, ?, ?, now());")
			wQ.UpdateLastTweet = Global.db.PrepareOrDie(
				"UPDATE twitter_watches SET lastpost=? WHERE id=?;")
			wQ.SetTweetAbstract = Global.db.PrepareOrDie(
				"UPDATE tracked_tweets SET abstract=? WHERE statusid=? AND handleid=?;")
			shared.ValidatePreparedQueriesOrDie(wQ)
		}
	}(ThreadPurpose)
	// Block stop
	ThreadPurpose.WgWait()
	ThreadPurpose.DisarmDeadman()
	Global.HandlesByName = make(map[string]*TweetHandle)
	Global.HandlesById = make(map[int]*TweetHandle)
	Re.ForceSpace = regexp.MustCompile(`(pic.twitter.com)`)
}

func main() {
	LoadConfigValues("/data/baytor/tweetmon.ini")
	InitAndConnect()
	//Global.ReadOnly = true
	defer Global.db.Close()
	config := oauth1.NewConfig(Global.ConsumerKey, Global.ConsumerSecret)
	token := oauth1.NewToken(Global.AccessToken, Global.AccessTokenSecret)
	httpClient := config.Client(oauth1.NoContext, token)

	// Twitter client
	client := twitter.NewClient(httpClient)

	selDB, err := rQ.GetWatchlist.Query()
	if err != nil {
		log.Fatalf("ERROR '%s' when fetching watchlist.\n", err)
	}
	for selDB.Next() {
		var watchid int
		var handle string
		var statusid int
		log.FatalIff(selDB.Scan(&watchid, &handle, &statusid), "Error scanning row")
		if Cli.ScanOnly != "" && Cli.ScanOnly != handle {
			continue
		}
		//if handle!="UltimateTeamUK" {
		//	continue
		//}
		log.Printf("Scanning %s\n", handle)
		Handle := TweetHandle{
			WatchId:  watchid,
			StatusId: statusid,
			Handle:   handle,
		}
		Global.HandlesById[watchid] = &Handle
		Global.HandlesByName[handle] = &Handle

		var T bool = true
		Tweets, _, err := client.Timelines.UserTimeline(&twitter.UserTimelineParams{
			UserID:          0,
			ScreenName:      handle,
			Count:           10,
			SinceID:         0,
			MaxID:           0,
			TrimUser:        nil,
			ExcludeReplies:  &T,
			IncludeRetweets: nil,
			TweetMode:       "",
		})
		if err != nil {
			log.Fatalf("Tweet fetch error! %s -> %s\n", handle, err)
		}
		for _, tweet := range Tweets {
			/*
				if tweet.IsRetweet {
					//log.Printf("Tweet %s is retweeted; ignoring.\n", tweet.ID)
					continue
				}
				if tweet.IsPin {
					//log.Printf("Tweet %s is pin; ignoring.\n", tweet.ID)
					continue
				}
			*/
			Message := tweet.Text

			Message = Re.ForceSpace.ReplaceAllString(Message, " $1")
			if !utf8.ValidString(Message) {
				Message = tweet.Text
				if !utf8.ValidString(Message) {
					Message = "*INVALID UTF-8 SEQUENCE RECEIVED FROM TWITTER*"
				}
				if utf8.ValidString(Message) {
					log.Debugf("Message '%s' corrected to pass UTF8 filter.\n", Message)
				}
			} else {
				log.Debugf("Message '%s' passes UTF8 filter.\n", Message)
			}
			PermUrl := fmt.Sprintf("https://twitter.com/%s/status/%s", handle, tweet.IDStr)
			TimeParsed, _ := tweet.CreatedAtTime()
			log.Printf("%s tweet %d (at %s): %s\n", handle, tweet.ID, TimeParsed, tweet.Text)
			var Found int = 0
			res, err := rQ.TweetedByHandle.Query(Handle.WatchId, tweet.ID)
			log.FatalIff(err, "Fatal error checking for historical tweet %d by handle %s(%d)", tweet.ID, Handle.Handle, Handle.WatchId)
			for res.Next() {
				log.FatalIff(res.Scan(&Found), "Error scanning row")
			}
			if Found == 1 {
				log.Debugf("%s tweet %d (%s) is already archived.\n", handle, tweet.ID, TimeParsed)
				continue
			}
			if Global.ReadOnly {
				log.Printf("READ-ONLY MODE ENABLED. But we'd send: :bluebird: *%s* has tweeted or retweeted the following: %s\n%s",
					Handle.Handle, tweet.ID, handle, PermUrl, tweet.Text)
			} else {
				_, err := wQ.AddTweet.Exec(Handle.WatchId, tweet.ID, PermUrl, Message)
				switch shared.DbErrorType(Global.db, err) {
				case "duplicate_key":
					log.Errorf("%s tweet %s (%s) is already archived, which is weird because we screened for that and failed.\n", handle, tweet.ID, TimeParsed)
					continue
				}
				if err != nil {
					log.Fatalf("ERROR on adding %s tweet %s: %s\n", handle, Handle.WatchId, err)
				}
				_, err = Global.Chat.SendDefaultf(":bluebird: *%s* has tweeted or retweeted the following: %s\n%s",
					handle, PermUrl, tweet.Text)
				if err != nil {
					log.Fatalf("ERROR on chat: %s\n", err)
				}
			}
			//os.Exit(0)
			time.Sleep(time.Second)
		}
	}
}
