module github.com/treapster/crossposter

go 1.19

require (
	github.com/SevereCloud/vksdk/v2 v2.15.0
	github.com/mattn/go-sqlite3 v1.14.16
	gopkg.in/telebot.v3 v3.1.2
)

require (
	github.com/klauspost/compress v1.15.13 // indirect
	github.com/vmihailenco/msgpack/v5 v5.3.5 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	golang.org/x/text v0.5.0 // indirect
)

// we replace because https://github.com/tucnak/telebot/pull/491 is not accepted at the time of writing
replace gopkg.in/telebot.v3 v3.1.2 => github.com/treapster/telebot v0.0.0-20221217070746-1e1c2fed75d2
