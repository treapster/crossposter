package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

func main() {
	var config CrossposterConfig
	data, err := os.ReadFile("./config.json")
	if err != nil {
		log.Printf(err.Error() + "\n")
		return
	}
	err = json.Unmarshal(data, &config)
	if err != nil {
		log.Printf(err.Error() + "\n")
		return
	}
	cp, err := NewCrossposter(config)
	if err != nil {
		log.Printf(err.Error() + "\n")
		return
	}

	go cp.Start()
	log.Printf("Bot Started, enter q to stop\n")
	var input string
	for {
		fmt.Scanf("%s", &input)
		if input == "exit" || input == "q" {
			cp.Stop()
			return
		}
	}
}
