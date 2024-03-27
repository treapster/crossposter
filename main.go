package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

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
	log.Printf("Bot Started, send ^C to stop\n")

	signalChan := make(chan os.Signal, 2)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	for s := range signalChan {
		log.Println("Received", s.String())
		cp.Stop()
		return
	}
}
