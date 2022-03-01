package main

import (
	"fmt"
	"log"
	"os"
)

func main() {

	cp, err := NewCrossposter(CrossposterConfig{
		vkToken:      os.Getenv("CROSSPOSTER_VK_TOKEN"),
		vkAudioToken: os.Getenv("CROSSPOSTER_VKAUDIO_TOKEN"),
		vkApiVersion: "5.131",
		tgToken:      os.Getenv("CROSSPOSTER_TG_TOKEN"),
		dbName:       "./crossposter.db",
	})
	if err != nil {
		log.Printf(err.Error() + "\n")
		return
	}
	go cp.Start()
	log.Printf("Bot Started\n")
	var input string
	for {
		fmt.Scanf("%s", &input)
		if input == "exit" || input == "q" {
			cp.Stop()
			return
		}
	}
}
