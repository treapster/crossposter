package main

import (
	"fmt"
	"math/rand"
	"regexp"
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

func lowerBound[T any](slice []T, val *T, comp func(*T, *T) bool) int {
	if len(slice) == 0 {
		return 1
	}
	count := len(slice)
	left := 0
	cur := 0
	for count > 0 {
		cur = left
		step := count / 2
		cur += step
		if comp(&slice[cur], val) {
			cur++
			left = cur
			count -= step + 1
		} else {
			count = step
		}
	}
	return left
}

var inlineLinkRegex *regexp.Regexp = regexp.MustCompile(`\[` +
	`(?:` +
	`(?:(?:https?://)?vk\.(?:com|me)/)([a-zA-Z0-9_\-\.\?=/@]+)` +
	`|` +
	`((?:club|id)[0-9]+)` +
	`)` +
	`\|` +
	`([^]\[]+)` +
	`\]`)

func findIndexToSplit(text string, target int) (int, int) {

	/*
		We have to cut text in pieces which, when rendered in a telegram message, should
		be shorter or the same length as target. We could just split on the whitespace
		character closest to target(and we did before), but it is wrong because:
		- there may be inline links in vk format which take the form [linkOrId|text] and
		  are matched by the above regex. To properly count characters which will be rendered
		  in telegram we have to skip linkOrId part, the '|' and brackets.
		- While trying to split text on whitespace character, we have to make sure
		  we don't split the inline link text in case it contains whitespace, or it will break
		This function counts rendered characters and returns a pair of string index
		at which to split the text and length of the resulting piece in rendered characters.
		We do this work before we replace VK link format with <a href...> because vk format
		is easier to match in regex, and after we have all the split points we will
		individually replace those links with <a> tags and send to telegram.
	*/

	matches := inlineLinkRegex.FindAllStringSubmatchIndex(text, -1)
	// index of current inline link
	curMatch := 0
	// number of *rendered* characters - not counting inline parts of links
	charCount := 0
	// index at which to split
	splitIndex := 0
	// split index in rendered characters
	splitCharacterIndex := 0

	for i, c := range text {
		if curMatch < len(matches) {
			if matches[curMatch][0] <= i && i < matches[curMatch][1] {
				// if we're inside link, don't count any characters which will not be rendered.
				// indices 6 and 7 contain bounds of the rendered link text
				if matches[curMatch][6] <= i && i < matches[curMatch][7] {
					charCount++
				}
				continue
			}
			if i >= matches[curMatch][1] { // end of match, move to the next
				curMatch++
			}
		}

		if charCount >= target {
			if isSeparator(c) && charCount == target {
				return i, charCount
			}
			if splitIndex > 0 {
				return splitIndex, splitCharacterIndex
			} else {
				// if there were no separators, or the whole text is a big link,
				// give up and just split on the given index
				return len(string([]rune(text)[:target])), target
			}
		}
		// we're not inside inline link, it's safe to split on a whitespace
		if isSeparator(c) {
			splitIndex = i
			splitCharacterIndex = charCount
		}
		charCount++
	}
	// We counted all characters and are still within the limit, so return the whole text
	return len(text), charCount
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
