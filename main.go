package main

import (
	"fmt"
	"log"
	"os"

	toml "github.com/BurntSushi/toml"
)

func main() {
	var config CrossposterConfig
	data, err := os.ReadFile("./config.toml")
	if err != nil {
		log.Printf(err.Error() + "\n")
		return
	}
	err = toml.Unmarshal(data, &config)
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
