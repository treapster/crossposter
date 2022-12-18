package main

import (
	"sync"
	"time"
)

type subscriber struct {
	feed      chan update
	subsCount int32
	flags     uint64
}
type subscribersMap = map[int64]uint64
type vkSource struct {
	lastPost int64
	subs     subscribersMap
}

type pubsub struct {
	pubToSub map[int64]vkSource   // vk group id to a list of subscriber ids
	subToPub map[int64]subscriber // tg channel id to it's vk feed and subCount
	mu       sync.RWMutex
}

func (ps *pubsub) subscribe(sub int64, pub int64, flags uint64, consumer func(<-chan update)) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if _, exists := ps.subToPub[sub]; !exists {
		ps.subToPub[sub] = subscriber{feed: make(chan update, 4), subsCount: 0}
		go consumer(ps.subToPub[sub].feed)
	}
	s := ps.subToPub[sub]
	s.subsCount++
	ps.subToPub[sub] = s
	if _, exists := ps.pubToSub[pub]; !exists {
		ps.pubToSub[pub] = vkSource{lastPost: time.Now().Unix(), subs: make(subscribersMap)}
	}
	ps.pubToSub[pub].subs[sub] = flags
}
func (ps *pubsub) addSubscriber(sub int64, consumer func(<-chan update)) {
	if _, exists := ps.subToPub[sub]; !exists {
		ps.subToPub[sub] = subscriber{feed: make(chan update, 4)}
		go consumer(ps.subToPub[sub].feed)
	}

}
func (ps *pubsub) addPublisher(pub int64, pubInstnce vkSource) {
	if _, exists := ps.pubToSub[pub]; !exists {
		ps.pubToSub[pub] = pubInstnce
	}

}
func (ps *pubsub) subscribeSimple(sub int64, pub int64, flags uint64) {
	s := ps.subToPub[sub]
	s.subsCount++
	ps.subToPub[sub] = s
	ps.pubToSub[pub].subs[sub] = flags
}
func (ps *pubsub) updateTimeStamp(pubID int64, lastPost int64) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	pub := ps.pubToSub[pubID]
	pub.lastPost = lastPost
	ps.pubToSub[pubID] = pub
}
func (ps *pubsub) unsubscribe(sub int64, pub int64) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	delete(ps.pubToSub[pub].subs, sub)
	// log.Printf("Deleted subscruber %d from publisher %d\n", sub, pub)
	if len(ps.pubToSub[pub].subs) == 0 {
		// log.Printf("Deleted publisher %d\n", pub)
		delete(ps.pubToSub, pub)
	}
	s := ps.subToPub[sub]
	s.subsCount--
	ps.subToPub[sub] = s
	if s.subsCount == 0 {
		close(ps.subToPub[sub].feed)
		delete(ps.subToPub, sub)
		// log.Printf("Deleted subscriber %d\n", sub)
	}
}

func (ps *pubsub) publish(pub int64, msg []preparedPost) {
	ps.mu.Lock()
	for sub, flags := range ps.pubToSub[pub].subs {
		ps.subToPub[sub].feed <- update{msg, flags}
	}
	ps.mu.Unlock()
}
func (ps *pubsub) stopPubSub() {
	ps.mu.Lock()
	for _, sub := range ps.subToPub {
		close(sub.feed)
	}
	ps.mu.Unlock()
}
