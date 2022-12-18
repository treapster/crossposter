# Crossposter
This bot can crosspost vk posts to telegram groups, chats or direct messages. Send `/add vk.com/group @channel` and any new posts from the group will duplicate to channel with pictures, documents, audios, videos and reposts. 
If you use `me` instead of channel username it will send updates to you. Use `/ls` and `/del` to manage your subscriptions.  
Try it yourself on [telegram](https://t.me/vkcrosspostbot).
# Dependencies
[Telebot](https://github.com/tucnak/telebot/tree/v3)  
[vksdk](https://github.com/SevereCloud/vksdk)  
[go-sqlite3](https://github.com/mattn/go-sqlite3)  
# Usage
Clone repo, go build. Rename `dummy_config.json` to `config.json`. To crosspost audio get a kate mobile token with [this tool](https://github.com/vodka2/vk-audio-token) and set it to `VkAudioToken`. If your primary token has access to audio you can use it for audio. Set service token to `VkToken` and telegram token to `TgToken`. Then launch bot and try it out in telegram.
