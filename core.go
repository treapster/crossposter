package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	vkApi "github.com/SevereCloud/vksdk/v2/api"
	_ "github.com/mattn/go-sqlite3"
	tele "gopkg.in/telebot.v3"
)

type botReplies struct {
	okAdded           string
	helpMsg           string
	invalidRequest    string
	noSuchGroup       string
	noSuchChannel     string
	noSuchUser        string
	groupPrivate      string
	userPrivate       string
	queryFailed       string
	noSuchSub         string
	delSuccess        string
	noSubs            string
	notAdmin          string
	alreadySubscribed string
	subsLimitReached  string
}

var i18n = map[string]botReplies{
	"ru": {
		invalidRequest: "Инвалид сюнтах",
		helpMsg: `<b>Команды для себя</b>:

<code>/add vk.com/group me s</code> - получать в лс все посты группы(или личной страницы) с ссылкой на исходный пост в конце сообщения с текстом <i>[Имя паблика]</i>.

<code>/add vk.com/group me</code> - получать в лс все посты группы(или личной страницы) без ссылки на источник

<b>Команды для канала(сначала сделать бота админом, разрешить сообщения)</b>:

<code>/add vk.com/group @channel s</code> - кросспостить посты из группы или страницы в канал со ссылкой на источник

<code>/add vk.com/group @channel</code> - кросспостить из вк в канал без указания источника

<code>/add vk.com/group [id] [s]</code> - для приватных каналов без юзернейма, отправляй id канала. Ставь s в конце чтоб была ссылка на пост, id можно получить добавив в канал @username_to_id_bot.
(Когда-нибудь я научу бота узнавать id самостоятельно, но не сегодня)

<b>Общие</b>:

/ls - показать подписки

<code>/del [номер]</code> - удалить подписку с данным номером`,
		okAdded:           "%s теперь подписан на %s",
		noSuchGroup:       "Группа %s не существует",
		noSuchUser:        "Пользователь %s не существует",
		noSuchChannel:     "Канал %s не существует",
		groupPrivate:      "Группа %s закрыта или заблокирована",
		userPrivate:       "Страница пользователя %s заблокирована или скрыта",
		queryFailed:       "Не удалось выполнить запрос из-за неизвестной ошибки",
		noSuchSub:         "Нет подписки с таким id",
		alreadySubscribed: "%s уже подписан на vk.com/%s",
		delSuccess:        "Подписка %d успешно удалена",
		noSubs:            "Список каналов пуст",
		notAdmin:          "Как минимум одному из нас не хватает прав администратора этого чата. Они должны быть у нас обоих.",
		subsLimitReached:  "Достигнут лимит в %d подписок для одного пользователя.",
	},
}

type CrossposterConfig struct {
	VkToken      string
	VkAudioToken string
	VkApiVersion string
	TgToken      string
	DbName       string

	// Time in minutes between requests for updates to vk.
	// It is an upper bound on how much time will pass between a post
	// appearing on VK page and the bot picking it up and sending
	// in telegram. The more pages the bot tracks, the more time it will need
	// to process all updates because it needs to avoid api limits and sleep
	// between requests. I think you can start at 3-5 minutes, but as you get more
	// users and followed pages, you may need to increase it.
	UpdatePeriod int64

	// How many vk pages are checked for updates in one query.
	// 20 is max supported by vk API, in practice 13 or more can
	// hit the limit. I use 12.
	BatchSize int

	// How many latest posts are fetched through wall.get for each page.
	// 100 is max allowed by vkAPI.
	// Only the new ones are returned, so for small update periods (<10 mins)
	// fetching a couple dozen will be enough, unless the page makes several
	// posts a minute.
	NPostsToFetch int

	// Max subscriptions per user
	SubsLimit int
	// For priveledged commands
	BotAdmins []int64
}

type resolvedVkId struct {
	ScreenName string
	Name       string
}

// we have one instance of vk api with service token and other
// with kate mobile token obtained by https://github.com/vodka2/vk-audio-token.
// kate token allows to download audio but i don't use it for anything else
// to not get banned or anything.
type Crossposter struct {
	vk               *vkApi.VK
	vkAudio          *vkApi.VK
	vkIdCache        CacheMap[int64, resolvedVkId]
	updatePeriod     time.Duration
	tgBot            *tele.Bot
	db               *sql.DB
	dbName           string
	dbSelectStmt     *sql.Stmt
	dbDelStmt        *sql.Stmt
	dbFindPubSubStmt *sql.Stmt
	dbSelectAllStmt  *sql.Stmt
	dbUpdateStmt     *sql.Stmt
	dbReadPubsStmt   *sql.Stmt
	dbReadSubsStmt   *sql.Stmt
	addMsgRegex      *regexp.Regexp
	delMsgRegex      *regexp.Regexp
	chDone           chan bool
	ps               pubsub
	wg               sync.WaitGroup
	batchSize        int
	nPostsToFetch    int
	subsLimit        int
	stats            stats
	botAdmins        []int64
}

type pubSubData struct {
	userID   int64
	pubID    int64
	subID    int64
	lastPost int64
	flags    uint64
}

const (
	reqSubscribe   string = "/add"
	reqUnsubscribe string = "/del"
	reqShowSubs    string = "/ls"
	reqHelp        string = "/help"
	reqStart       string = "/start"
	reqStats       string = "/stats"
	kateUserAgent  string = "KateMobileAndroid/56 lite-460 (Android 4.4.2; SDK 19; x86; unknown Android SDK built for x86; en)"
)

type userError struct {
	code          int
	vkUserOrGroup string
	tgUserOrGroup string
	subsLimit     int
}

const (
	errNoSuchGroup int = iota + 1
	errGroupPrivate
	errNoSuchUser
	errUserPrivate
	errNoSuchChannel
	errInvalidRequest
	errUserNotAdmin
	errNoSubs
	errNoSuchSub
	errAlreadySubscribed
	errSubsLimitReached
)

const (
	addCommandShowSource = `s`

	regexAddSub = `^` + reqSubscribe +
		`\s+` +
		`(?:(?:https??://)?)` +
		`(?:vk.com/(?P<vk>[a-zA-Z0-9_\.]+))\s+` +
		`(?P<tg>(?:@[a-zA-Z][0-9a-zA-Z_]{4,})|(?:-?[0-9]+)|me)` +
		`(?P<link_data>` +
		`(\s+` + addCommandShowSource + `)|()` +
		`)\s*$`

	regexDelSub = `^` + reqUnsubscribe + `\s+([0-9]{1,4})$`
)

// it is dummy method, user errors are handled by HandleUserError
func (err userError) Error() string {
	switch err.code {
	case errNoSuchGroup:
		return "No such group"
	case errGroupPrivate:
		return "Group is private"
	case errNoSuchUser:
		return "user doesn't exist"
	case errNoSuchChannel:
		return "No such channel"
	}
	return "Unknown error"
}

func (cp *Crossposter) isUserAdmin(user int64, chat int64) bool {
	admins, err := cp.tgBot.AdminsOf(&tele.Chat{ID: chat})
	if err != nil {
		return false
	}
	for _, a := range admins {
		if a.User.ID == user {
			return true
		}
	}
	return false
}
func (cp *Crossposter) resolveVkId(id int64) (resolvedVkId, error) {
	const vkIdCacheValidDays = 2
	if res, e := cp.vkIdCache.Get(id); e {
		return res, nil
	}
	var err error
	if id > 0 {
		var usr vkApi.UsersGetResponse
		usr, err = cp.vk.UsersGet(vkApi.Params{
			"user_ids": id,
			"fields":   "screen_name",
			"lang":     "ru",
		})
		if err == nil && len(usr) > 0 {
			res := resolvedVkId{usr[0].ScreenName, usr[0].FirstName + " " + usr[0].LastName}
			cp.vkIdCache.Put(id, res, approxNDaysFromNow(vkIdCacheValidDays))
			return res, nil
		}
	} else {
		var group vkApi.GroupsGetByIDResponse
		group, err = cp.vk.GroupsGetByID(vkApi.Params{
			"group_ids": -id,
		})
		if err == nil && len(group) > 0 {
			res := resolvedVkId{group[0].ScreenName, group[0].Name}
			cp.vkIdCache.Put(id, res, approxNDaysFromNow(vkIdCacheValidDays))
			return res, nil
		}
	}
	return resolvedVkId{}, fmt.Errorf("Failed to resolve vk id %d: %s: ", id, err.Error())
}
func (cp *Crossposter) vkScreenNameById(id int64) (string, error) {

	res, err := cp.resolveVkId(id)
	if err != nil {
		return "", err
	}
	return res.ScreenName, nil
}
func (cp *Crossposter) vkNameById(id int64) (string, error) {

	res, err := cp.resolveVkId(id)
	if err != nil {
		return "", err
	}
	return res.Name, nil
}
func (cp *Crossposter) ResolveTgID(id int64) (string, error) {
	chat, err := cp.tgBot.ChatByID(id)
	if err != nil {
		return "", err
	}

	if chat.Username == "" {
		switch chat.Type {
		case "private":
			return chat.FirstName + " " + chat.LastName, nil
		default:
			return chat.Title, nil
		}
	} else {
		return "@" + chat.Username, nil
	}
}
func (cp *Crossposter) ResolveTgName(name string) (int64, error) {
	chat, err := cp.tgBot.ChatByUsername(name)
	if err != nil {
		return 0, err
	}
	return chat.ID, nil
}
func (cp *Crossposter) resolveVkName(name string) (int64, error) {
	vkResp, err := cp.vk.UtilsResolveScreenName(vkApi.Params{
		"screen_name": name,
	})

	if err != nil {
		log.Printf("resolveVkName: %s", err.Error())
		return 0, userError{code: errNoSuchGroup, vkUserOrGroup: name}
	}

	var id int64
	switch vkResp.Type {
	case "group":
		id = int64(vkResp.ObjectID)
		res, err := cp.vk.GroupsGetByID(vkApi.Params{
			"group_ids": id,
		})
		if err == nil && len(res) > 0 {
			if res[0].IsClosed == 0 && res[0].Deactivated == "" {
				return -id, nil
			}
			return 0, userError{code: errGroupPrivate, vkUserOrGroup: name}
		}
		return 0, userError{code: errNoSuchGroup, vkUserOrGroup: name}

	case "user":
		id = int64(vkResp.ObjectID)
		res, err := cp.vk.UsersGet(vkApi.Params{
			"user_ids": id,
			"fields":   "can_see_all_posts",
		})
		if err == nil && len(res) > 0 {
			if !res[0].IsClosed && res[0].Deactivated == "" && res[0].CanSeeAllPosts {
				return id, nil
			}
			return 0, userError{code: errUserPrivate, vkUserOrGroup: name}
		}
		return 0, userError{code: errNoSuchUser, vkUserOrGroup: name}
	default:
		return 0, userError{code: errNoSuchGroup, vkUserOrGroup: name}
	}
}
func getLang(c tele.Context) string {
	return "ru"
}
func handleUserError(err userError, c tele.Context) {
	lang := getLang(c)
	switch err.code {
	case errInvalidRequest:
		c.Send(i18n[lang].invalidRequest)
	case errNoSuchGroup:
		c.Send(fmt.Sprintf(i18n[lang].noSuchGroup, "vk.com/"+err.vkUserOrGroup))
	case errNoSuchUser:
		c.Send(fmt.Sprintf(i18n[lang].noSuchUser, "vk.com/"+err.vkUserOrGroup))
	case errNoSuchChannel:
		c.Send(fmt.Sprintf(i18n[lang].noSuchChannel, err.tgUserOrGroup))
	case errGroupPrivate:
		c.Send(fmt.Sprintf(i18n[lang].groupPrivate, "vk.com/"+err.vkUserOrGroup))
	case errUserPrivate:
		c.Send(fmt.Sprintf(i18n[lang].userPrivate, "vk.com/"+err.vkUserOrGroup))
	case errNoSubs:
		c.Send(i18n[lang].noSubs)
	case errUserNotAdmin:
		c.Send(i18n[lang].notAdmin)
	case errNoSuchSub:
		c.Send(i18n[lang].noSuchSub)
	case errAlreadySubscribed:
		c.Send(fmt.Sprintf(i18n[lang].alreadySubscribed, err.tgUserOrGroup, err.vkUserOrGroup))
	case errSubsLimitReached:
		c.Send(fmt.Sprintf(i18n[lang].subsLimitReached, err.subsLimit))
	}
}

func handleErrors(err error, c tele.Context) {
	switch e := err.(type) {
	case userError:
		handleUserError(e, c)
	default:
		lang := getLang(c)
		c.Send(i18n[lang].queryFailed)
		log.Printf("Internal error: %s", err.Error())
	}
}
func (cp *Crossposter) handleDel(c tele.Context) error {
	msg := c.Text()
	matches := cp.delMsgRegex.FindStringSubmatch(msg)
	if len(matches) != 2 {
		return userError{code: errInvalidRequest}
	}
	lang := getLang(c)
	pubSubID, _ := strconv.ParseInt(matches[1], 10, 64)

	rows, err := cp.dbFindPubSubStmt.Query(pubSubID, c.Sender().ID)
	if err != nil {
		return err
	}
	if !rows.Next() {
		return userError{code: errNoSuchSub}
	}
	var pubID, subID int64
	err = rows.Scan(&pubID, &subID)
	if err != nil {
		return err
	}

	rows.Close()
	_, err = cp.db.Exec("begin transaction;"+
		"delete from pubSub where userID=? and pubSubID=?;"+
		"delete from publishers where id not in (select pubID from pubSub);"+
		"delete from subscribers where id not in (select subID from pubSub);"+
		"commit;", c.Sender().ID, pubSubID, pubID)
	if err != nil {
		return err
	}
	cp.ps.unsubscribe(subID, pubID)
	c.Send(fmt.Sprintf(i18n[lang].delSuccess, pubSubID))
	log.Printf("PubSub %d owned by %s deleted\n", pubSubID, c.Sender().Username)
	return nil
}

func (cp *Crossposter) handleHelp(c tele.Context) error {
	lang := getLang(c)
	return c.Send(i18n[lang].helpMsg)
}
func (cp *Crossposter) handleAdd(c tele.Context) error {
	msg := c.Text()
	matches := cp.addMsgRegex.FindStringSubmatch(msg)
	if len(matches) < 4 {
		return userError{code: errInvalidRequest}
	}
	vkName, tgName, tgLinkData := matches[1], matches[2], matches[3]
	var flags uint64 = 0
	if strings.Contains(tgLinkData, addCommandShowSource) {
		flags |= flagAddLinkToPost
	}

	vkId, err := cp.resolveVkName(vkName)
	if err != nil {
		return err
	}
	var tgId int64
	userID := c.Sender().ID
	lang := getLang(c)
	if tgName == "me" {
		tgId = userID
		tgName = c.Sender().FirstName + c.Sender().LastName
	} else {
		tgId, err = cp.ResolveTgName(tgName)
		if err != nil {
			return userError{code: errNoSuchChannel, tgUserOrGroup: tgName}
		}
		if !cp.isUserAdmin(userID, tgId) {
			return userError{code: errUserNotAdmin} // this return is a single thing that prevents users from messing each other's subscriptions
		}
	}

	res := pubSubData{
		userID:   userID,
		pubID:    vkId,
		subID:    tgId,
		lastPost: 0,
	}

	_, err = cp.db.Exec(
		"begin transaction;"+
			"insert or ignore into publishers (id, lastPost) values(?,(select strftime('%s')));"+
			"insert or ignore into subscribers (id, flags) values(?, ?);"+
			"insert or rollback into pubSub (userID, pubID, subID, flags) values (?, ?, ?, ?);"+
			"commit;",
		res.pubID, res.subID, flags, userID, res.pubID, res.subID, flags)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return userError{code: errAlreadySubscribed,
				tgUserOrGroup: tgName,
				vkUserOrGroup: vkName,
			}
		}
		if strings.Contains(err.Error(), "too many subscriptions") {
			// log the event so that we know when users want more subscriptions
			log.Printf("User %d exhausted subscriptions limit and wanted more\n", userID)
			return userError{code: errSubsLimitReached,
				tgUserOrGroup: tgName,
				vkUserOrGroup: vkName,
				subsLimit:     cp.subsLimit,
			}
		}
		return err
	}
	cp.ps.subscribe(tgId, vkId, flags, func(ch <-chan update) {
		cp.listenAndForward(ch, tgId)
	})
	c.Send(fmt.Sprintf(i18n[lang].okAdded, tgName, "vk.com/"+vkName))
	log.Printf("%d (%s) subscribed to vk.com/%s\n", tgId, tgName, vkName)
	return nil
}
func (cp *Crossposter) handleShow(c tele.Context) error {
	rows, err := cp.dbSelectStmt.Query(c.Sender().ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	msg := ""
	patt := "[%d] %s => %s\n"
	for rows.Next() {
		var (
			id  int64
			pub int64
			sub int64
		)
		err := rows.Scan(&id, &pub, &sub)
		if err != nil {
			return err
		}
		vkName, err := cp.vkScreenNameById(pub)
		if err != nil {
			vkName = "[DELETED]"
		} else {
			vkName = "vk.com/" + vkName
		}
		tgName, err := cp.ResolveTgID(sub)
		if err != nil {
			tgName = "[DELETED]"
		}
		msg += fmt.Sprintf(patt, id, vkName, tgName)
	}
	if msg == "" {
		return userError{code: errNoSubs}
	}
	return c.Send(msg)
}

func (cp *Crossposter) handleStats(c tele.Context) error {

	sql :=
		`select * from (select count(*) from subscribers where id > 0),
(select count(*) from subscribers where id < 0),
(select count(*) from publishers),
(select count(*) from pubsub),
(select count(*) from (select distinct userID from pubsub));`
	rows, err := cp.db.Query(sql)
	if err != nil {
		log.Print(err.Error())
		return c.Send(err.Error())
	}

	var subsPeople, subsChannels, publishers, subscriptions, users int64
	for rows.Next() {
		rows.Scan(&subsPeople, &subsChannels, &publishers, &subscriptions, &users)
	}

	dbInfo := fmt.Sprintf(`%d subscribed people
%d subscribed channels
%d publishers
%d subscriptions
%d total users`, subsPeople, subsChannels, publishers, subscriptions, users)

	totalPosts, lastHour, uptime := cp.stats.get()
	d := uptime / (24 * 3600)
	hr := (uptime % (24 * 3600)) / 3600
	min := (uptime % 3600) / 60
	sec := uptime % 60
	uptimeInfo := fmt.Sprintf("Uptime %dd %dhr %dmin %ds", d, hr, min, sec)
	hrFloat := float64(uptime) / 3600
	avgPosts := float64(totalPosts) / hrFloat
	postsInfo := fmt.Sprintf("%d total posts since launch, %d last hour, avg %.2f/hr", totalPosts, lastHour, avgPosts)
	msg := strings.Join([]string{uptimeInfo, postsInfo, dbInfo}, "\n")
	return c.Send(msg)
}

func createTableIfNotExists(db *sql.DB, subsLimit int) (sql.Result, error) {
	trigger := fmt.Sprintf(
		// remove prev trigger to propagate possible changes to subsLimit
		// to the database
		"drop trigger if exists checkSubsCount;\n"+
			"create trigger checkSubsCount before insert on pubSub\n"+
			"begin\n"+
			"select case when (select count(*) from pubsub where userID=NEW.userID) >= %d then raise(ROLLBACK, \"too many subscriptions\") else '' end;\n"+
			"end;",
		subsLimit)
	return db.Exec(
		"create table if not exists publishers" +
			"(id integer primary key, lastPost integer);" +
			"create table if not exists subscribers" +
			"(id integer primary key, flags integer);" +
			"create table if not exists pubSub" +
			"(pubSubID integer primary key, userID integer, pubID integer, subID integer, flags integer, " +
			"unique(pubID, subID), " +
			"foreign key (pubID) references publishers(id)," +
			"foreign key (subID) references subscribers(id));" +
			"create index if not exists pub on pubSub (pubID);" +
			"create index if not exists sub on pubSub (subID);" +
			"create index if not exists user on pubSub (userID);" +
			trigger)
}

func (cp *Crossposter) updateTimeStamp(id int64, newTimeStamp int64) {
	cp.ps.updateTimeStamp(id, newTimeStamp)
	_, err := cp.dbUpdateStmt.Exec(newTimeStamp, id)
	if err != nil {
		log.Printf("Failed to update db for publisher %d and lastPost %d:\n%s\n", id, newTimeStamp, err.Error())
	}
}
func openDB(dbName string) (*sql.DB, error) {

	if _, err := os.Stat(dbName); errors.Is(err, os.ErrNotExist) {
		_, err := os.Create(dbName)
		if err != nil {
			return nil, fmt.Errorf("openDB: couldn't create database file%s:\n%w", dbName, err)
		}
	}
	db, err := sql.Open("sqlite3", dbName)
	if err != nil {
		return nil, fmt.Errorf("openDB: couldn't open database file %s:\n%w", dbName, err)
	}
	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("openDB: couldn't connect to database:\n" + err.Error())
	}
	log.Printf("Opened database %s successfully\n", dbName)

	return db, nil
}
func (cp *Crossposter) prepareStatements() error {
	var err error

	cp.dbSelectStmt, err =
		cp.db.Prepare("select pubSubID, pubID, subID from pubSub where userID=?;")

	if err != nil {
		return fmt.Errorf("failed to prepare insert statement:\n%w", err)
	}
	cp.dbDelStmt, err =
		cp.db.Prepare("delete from pubSub where userID=? and pubSubID=?;")
	if err != nil {
		return fmt.Errorf("failed to prepare count statement:\n%w", err)
	}
	cp.dbFindPubSubStmt, err =
		cp.db.Prepare("select pubID, subID from pubSub where pubSubID=? and userID=?;")
	if err != nil {
		return fmt.Errorf("failed to prepare find statement:\n%w", err)
	}
	cp.dbSelectAllStmt, err =
		cp.db.Prepare("select * from pubSub;")
	if err != nil {
		return fmt.Errorf("failed to prepare select all statement:\n%w", err)
	}
	cp.dbUpdateStmt, err =
		cp.db.Prepare("update publishers set lastPost=? where id=?")
	if err != nil {
		return fmt.Errorf("failed to prepare update statement:\n%w", err)
	}
	cp.dbReadPubsStmt, err = cp.db.Prepare("select id, lastPost from publishers;")
	if err != nil {
		return fmt.Errorf("failed to prepare read statement:\n%w", err)
	}
	cp.dbReadSubsStmt, err = cp.db.Prepare("select id from subscribers;")
	if err != nil {
		return fmt.Errorf("failed to prepare read statement:\n%w", err)
	}
	return nil
}
func (cp *Crossposter) initDB() error {
	var err error
	cp.db, err = openDB(cp.dbName)
	if err != nil {
		return err
	}
	_, err = createTableIfNotExists(cp.db, cp.subsLimit)
	if err != nil {
		return fmt.Errorf("initDB: failed to createTableIfNotExists:\n%w", err)
	}
	err = cp.prepareStatements()
	if err != nil {
		return err
	}
	return nil
}
func (cp *Crossposter) readDB() error {
	rows, err := cp.dbReadPubsStmt.Query()
	if err != nil {
		return err
	}
	var id, lastPost int64
	for rows.Next() {
		err = rows.Scan(&id, &lastPost)
		if err != nil {
			return err
		}
		cp.ps.addPublisher(id, vkSource{lastPost: lastPost, subs: make(subscribersMap)})
	}
	rows, err = cp.dbReadSubsStmt.Query()
	if err != nil {
		return err
	}
	for rows.Next() {
		err = rows.Scan(&id)
		if err != nil {
			return err
		}
		id := id
		cp.ps.addSubscriber(id, func(ch <-chan update) {
			cp.listenAndForward(ch, id)
		})
	}
	rows, err = cp.dbSelectAllStmt.Query()
	if err != nil {
		return err
	}

	for rows.Next() {
		var ps pubSubData
		var rowID int64
		err = rows.Scan(&rowID, &ps.userID, &ps.pubID, &ps.subID, &ps.flags)
		if err != nil {
			return err
		}
		cp.ps.subscribeSimple(ps.subID, ps.pubID, ps.flags)
	}
	return nil
}

func (cp *Crossposter) isUserBotAdmin(id int64) bool {
	for _, adminID := range cp.botAdmins {
		if id == adminID {
			return true
		}
	}
	return false
}

func (cp *Crossposter) setHandlers() {
	type commandHandler = func(*Crossposter, tele.Context) error
	regularHandler := func(impl commandHandler) tele.HandlerFunc {
		cp := cp
		return func(c tele.Context) error {
			return impl(cp, c)
		}
	}

	priveledgedHandler := func(impl commandHandler) tele.HandlerFunc {
		cp := cp
		return func(c tele.Context) error {
			if !cp.isUserBotAdmin(c.Sender().ID) {
				return nil
			}
			return impl(cp, c)
		}
	}

	cp.tgBot.Handle(reqSubscribe, regularHandler((*Crossposter).handleAdd))
	cp.tgBot.Handle(reqHelp, regularHandler((*Crossposter).handleHelp))
	cp.tgBot.Handle(reqShowSubs, regularHandler((*Crossposter).handleShow))
	cp.tgBot.Handle(reqUnsubscribe, regularHandler((*Crossposter).handleDel))
	cp.tgBot.Handle(reqStart, regularHandler((*Crossposter).handleHelp))
	cp.tgBot.Handle(reqStats, priveledgedHandler((*Crossposter).handleStats))

}

func NewCrossposter(cfg CrossposterConfig) (*Crossposter, error) {
	cp := &Crossposter{}
	cp.vk = vkApi.NewVK(cfg.VkToken)
	cp.vkAudio = vkApi.NewVK(cfg.VkAudioToken)
	cp.vkAudio.UserAgent = kateUserAgent
	if cfg.UpdatePeriod < 1 {
		return nil, fmt.Errorf("UpdatePeriod not provided")
	}
	cp.updatePeriod = time.Minute * time.Duration(cfg.UpdatePeriod)
	if cfg.BatchSize < 1 {
		return nil, fmt.Errorf("BatchSize not provided")
	}
	cp.batchSize = cfg.BatchSize

	if cfg.NPostsToFetch < 1 {
		return nil, fmt.Errorf("NPostsToFetch not provided")
	}
	cp.nPostsToFetch = cfg.NPostsToFetch

	if cfg.SubsLimit < 1 {
		return nil, fmt.Errorf("SubsLimit not provided")
	}
	cp.subsLimit = cfg.SubsLimit

	if len(cfg.BotAdmins) > 0 {
		cp.botAdmins = cfg.BotAdmins
	} else {
		log.Print("BotAdmins not provided, /stats command won't work\n")
	}
	var err error
	cp.tgBot, err = tele.NewBot(tele.Settings{
		Token:     cfg.TgToken,
		Poller:    &tele.LongPoller{Timeout: 10 * time.Second},
		ParseMode: "HTML",
		OnError:   handleErrors,
	})

	if err != nil {
		return nil, fmt.Errorf("NewCrossposter: failed to build telegram bot:\n%w", err)
	}
	cp.setHandlers()
	cp.addMsgRegex = regexp.MustCompile(regexAddSub)
	cp.delMsgRegex = regexp.MustCompile(regexDelSub)

	cp.dbName = cfg.DbName
	err = cp.initDB()
	if err != nil {
		return nil, fmt.Errorf("NewCrossposter: failed to init DB:\n%w", err)
	}

	cp.chDone = make(chan bool)
	cp.ps.pubToSub = make(map[int64]vkSource)
	cp.ps.subscribers = make(map[int64]subscriber)
	err = cp.readDB()
	if err != nil {
		return nil, fmt.Errorf("Failed to read db:\n%w", err)
	}
	cp.vkIdCache = NewCacheMap[int64, resolvedVkId](1000)
	return cp, nil
}
func (cp *Crossposter) Start() {
	cp.stats.startTime = time.Now().Unix()
	go cp.startCrossposting()
	cp.tgBot.Start()
}
func (cp *Crossposter) Stop() {
	log.Printf("Shutting down, please wait\n")
	cp.tgBot.Stop()
	log.Printf("Stopped Telegram bot\n")
	cp.db.Close()
	log.Printf("Closed db connection, waiting for workers to finish\n")
	cp.chDone <- true
	cp.ps.stopPubSub()
	cp.wg.Wait()
	log.Printf("Finished\n")
}
