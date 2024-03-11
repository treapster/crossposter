package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	vkObject "github.com/SevereCloud/vksdk/v2/object"
)

func makeJs(batch []vkReqData, count int) string {
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
		if (posts[j].date > batch[i].lastPost && !posts[j].marked_as_ads) {
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
	return fmt.Sprintf(js, arrContent, count)
}
func findVideoURL(vid *vkObject.VideoVideo) string {
	if vid.Files.Mp4_720 != "" {
		return vid.Files.Mp4_720
	}
	if vid.Files.Mp4_480 != "" {
		return vid.Files.Mp4_480
	}
	if vid.Files.Mp4_360 != "" {
		return vid.Files.Mp4_360
	}

	return vid.Files.Mp4_240
}

func convertYoutubeUrl(url string) string {
	if strings.HasPrefix(url, "https://www.youtube.com/") {
		url = strings.Replace(url, "embed/", "watch?v=", 1)
		url = strings.Replace(url, "?__ref=vk.kate_mobile", "", 1)
		url = strings.Replace(url, "&__ref=vk.kate_mobile", "", 1)
		url = url[len("https://www."):]
	}
	return url
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
func isSeparator(c rune) bool {
	return c == ' ' || c == '\n' || c == '\t'
}
func findIndexToCut(text []rune, target int) int {
	if len(text) <= target {
		return len(text)
	}
	maxCharsToSkip := 100
	res := target
	for !isSeparator(text[res]) &&
		res > 0 &&
		target-res < maxCharsToSkip {
		res--
	}
	if !isSeparator(text[res]) {
		return target
	}
	return res
}

// Return a random time in the range (n - 1), (n + 1) days.
// used to determinne vk-id-to-name cache entry lifetime.
// Randomness is added to avoid bulk invalidations
// at the same time which could lead to exceeding vk api limits
func approxNDaysFromNow(n int) int64 {
	var secsInDay int64 = 24 * 60 * 60
	nDaysFromNow := time.Now().Unix() + int64(n)*secsInDay
	varSecs := (rand.Int63() % (2 * secsInDay)) - secsInDay
	return nDaysFromNow + varSecs
}
