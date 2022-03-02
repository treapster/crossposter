module github.com/treapster/crossposter

go 1.17

require (
	github.com/SevereCloud/vksdk/v2 v2.13.1
	github.com/mattn/go-sqlite3 v1.14.12
	gopkg.in/telebot.v3 v3.0.0
)

require (
	github.com/klauspost/compress v1.14.2 // indirect
	github.com/vmihailenco/msgpack/v5 v5.3.5 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/text v0.3.7 // indirect
)

// we replace because https://github.com/tucnak/telebot/pull/491 is not accepted at the time of writing
replace gopkg.in/telebot.v3 v3.0.0 => github.com/treapster/telebot v3-album-caption
