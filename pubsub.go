package main

import (
	"log"
	"sync"
	"time"

	vkObject "github.com/SevereCloud/vksdk/v2/object"
)

type subscriber struct {
	feed      chan []vkObject.WallWallpost
	subsCount int32
}
type vkSource struct {
	lastPost int64
	subs     map[int64]struct{}
}

type pubsub struct {
	pubToSub map[int64]vkSource   // vk group id to a list of subscriber ids
	subToPub map[int64]subscriber // tg channel id to it's vk feed and subCount
	mu       sync.RWMutex
}

// crossposter needs to iterate over publishers to poll for updates,
// and handlers want to change these maps when user requests, so we lock when we begin
// iterating(see crosspost.go) and have locks in subscribe and unsubscribe, but don't have in publish
// because it's already locked when we're publishing (since we publish whom inside loop).
// It's messy but i can't get around it because we can't hide data when crossposter has
// to iterate over it every couple of minutes.
func (ps *pubsub) subscribe(sub int64, pub int64, consumer func(<-chan []vkObject.WallWallpost)) {
	ps.mu.Lock()
	if _, exists := ps.subToPub[sub]; !exists {
		ps.subToPub[sub] = subscriber{feed: make(chan []vkObject.WallWallpost, 4), subsCount: 0}
		go consumer(ps.subToPub[sub].feed)
	}
	s := ps.subToPub[sub]
	s.subsCount++
	ps.subToPub[sub] = s
	if _, exists := ps.pubToSub[pub]; !exists {
		ps.pubToSub[pub] = vkSource{lastPost: time.Now().Unix(), subs: make(map[int64]struct{})}
	}
	ps.pubToSub[pub].subs[sub] = struct{}{}
	ps.mu.Unlock()
}
func (ps *pubsub) subscribeNoMutex(sub int64, pub int64, consumer func(<-chan []vkObject.WallWallpost)) {
	if _, exists := ps.subToPub[sub]; !exists {
		ps.subToPub[sub] = subscriber{feed: make(chan []vkObject.WallWallpost, 4)}
		go consumer(ps.subToPub[sub].feed)
	}
	s := ps.subToPub[sub]
	s.subsCount++
	ps.subToPub[sub] = s
	if _, exists := ps.pubToSub[pub]; !exists {
		ps.pubToSub[pub] = vkSource{lastPost: 0, subs: make(map[int64]struct{})}
	}
	ps.pubToSub[pub].subs[sub] = struct{}{}
}
func (ps *pubsub) unsubscribe(sub int64, pub int64) {
	ps.mu.Lock()

	delete(ps.pubToSub[pub].subs, sub)
	log.Printf("Deleted subscruber %d from publisher %d\n", sub, pub)
	if len(ps.pubToSub[pub].subs) == 0 {
		log.Printf("Deleted publisher %d\n", pub)
		delete(ps.pubToSub, pub)
	}
	s := ps.subToPub[sub]
	s.subsCount--
	ps.subToPub[sub] = s
	if s.subsCount == 0 {
		close(ps.subToPub[sub].feed)
		delete(ps.subToPub, sub)
		log.Printf("Deleted subscriber %d\n", sub)
	}
	ps.mu.Unlock()
}

func (ps *pubsub) publish(pub int64, msg []vkObject.WallWallpost) {
	for sub, _ := range ps.pubToSub[pub].subs {
		ps.subToPub[sub].feed <- msg
	}
}
func (ps *pubsub) stopPubSub() {
	ps.mu.Lock()
	for _, sub := range ps.subToPub {
		close(sub.feed)
	}
	ps.mu.Unlock()
}
