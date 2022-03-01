package main

// this part uses vk api execute method to get batched updates for vk
// and dispatch them to subscribers via channels

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	vkApi "github.com/SevereCloud/vksdk/v2/api"
	vkObject "github.com/SevereCloud/vksdk/v2/object"
	tele "gopkg.in/telebot.v3"
)

type update struct {
	code int
	id   int64
	r    pubSubData
}

const (
	batchSize       = 25
	updatePostCount = 15
	updatePeriod    = time.Minute * 2
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
func makeJs(batch []vkReqData) string {
	js :=
		`
var batch = [%s];
var postCount = %d;
var res = [];
var i = 0;
while (i < batch.length) {
	var filtered = [];
	var posts = API.wall.get({"owner_id": batch[i].id, "count": postCount}).items;
	var j = 0;
	var lastPost = 0;
	while (j < posts.length) {
		if (posts[j].date > batch[i].lastPost) {
			filtered.push(posts[j]);
			if (posts[j].date > lastPost) {
				lastPost = posts[j].date;
			}
		}
		j = j + 1;
	}
	if (filtered.length > 0) {
		res.push({"id": batch[i].id, "lastPost":lastPost, "posts": filtered});
	}
	i = i + 1;
}
return res;
`
	arrContent := makeObjects(batch)
	return fmt.Sprintf(js, arrContent, updatePostCount)
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

func (cp *Crossposter) getAttachments(post *vkObject.WallWallpost) map[string][]tele.Inputtable {

	// because telegram album contains either photo/video or audio or documents, separate them
	res := make(map[string][]tele.Inputtable)
	audioIds := []string{}
	for _, att := range post.Attachments {
		switch att.Type {
		case "photo":
			url := getPhotoUrl(att.Photo)
			res["photo/video"] = append(res["photo/video"],
				&tele.Photo{File: tele.FromURL(url)})
		case "audio":
			audioIds = append(audioIds, strconv.Itoa(att.Audio.OwnerID)+"_"+strconv.Itoa(att.Audio.ID))
		case "doc":
			res["doc"] = append(res["doc"],
				&tele.Document{File: tele.FromURL(att.Doc.URL)})
		}
	}
	if len(audioIds) > 0 {
		res["audio"] = cp.getAudio(audioIds)
	}
	return res
}
func getPhotoUrl(photo vkObject.PhotosPhoto) string {
	index := len(photo.Sizes) - 1

	// sizes with this letters are cropped so we skip them
	for photo.Sizes[index].Type != "w" &&
		photo.Sizes[index].Type != "z" &&
		photo.Sizes[index].Type != "y" &&
		photo.Sizes[index].Type != "x" &&
		photo.Sizes[index].Type != "s" &&
		photo.Sizes[index].Type != "m" &&
		index > 0 {
		index--
	}

	// we need random because of https://stackoverflow.com/questions/49645510/telegram-bot-send-photo-by-url-returns-bad-request-wrong-file-identifier-http/62672868#62672868
	return photo.Sizes[index].URL + "&random=" + strconv.Itoa(int(rand.Int31()))
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func (cp *Crossposter) sendText(text []rune, chat int64) {

	n := len(text)
	for n > 0 {
		toSend := min(n, 4096)
		_, err := cp.tgBot.Send(tele.ChatID(chat), string(text[0:toSend]))
		if err != nil {
			log.Printf("Failed to send msg:\n%s\n", err.Error())
		}
		text = text[toSend:]
		n = len(text)
		time.Sleep(time.Second * 3)
	}
}
func (cp *Crossposter) sendWithAttachments(text []rune, id int64, att map[string][]tele.Inputtable) {
	if len(text) > 1024 {
		cp.sendText(text, id)
		text = text[:0]
	}

	for str, _ := range att {
		_, err := cp.tgBot.SendAlbum(tele.ChatID(id), att[str], string(text))
		if err != nil {
			log.Printf("Failed to send msg:\n%s\n", err.Error())
		}
		time.Sleep(time.Second * 3 * time.Duration(len(att[str])))
	}

}

func (cp *Crossposter) forwardSinglePost(post *vkObject.WallWallpost, chatID int64) {

	att := cp.getAttachments(post)
	if len(att) == 0 && len(post.Text) == 0 {
		return
	}
	if len(att) == 0 {
		cp.sendText([]rune(post.Text), chatID)
		return
	}
	cp.sendWithAttachments([]rune(post.Text), chatID, att)
}

func (cp *Crossposter) forwardPost(post *vkObject.WallWallpost, chatID int64) {

	for index := len(post.CopyHistory) - 1; index >= 0; index-- {
		cp.forwardSinglePost(&post.CopyHistory[index], chatID)
	}
	cp.forwardSinglePost(post, chatID)

}
func (cp *Crossposter) listenAndForward(upd <-chan []vkObject.WallWallpost, chatID int64) {

	for posts := range upd {
		for index := len(posts) - 1; index >= 0; index-- {
			cp.forwardPost(&posts[index], chatID)
		}
	}
}

func (cp *Crossposter) processBatch(batch []vkReqData) {
	var res []vkReqResult
	err := cp.vk.Execute(makeJs(batch), &res)
	if err != nil {
		log.Printf("Failed to execute:\n%s\n", err.Error())
	} else {
		for i, _ := range res {
			pub := cp.ps.pubToSub[res[i].Id]
			pub.lastPost = res[i].LastPost
			cp.ps.pubToSub[res[i].Id] = pub
			cp.updateTimeStamp(res[i].Id, res[i].LastPost)
			cp.ps.publish(res[i].Id, res[i].Posts)
		}
	}
}

func (cp *Crossposter) startCrossposting() {
	batch := make([]vkReqData, 0, batchSize)
	for {
		cp.ps.mu.Lock()

		for id, pub := range cp.ps.pubToSub {
			batch = append(batch, vkReqData{
				id,
				pub.lastPost,
			})
			if len(batch)%batchSize == 0 {
				cp.processBatch(batch)
				batch = batch[:0]
			}
		}
		if len(batch) > 0 {
			cp.processBatch(batch)
			batch = batch[:0]
		}
		cp.ps.mu.Unlock()

		select {
		case <-cp.chDone:
			return
		case <-time.After(updatePeriod):
			continue
		}
	}
}
