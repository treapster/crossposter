package main

// this part uses vk api execute method to get batched updates for vk
// and dispatch them to subscribers via channels

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	vkApi "github.com/SevereCloud/vksdk/v2/api"
	vkObject "github.com/SevereCloud/vksdk/v2/object"
	tele "gopkg.in/telebot.v3"
)

const (
	maxVidDuration = 102 // because 720p is below 50 MB(telegram limit) for up to 102 seconds
	// bigger videos are posted via link
)
const (
	flagAddLinkToPost uint64 = 1 << iota
)

type vkReqData struct {
	id       int64
	lastPost int64
}
type vkReqResult struct {
	Id       int64                   `json:"id"`
	LastPost int64                   `json:"lastPost"`
	Posts    []vkObject.WallWallpost `json:"posts"`
}
type vkAudio struct {
	Url       string `json:"url"`
	Performer string `json:"artist"`
	Title     string `json:"title"`
}

type postLink struct {
	formattedPostLink string
	rawPostLink       string
	postLinkTextLen   int
}
type preparedAttachments struct {
	media map[string][]tele.Inputtable
	links []string
}

func (att *preparedAttachments) Empty() bool {
	return len(att.media) == 0 && len(att.links) == 0
}

type preparedPost struct {
	att         preparedAttachments
	ownerID     int
	ID          int
	text        string
	copyHistory []preparedPost
	Link        postLink
}

type update struct {
	posts []preparedPost
	flags uint64
}

type updateInfo struct {
	time   int64
	nPosts int
}

type stats struct {
	nPostsGlobal int
	updates      []updateInfo
	startTime    int64
	m            sync.Mutex
}

func (s *stats) addUpdate(u updateInfo) {
	s.m.Lock()
	defer s.m.Unlock()
	s.nPostsGlobal += u.nPosts
	s.updates = append(s.updates, u)
}

func (s *stats) get() (int, int, int64) {
	comp := func(l *updateInfo, r *updateInfo) bool {
		return l.time < r.time
	}
	now := time.Now().Unix()
	hourAgo := updateInfo{
		time: now - 3600,
	}
	s.m.Lock()
	defer s.m.Unlock()
	lb := lowerBound(s.updates, &hourAgo, comp)
	lastHour := 0
	for i := lb; i < len(s.updates); i++ {
		lastHour += s.updates[i].nPosts
	}
	uptime := now - s.startTime
	return s.nPostsGlobal, lastHour, uptime
}

func makeObjects(batch []vkReqData) string {
	if len(batch) == 0 {
		return ""
	}
	pat := `{"id":%d, "lastPost": %d}`
	res := fmt.Sprintf(pat, batch[0].id, batch[0].lastPost)
	for _, cur := range batch[1:] {
		res += `,` + fmt.Sprintf(pat, cur.id, cur.lastPost)
	}
	return res
}

func (cp *Crossposter) getAudio(audioIds []string) []tele.Inputtable {

	res := []tele.Inputtable{}
	if len(audioIds) == 0 {
		return res
	}
	vkRes := []vkAudio{}
	err := cp.vkAudio.RequestUnmarshal("audio.getById", &vkRes, vkApi.Params{
		"audios": strings.Join(audioIds, ","),
	})
	if err != nil {
		log.Printf("Failed to get audio:\n%s\n", err.Error())
		return nil
	}

	for i, a := range vkRes {
		if a.Url != "" {
			r, err := http.Get(a.Url)
			if err != nil {
				log.Printf("Failed to get audio from url %s\n%s\n", a.Url, err.Error())
				continue
			}
			res = append(res, &tele.Audio{
				File:      tele.FromReader(r.Body),
				Title:     a.Title,
				Performer: a.Performer,
			})
		} else {
			log.Printf("Failed to get audio %s\n", audioIds[i])
		}

	}
	return res
}
func (cp *Crossposter) getVideo(videoIds []string) ([]tele.Inputtable, []string) {
	vkRes, err := cp.vkAudio.VideoGet(map[string]interface{}{
		"videos": strings.Join(videoIds, ","),
	})
	time.Sleep(time.Millisecond * 200)
	if err != nil {
		log.Printf("Failed to get video:\n%s\n", err.Error())
		return nil, nil
	}
	res := []tele.Inputtable{}
	resLinks := []string{}
	for i := range vkRes.Items {
		v := &vkRes.Items[i]
		if v.Platform == "YouTube" {
			resLinks = append(resLinks, convertYoutubeUrl(v.Player))
			continue
		}
		if v.Duration < maxVidDuration && (v.Platform == "vk" || v.Platform == "") {

			if url := findVideoURL(v); url == "" {
				log.Printf("Couldn't find url for video %d_%d\n", v.OwnerID, v.ID)
			} else {
				req, _ := http.NewRequest("GET", url, nil)
				req.Header.Set("User-Agent", kateUserAgent)
				r, err := cp.vkAudio.Client.Get(url)
				if err != nil {
					log.Printf("Failed to get video from url\n%s\n", err.Error())
					continue
				}
				res = append(res, &tele.Video{
					File: tele.FromReader(r.Body),
				})
				time.Sleep(time.Millisecond * 200)
				continue
			}
		}
		resLinks = append(resLinks, fmt.Sprintf("vk.com/video%d_%d", v.OwnerID, v.ID))
	}
	return res, resLinks
}

func (cp *Crossposter) getAttachments(post *vkObject.WallWallpost) preparedAttachments {

	// because telegram album contains either photo/video or audio or documents, we separate them
	res := preparedAttachments{map[string][]tele.Inputtable{}, []string{}}
	audioIds := []string{}
	videoIds := []string{}
	for _, att := range post.Attachments {
		switch att.Type {
		case "photo":
			url := getPhotoUrl(att.Photo)
			res.media["photo/video"] = append(res.media["photo/video"],
				&tele.Photo{File: tele.FromURL(url)})
		case "audio":
			audioIds = append(audioIds, strconv.Itoa(att.Audio.OwnerID)+"_"+strconv.Itoa(att.Audio.ID))
		case "doc":
			res.media["doc"] = append(res.media["doc"],
				&tele.Document{File: tele.FromURL(att.Doc.URL)})
		case "video":
			vID := strconv.Itoa(att.Video.OwnerID) + "_" + strconv.Itoa(att.Video.ID)
			if att.Video.AccessKey != "" {
				vID += "_" + att.Video.AccessKey
			}
			videoIds = append(videoIds, vID)
		}
	}

	audio := cp.getAudio(audioIds)

	if len(audio) < len(audioIds) {
		log.Printf("Only got %d/%d audios for %s\n", len(audio),
			len(audioIds), fmt.Sprintf("https://vk.com/wall%d_%d", post.OwnerID, post.ID))
	}

	if len(audio) > 0 {
		res.media["audio"] = audio
	}

	if len(videoIds) > 0 {
		vids, links := cp.getVideo(videoIds)
		if len(vids) > 0 {
			res.media["photo/video"] = append(res.media["photo/video"], vids...)
		}
		res.links = links
	}
	return res
}

func (cp *Crossposter) sendText(text string, link postLink, chat int64, opts tele.SendOptions) *tele.Message {
	text = strings.Trim(text, " \t\n")
	if len(text) == 0 {
		return nil
	}
	if len(text) > 0 && link.postLinkTextLen > 0 {
		text = text + "\n\n"
	}

	var firstMsg *tele.Message
	appendedLink := false
	maxMsgSize := 4096
	for len(text) > 0 || (len(link.formattedPostLink) > 0 && !appendedLink) {
		splitIndex, msgLen := findIndexToSplit(text, maxMsgSize)

		msgText := inlineLinkRegex.ReplaceAllString(
			html.EscapeString(text[0:splitIndex]),
			"<a href='https://vk.com/$1$2'>$3</a>",
		)
		// if text fits in one message, check if it will fit with link too
		if splitIndex == len(text) && msgLen+link.postLinkTextLen <= maxMsgSize {
			msgText = msgText + link.formattedPostLink
			appendedLink = true
		}

		newMsg, err := cp.tgBot.Send(tele.ChatID(chat), msgText, &opts)
		if err != nil {
			log.Printf("Failed to send msg for post %s:\n%s\n", link.rawPostLink, err.Error())
			return nil
		}

		if firstMsg == nil {
			firstMsg = newMsg
		}
		opts.ReplyTo = newMsg
		text = strings.TrimLeft(text[splitIndex:], " \t\n")
		time.Sleep(time.Second * 3)
	}
	return firstMsg
}
func (cp *Crossposter) sendWithAttachments(text string, link postLink, id int64, att preparedAttachments, opts tele.SendOptions) *tele.Message {

	if len(att.links) != 0 {
		text = text + "\n" + strings.Join(att.links, "\n")
	}

	maxMsgSize := 1024
	_, msgSize := findIndexToSplit(text, 999999) // count rendered characters in a text
	if link.postLinkTextLen > 0 {
		if msgSize > 0 {
			msgSize += 2 // two newlines
			text = text + "\n\n"
		}
		msgSize += link.postLinkTextLen
	}
	var firstMsg *tele.Message = nil
	if msgSize > maxMsgSize || len(att.media) == 0 {
		firstMsg = cp.sendText(text, link, id, opts)
		text = text[:0]
	} else {
		text = inlineLinkRegex.ReplaceAllString(
			html.EscapeString(text),
			"<a href='https://vk.com/$1$2'>$3</a>") +
			link.formattedPostLink
	}
	for mediaType := range att.media {
		msg, err := cp.tgBot.SendAlbum(tele.ChatID(id), att.media[mediaType], text, &opts)
		if err != nil {
			log.Printf("Failed to send msg for post %s:\n%s\n", link.rawPostLink, err.Error())
			return nil
		}

		// we post attachments as a reply to initial message, while the initial message may be a reply
		// to another message passed in opts in case of repost chains
		if firstMsg == nil && len(msg) > 0 {
			firstMsg = &msg[0]
			opts.ReplyTo = firstMsg
		}
		text = text[:0]

		// simplest way to not exceed 20 messages per minute
		time.Sleep(time.Second * 3 * time.Duration(len(att.media[mediaType])))
	}
	return firstMsg
}

func (cp *Crossposter) forwardSinglePost(post *preparedPost, flags uint64, chatID int64, opts tele.SendOptions) *tele.Message {

	link := post.Link
	if flags&flagAddLinkToPost == 0 {
		link.formattedPostLink = ""
		link.postLinkTextLen = 0
	}

	if post.att.Empty() {
		return cp.sendText(post.text, link, chatID, opts)
	}
	return cp.sendWithAttachments(post.text, link, chatID, post.att, opts)
}

func (cp *Crossposter) forwardPost(post *preparedPost, chatID int64, flags uint64) {

	opts := tele.SendOptions{
		ParseMode: "HTML",
	}

	for i := range post.copyHistory {
		flags := flags
		// if we have reposts from external pages, add reference to source regardless of setting
		if post.copyHistory[i].ownerID != post.ownerID {
			flags |= flagAddLinkToPost
		}

		opts.ReplyTo = cp.forwardSinglePost(&post.copyHistory[i], flags, chatID, opts)
	}
	cp.forwardSinglePost(post, flags, chatID, opts)

}
func (cp *Crossposter) listenAndForward(upd <-chan update, chatID int64) {
	cp.wg.Add(1)
	for update := range upd {
		for i := range update.posts {
			cp.forwardPost(&update.posts[i], chatID, uint64(update.flags))
		}
	}
	cp.wg.Done()
}

func (cp *Crossposter) makeLinkToPost(post *vkObject.WallWallpost) postLink {

	ownerData, _ := cp.resolveVkId(int64(post.OwnerID))
	postLinkText := ownerData.Name
	rawPostLink := fmt.Sprintf("https://vk.com/wall%d_%d", post.OwnerID, post.ID)
	formattedPostLink := fmt.Sprintf("[<a href = '%s'>%s</a>]", rawPostLink, postLinkText)
	postLinkLen := len([]rune(postLinkText)) + 2

	return postLink{
		formattedPostLink,
		rawPostLink,
		postLinkLen,
	}
}

func (cp *Crossposter) preparePosts(posts []vkObject.WallWallpost, HandleReposts bool) []preparedPost {
	res := make([]preparedPost, 0, len(posts))
	for i := len(posts) - 1; i >= 0; i-- {
		// We only skip ads if they're not intentionally reposted.
		// For reposts HandleReposts is false, so the ads will be
		// handled as ordinary posts.
		if bool(posts[i].MarkedAsAds) && HandleReposts {
			continue
		}

		var copyHistory []preparedPost = nil
		if HandleReposts {
			copyHistory = cp.preparePosts(posts[i].CopyHistory, false)
		}
		res = append(res, preparedPost{
			att:         cp.getAttachments(&posts[i]),
			text:        posts[i].Text,
			copyHistory: copyHistory,
			ID:          posts[i].ID,
			ownerID:     posts[i].OwnerID,
			Link:        cp.makeLinkToPost(&posts[i]),
		})
	}
	return res
}

func (cp *Crossposter) processBatch(batch []vkReqData) {
	var res []vkReqResult
	err := cp.vk.Execute(makeJs(batch, cp.nPostsToFetch), &res)
	if err != nil {
		log.Printf("Failed to execute:\n%s\n", err.Error())
		switch e := err.(type) {
		case *vkApi.ExecuteErrors:
			for _, exErr := range *e {
				log.Printf("Method: %s Code: %d Message: %s\n", exErr.Method, exErr.Code, exErr.Msg)
			}
		default:
			log.Println("Unknown error type: ", reflect.TypeOf(err))
		}
	} else {
		nUpdates := 0
		time := time.Now().Unix()
		for i := range res {
			cp.updateTimeStamp(res[i].Id, res[i].LastPost)
			nUpdates += len(res[i].Posts)
			cp.ps.publish(res[i].Id, cp.preparePosts(res[i].Posts, true /*HandleReposts*/))
		}
		if nUpdates > 0 {
			cp.stats.addUpdate(updateInfo{
				time,
				nUpdates,
			})
		}
	}
}

func (cp *Crossposter) startCrossposting() {
	batch := make([]vkReqData, 0, cp.batchSize)
	for {
		cp.ps.mu.RLock()
		for id, pub := range cp.ps.pubToSub {
			batch = append(batch, vkReqData{
				id,
				pub.lastPost,
			})
			if len(batch)%cp.batchSize == 0 {
				cp.ps.mu.RUnlock()
				cp.processBatch(batch)
				time.Sleep(300 * time.Millisecond)
				cp.ps.mu.RLock()
				batch = batch[:0]
			}
		}
		cp.ps.mu.RUnlock()
		if len(batch) > 0 {
			cp.processBatch(batch)
			time.Sleep(300 * time.Millisecond)
			batch = batch[:0]
		}
		select {
		case <-cp.chDone:
			return
		case <-time.After(cp.updatePeriod):
			continue
		}
	}
}
