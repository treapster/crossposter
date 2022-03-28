package main

// this part uses vk api execute method to get batched updates for vk
// and dispatch them to subscribers via channels

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	vkApi "github.com/SevereCloud/vksdk/v2/api"
	vkObject "github.com/SevereCloud/vksdk/v2/object"
	tele "gopkg.in/telebot.v3"
)

const (
	batchSize       = 20
	updatePostCount = 20
	updatePeriod    = time.Minute * 2
	maxVidDuration  = 102 // because 720p is below 50 MB(telegram limit) for up to 102 seconds
	// bigger videos are posted via link
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

type preparedAttachments struct {
	media map[string][]tele.Inputtable
	links []string
}

func (att *preparedAttachments) Empty() bool {
	return len(att.media) == 0 && len(att.links) == 0
}

type preparedPost struct {
	att         preparedAttachments
	text        string
	copyHistory []preparedPost
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

	vkRes := []vkAudio{}

	err := cp.vkAudio.RequestUnmarshal("audio.getById", &vkRes, vkApi.Params{
		"audios": strings.Join(audioIds, ","),
	})
	if err != nil {
		log.Printf("Failed to get audio:\n%s\n", err.Error())
		return nil
	}
	res := []tele.Inputtable{}
	for _, a := range vkRes {
		r, err := http.Get(a.Url)
		if err != nil {
			log.Printf("Failed to get audio from url\n%s\n", err.Error())
			continue
		}
		res = append(res, &tele.Audio{
			File:      tele.FromReader(r.Body),
			Title:     a.Title,
			Performer: a.Performer,
		})
	}
	return res
}
func (cp *Crossposter) getVideo(videoIds []string) ([]tele.Inputtable, []string) {
	vkRes, err := cp.vkAudio.VideoGet(map[string]interface{}{
		"videos": strings.Join(videoIds, ","),
	})
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
				continue
			}
		}
		resLinks = append(resLinks, fmt.Sprintf("vk.com/video%d_%d", v.OwnerID, v.ID))
	}
	return res, resLinks
}
func (cp *Crossposter) getVideos(post *vkObject.WallWallpost) []string {
	res := []string{}
	videoIds := []string{}
	for _, att := range post.Attachments {
		switch att.Type {
		case "video":
			videoIds = append(videoIds, strconv.Itoa(att.Video.OwnerID)+"_"+strconv.Itoa(att.Video.ID))
		}
	}
	if len(videoIds) > 0 {
		//res["photo/video"] = append(res["photo/video"],
	}
	return res
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
	if len(audioIds) > 0 {
		res.media["audio"] = cp.getAudio(audioIds)
	}
	links := []string{}
	vids := []tele.Inputtable{}
	if len(videoIds) > 0 {
		vids, links = cp.getVideo(videoIds)
		if len(vids) > 0 {
			res.media["photo/video"] = append(res.media["photo/video"], vids...)
		}
		res.links = links
	}
	return res
}

func (cp *Crossposter) sendText(text []rune, chat int64) {

	n := len(text)
	for n > 0 {
		msgLen := findIndexToCut(text, 4096)
		_, err := cp.tgBot.Send(tele.ChatID(chat), string(text[0:msgLen]))
		if err != nil {
			log.Printf("Failed to send msg:\n%s\n", err.Error())
		}
		text = text[msgLen:]
		n = len(text)
		time.Sleep(time.Second * 3)
	}
}
func (cp *Crossposter) sendWithAttachments(text []rune, id int64, att preparedAttachments) {
	text = append(text, '\n')
	text = append(text, []rune(strings.Join(att.links, "\n"))...)
	if len(text) > 1024 || len(att.media) == 0 {
		cp.sendText(text, id)
		text = text[:0]
	}

	for mediaType := range att.media {
		_, err := cp.tgBot.SendAlbum(tele.ChatID(id), att.media[mediaType], string(text))
		if err != nil {
			log.Printf("Failed to send msg:\n%s\n", err.Error())
			return
		}
		text = text[:0]
		time.Sleep(time.Second * 3 * time.Duration(len(att.media[mediaType])))
	}

}

func (cp *Crossposter) forwardSinglePost(post *preparedPost, chatID int64) {

	if post.att.Empty() {
		cp.sendText([]rune(post.text), chatID)
		return
	}
	cp.sendWithAttachments([]rune(post.text), chatID, post.att)
}

func (cp *Crossposter) forwardPost(post *preparedPost, chatID int64) {

	for i := range post.copyHistory {
		cp.forwardSinglePost(&post.copyHistory[i], chatID)
	}
	cp.forwardSinglePost(post, chatID)

}
func (cp *Crossposter) listenAndForward(upd <-chan []preparedPost, chatID int64) {
	for posts := range upd {
		for i := range posts {
			cp.forwardPost(&posts[i], chatID)
		}
	}
}
func (cp *Crossposter) prepareCopyHistory(post vkObject.WallWallpost) []preparedPost {
	res := make([]preparedPost, 0, len(post.CopyHistory))
	// we reverse copy history so in preparedPost slice it is ordered by time

	for i := len(post.CopyHistory) - 1; i >= 0; i-- {
		if len(post.CopyHistory[i].Attachments) == 0 && post.CopyHistory[i].Text == "" {
			continue
		}
		res = append(res, preparedPost{
			att:         cp.getAttachments(&post.CopyHistory[i]),
			text:        post.CopyHistory[i].Text,
			copyHistory: nil,
		})
	}
	return res
}

func (cp *Crossposter) preparePosts(posts []vkObject.WallWallpost) []preparedPost {
	res := make([]preparedPost, 0, len(posts))
	for i := len(posts) - 1; i >= 0; i-- {
		res = append(res, preparedPost{
			att:  cp.getAttachments(&posts[i]),
			text: posts[i].Text,
			// we don't call ourselves recursively because we don't want to repeat the same copy history
			// for every repost, so we call prepareCopyHistory once per actual post
			copyHistory: cp.prepareCopyHistory(posts[i]),
		})
	}
	return res
}
func (cp *Crossposter) processBatch(batch []vkReqData) {
	var res []vkReqResult
	err := cp.vk.Execute(makeJs(batch), &res)
	if err != nil {
		log.Printf("Failed to execute:\n%s\n", err.Error())
	} else {
		for i := range res {
			pub := cp.ps.pubToSub[res[i].Id]
			pub.lastPost = res[i].LastPost
			cp.ps.pubToSub[res[i].Id] = pub
			cp.updateTimeStamp(res[i].Id, res[i].LastPost)
			cp.ps.publish(res[i].Id, cp.preparePosts(res[i].Posts))
		}
	}
}

func (cp *Crossposter) startCrossposting() {
	batch := make([]vkReqData, 0, batchSize)
	for {

		cp.ps.mu.RLock()
		for id, pub := range cp.ps.pubToSub {
			batch = append(batch, vkReqData{
				id,
				pub.lastPost,
			})
			if len(batch)%batchSize == 0 {
				cp.ps.mu.RUnlock()
				cp.processBatch(batch)
				cp.ps.mu.RLock()
				batch = batch[:0]
			}
		}
		cp.ps.mu.RUnlock()
		if len(batch) > 0 {
			cp.processBatch(batch)
			batch = batch[:0]
		}
		select {
		case <-cp.chDone:
			return
		case <-time.After(updatePeriod):
			continue
		}
	}
}
