package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"time"

	vkApi "github.com/SevereCloud/vksdk/v2/api"
	vkObject "github.com/SevereCloud/vksdk/v2/object"
	_ "github.com/mattn/go-sqlite3"
	tele "gopkg.in/telebot.v3"
)

type botReplies struct {
	okAdded        string
	helpMsg        string
	invalidRequest string
	noSuchGroup    string
	noSuchChannel  string
	noSuchUser     string
	groupPrivate   string
	userPrivate    string
	queryFailed    string
	noSuchSub      string
	delSuccess     string
	noSubs         string
	notAdmin       string
}

var i18n = map[string]botReplies{
	"en": {
		invalidRequest: "Invalid request",
		helpMsg: "1. Add this bot to your channel with permission to post messages\n" +
			"2. Send <code>/add vk.com/group @channel</code> to begin crossposting from group to channel. " +
			"You can crosspost from personal wall too as long as it is public\n\n" +
			"If you want to crosspost to private channel without username, you can obtain it's id through @username_to_id_bot and " +
			"send <code>/add vk.com/group id</code> without @ sign. You can also use \"me\" instead of username or id to get messages in DM. " +
			"Successfull /add command creates subscription vk => tg identified by a number. To see all your subscriptions with their ids send /show. " +
			"To delete particular subscription send <code>/del id</code>.",
		okAdded:       "%s is now subscribed to %s",
		noSuchGroup:   "Group %s does not exist",
		noSuchUser:    "User %s does not exist",
		noSuchChannel: "Channel %s does not exist",
		groupPrivate:  "Goup %s is private or blocked",
		userPrivate:   "User page %s is private or blocked",
		queryFailed:   "Failed to execute operation because of unknown error",
		noSuchSub:     "No subscription with such id",
		delSuccess:    "subscription %d successfully deleted",
		noSubs:        "No subscriptions to show",
		notAdmin:      "At least one of us is not admin of the chat. We both shall be.",
	},
	"ru": {
		invalidRequest: "Инвалид сюнтах",
		helpMsg: "1. Добавь меня в свой канал и дай разрешение отправлять сообщения\n" +
			"2. Отправь <code>/add vk.com/group @channel</code>, чтобы начать дублировать посты из группы в канал. " +
			"Вместо группы также может быть личная страница, если она пубично доступна.\n\n" +
			"Чтобы кросспостить в закрытый канал или группу без юзернейма, можешь получить её id через @username_to_id_bot и " +
			"отправить <code>/add vk.com/group id</code> (без @). Можешь также использовать \"me\" вместо юзернейма, чтобы получать посты в ЛС.\n" +
			"После добавления через /add создаётся подписка на группу и ей присваивается id. Чтобы посмотреть свои подписки, напиши /show. " +
			"Чтобы удалить подписку и перестать кросспостить, отправь <code>/del id</code>.",
		okAdded:       "%s теперь подписан на %s",
		noSuchGroup:   "Группа %s не существует",
		noSuchUser:    "Пользователь %s не существует",
		noSuchChannel: "Канал %s не существует",
		groupPrivate:  "Группа %s закрыта или заблокирована",
		userPrivate:   "Страница пользователя %s заблокирована или скрыта",
		queryFailed:   "Не удалось выполнить запрос из-за неизвестной ошибки",
		noSuchSub:     "Нет подписки с таким id",
		delSuccess:    "Подписка %d успешно удалена",
		noSubs:        "Список каналов пуст",
		notAdmin:      "Как минимум одному из нас не хватает прав администратора этого чата. Они должны быть у нас обоих.",
	},
}

type CrossposterConfig struct {
	vkToken      string
	vkAudioToken string
	vkApiVersion string
	tgToken      string
	dbName       string
}

// we have one instance of vk api with service token and other
// with kate mobile token obtained by https://github.com/vodka2/vk-audio-token/tree/master/src/Vodka2/VKAudioToken.
// kate token allows to download audio but i don't use it for anything else
// to not get banned or anything.
type Crossposter struct {
	vk               *vkApi.VK
	vkAudio          *vkApi.VK
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
}

type pubSubData struct {
	userID   int64
	pubID    int64
	subID    int64
	lastPost int64
}

const (
	reqSubscribe   string = "/add"
	reqUnsubscribe string = "/del"
	reqShowSubs    string = "/show"
	reqHelp        string = "/help"
	reqStart       string = "/start"
)

// userError is in fact enum to classify wrong input and generate appropriate messages.
// Could be done with default error and string constants, but this seems more natural to me
type userError struct {
	code        int
	userOrGroup string
}

const (
	errNoSuchGroup int = iota + 1
	errGroupPrivate
	errNoSuchUser
	errUserPrivate
	errNoSuchChannel
	errInvalidRequest
	errNoSubs
)
const (
	regexAddSub = `^` + reqSubscribe +
		`\s+(?:(?:https://)?)(?:vk.com/(?P<vk>[a-zA-Z0-9_]+))\s+(?P<tg>(?:@[a-zA-Z][0-9a-zA-Z_]{4,})|(?:-?[0-9]+)|me)\s*$`

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
func (cp *Crossposter) resolveVkID(id int64) (string, error) {

	var errmsg string
	if id > 0 {
		usr, err := cp.vk.UsersGet(vkApi.Params{
			"user_ids": id,
			"fields":   "screen_name",
		})
		if err == nil && len(usr) > 0 {
			return usr[0].ScreenName, nil
		}
		errmsg = err.Error()
	} else {
		group, err := cp.vk.GroupsGetByID(vkApi.Params{
			"group_ids": -id,
		})
		if err == nil && len(group) > 0 {
			return group[0].ScreenName, nil
		}
		errmsg = err.Error()
	}
	return "", fmt.Errorf("Failed to resolve vk id %d: %s", id, errmsg)
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
		return 0, userError{errNoSuchGroup, name}
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
			return 0, userError{errGroupPrivate, name}
		}
		return 0, userError{errNoSuchGroup, name}

	case "user":
		id = int64(vkResp.ObjectID)
		res, err := cp.vk.UsersGet(vkApi.Params{
			"user_ids": id,
		})
		if err == nil && len(res) > 0 {
			if res[0].IsClosed == false && res[0].Deactivated == "" {
				return id, nil
			}
			return 0, userError{errUserPrivate, name}
		}
		return 0, userError{errNoSuchUser, name}
	default:
		return 0, userError{errNoSuchGroup, name}
	}
}
func getLang(c tele.Context) string {
	lang := c.Sender().LanguageCode[0:2]
	if lang == "ru" || lang == "uk" || lang == "kz" || lang == "be" {
		lang = "ru"
	} else {
		lang = "en"
	}
	return lang
}
func (cp *Crossposter) handleUserError(c tele.Context, err userError) {
	lang := getLang(c)
	switch err.code {
	case errInvalidRequest:
		c.Send(i18n[lang].invalidRequest)
	case errNoSuchGroup:
		c.Send(fmt.Sprintf(i18n[lang].noSuchGroup, "vk.com/"+err.userOrGroup))
	case errNoSuchUser:
		c.Send(fmt.Sprintf(i18n[lang].noSuchUser, "vk.com/"+err.userOrGroup))
	case errNoSuchChannel:
		c.Send(fmt.Sprintf(i18n[lang].noSuchChannel, err.userOrGroup))
	case errGroupPrivate:
		c.Send(fmt.Sprintf(i18n[lang].groupPrivate, "vk.com/"+err.userOrGroup))
	case errUserPrivate:
		c.Send(fmt.Sprintf(i18n[lang].userPrivate, "vk.com/"+err.userOrGroup))
	case errNoSubs:
		c.Send(i18n[lang].noSubs)
	}
}
func (cp *Crossposter) handleDel(c tele.Context) error {
	msg := c.Text()
	matches := cp.delMsgRegex.FindStringSubmatch(msg)
	if len(matches) != 2 {
		cp.handleUserError(c, userError{code: errInvalidRequest})
		return nil
	}
	lang := getLang(c)
	pubSubID, _ := strconv.ParseInt(matches[1], 10, 64)

	rows, err := cp.dbFindPubSubStmt.Query(pubSubID, c.Sender().ID)
	if err != nil {
		c.Send(i18n[lang].queryFailed)
		return err
	}
	if !rows.Next() {
		c.Send(i18n[lang].noSuchSub)
		return nil
	}
	var pubID, subID int64
	err = rows.Scan(&pubID, &subID)
	if err != nil {
		c.Send(i18n[lang].queryFailed)
		return nil
	}

	rows.Close()
	_, err = cp.db.Exec("delete from pubSub where userID=? and pubSubID=?;"+
		"delete from publishers where id not in (select pubID from pubSub);"+
		"delete from subscribers where id not in (select subID from pubSub);", c.Sender().ID, pubSubID, pubID)
	if err != nil {
		c.Send(i18n[lang].queryFailed)
		return err
	}
	cp.ps.unsubscribe(subID, pubID)
	c.Send(fmt.Sprintf(i18n[lang].delSuccess, pubSubID))
	return nil
}
func (cp *Crossposter) handleHelp(c tele.Context) error {
	lang := getLang(c)
	return c.Send(i18n[lang].helpMsg)
}
func (cp *Crossposter) handleAdd(c tele.Context) error {
	msg := c.Text()
	matches := cp.addMsgRegex.FindStringSubmatch(msg)
	if len(matches) != 3 {
		cp.handleUserError(c, userError{code: errInvalidRequest})
		return nil
	}
	vkName, tgName := matches[1], matches[2]
	vkId, err := cp.resolveVkName(vkName)
	if err != nil {
		cp.handleUserError(c, err.(userError))
		return nil
	}
	var tgId int64
	user := c.Sender().ID
	lang := getLang(c)
	if tgName == "me" {
		tgId = user
		tgName = c.Sender().FirstName + c.Sender().LastName
	} else {
		tgId, err = cp.ResolveTgName(tgName)
		if err != nil {
			cp.handleUserError(c, userError{errNoSuchChannel, tgName})
			return nil
		}
		if !cp.isUserAdmin(user, tgId) {
			c.Send(i18n[lang].notAdmin)
			return nil // this return is a single thing that prevents users from messing each other's subscriptions
		}
	}

	res := pubSubData{
		userID:   user,
		pubID:    vkId,
		subID:    tgId,
		lastPost: 0,
	}

	// TODO: convert this and corresponding delete query to transaction
	_, err = cp.db.Exec(
		"insert or ignore into publishers (id, lastPost) values(?,(select strftime('%s')));"+
			"insert or ignore into subscribers (id, flags) values(?, 0);"+
			"insert into pubSub (userID, pubID, subID) values (?, ?, ?);", res.pubID, res.subID, user, res.pubID, res.subID)
	if err != nil {
		c.Send(i18n[lang].queryFailed)
		log.Printf("Failed to insert data to db:\n%s", err.Error())
		return nil
	}
	if err != nil {
		c.Send(i18n[lang].queryFailed)
		log.Printf("Failed to insert data to db:\n%s", err.Error())
		return nil
	}
	cp.ps.subscribe(tgId, vkId, func(ch <-chan []vkObject.WallWallpost) {
		cp.listenAndForward(ch, tgId)
	})
	c.Send(fmt.Sprintf(i18n[lang].okAdded, tgName, "vk.com/"+vkName))

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
		vkName, err := cp.resolveVkID(pub)
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
		cp.handleUserError(c, userError{code: errNoSubs})
		return nil
	}
	return c.Send(msg)
}

func createTableIfNotExists(db *sql.DB) (sql.Result, error) {

	return db.Exec(
		"create table if not exists publishers" +
			"(id integer primary key, lastPost integer);" +
			"create table if not exists subscribers" +

			// flags is currently not used but supposed to hold things like whether user
			// wants to see links to posts, names of groups and etc
			"(id integer primary key, flags integer);" +
			"create table if not exists pubSub" +
			"(pubSubID integer primary key, userID integer, pubID integer, subID integer," +
			"foreign key (pubID) references publishers(id)," +
			"foreign key (subID) references subscribers(id));" +
			"create index if not exists pub on pubSub (pubID);" +
			"create index if not exists sub on pubSub (subID);" +
			"create index if not exists user on pubSub (userID);")
}
func (cp *Crossposter) updateTimeStamp(timeStamp int64, id int64) {
	_, err := cp.dbUpdateStmt.Exec(timeStamp, id)
	if err != nil {
		log.Printf("Failed to update db for publisher %d and lastPost %d:\n%s\n", id, timeStamp, err.Error())
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
	_, err = createTableIfNotExists(cp.db)
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
		cp.ps.pubToSub[id] = vkSource{lastPost: lastPost, subs: make(map[int64]struct{})}
	}
	rows, err = cp.dbReadSubsStmt.Query()
	for rows.Next() {
		err = rows.Scan(&id)
		if err != nil {
			return err
		}
		cp.ps.subToPub[id] = subscriber{feed: make(chan []vkObject.WallWallpost, 4), subsCount: 0}
	}

	rows, err = cp.dbSelectAllStmt.Query()
	if err != nil {
		return err
	}

	for rows.Next() {
		var r pubSubData
		var id int64
		err = rows.Scan(&id, &r.userID, &r.pubID, &r.subID)
		if err != nil {
			return err
		}
		cp.ps.subscribeNoMutex(r.subID, r.pubID, func(ch <-chan []vkObject.WallWallpost) {
			cp.listenAndForward(ch, id)
		})
	}
	return nil
}
func (cp *Crossposter) setHandlers() {
	cp.tgBot.Handle(reqSubscribe, func(c tele.Context) error {
		return cp.handleAdd(c)
	})
	cp.tgBot.Handle(reqHelp, func(c tele.Context) error {
		return cp.handleHelp(c)
	})
	cp.tgBot.Handle(reqShowSubs, func(c tele.Context) error {
		return cp.handleShow(c)
	})
	cp.tgBot.Handle(reqUnsubscribe, func(c tele.Context) error {
		return cp.handleDel(c)
	})
	cp.tgBot.Handle(reqStart, func(c tele.Context) error {
		return cp.handleHelp(c)
	})

}
func NewCrossposter(cfg CrossposterConfig) (*Crossposter, error) {
	cp := &Crossposter{}
	cp.vk = vkApi.NewVK(cfg.vkToken)
	cp.vkAudio = vkApi.NewVK(cfg.vkAudioToken)
	cp.vkAudio.UserAgent = "KateMobileAndroid/56 lite-460 (Android 4.4.2; SDK 19; x86; unknown Android SDK built for x86; en)"

	var err error
	cp.tgBot, err = tele.NewBot(tele.Settings{
		Token:     cfg.tgToken,
		Poller:    &tele.LongPoller{Timeout: 10 * time.Second},
		ParseMode: "HTML",
	})

	if err != nil {
		return nil, fmt.Errorf("NewCrossposter: failed to build telegram bot:\n%w", err)
	}
	cp.setHandlers()
	cp.addMsgRegex = regexp.MustCompile(regexAddSub)
	cp.delMsgRegex = regexp.MustCompile(regexDelSub)

	cp.dbName = cfg.dbName
	err = cp.initDB()
	if err != nil {
		return nil, fmt.Errorf("NewCrossposter: failed to init DB:\n%w", err)
	}

	cp.chDone = make(chan bool)
	cp.ps.pubToSub = make(map[int64]vkSource)
	cp.ps.subToPub = make(map[int64]subscriber)
	err = cp.readDB()
	if err != nil {
		return nil, fmt.Errorf("Failed to read db:\n%w", err)
	}
	return cp, nil
}
func (cp *Crossposter) Start() {

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
	log.Printf("Finished\n")
}
