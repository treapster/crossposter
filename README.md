# crossposter
This bot can crosspost vk posts to telegram groups, chats or direct messages. Send `/add vk.com/group @channel` and any new posts from the group will duplicate to channel with pictures, documents, audios and reposts. Videos are coming soon.
If you use `me` instead of channel username it will send updates to you. `/show` `del` allow managing this subscriptions.
# Dependencies
[Telebot](https://github.com/tucnak/telebot/tree/v3)  
[vksdk](https://github.com/SevereCloud/vksdk)  
[go-sqlite3](https://github.com/mattn/go-sqlite3)  
# Usage
Clone repo, go build. Set your vk token to env CROSSPOSTER_VK_TOKEN and telegram token to CROSSPOSTER_TG_TOKEN. To crosspost audio get a kate mobile token with [this tool](https://github.com/vodka2/vk-audio-token/tree/master/src/Vodka2/VKAudioToken) and set it to CROSSPOSTER_VKAUDIO_TOKEN(well, if your primary token has access to audio you can probably set it to CROSSPOSTER_VKAUDIO_TOKEN). Then launch bot and try it out in telegram.
