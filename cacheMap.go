package main

import (
	"math"
	"sync"
	"time"
)

type CacheEntry[ValueType any] struct {
	data      ValueType
	validTill int64
}

func NewCacheEntry[ValueType any](data ValueType, validTill int64) CacheEntry[ValueType] {
	return CacheEntry[ValueType]{
		data:      data,
		validTill: validTill,
	}
}

func NewCacheEntryForever[ValueType any](data ValueType) CacheEntry[ValueType] {
	return CacheEntry[ValueType]{
		data:      data,
		validTill: math.MaxInt64,
	}
}

func (e *CacheEntry[ValueType]) valid() bool {
	return time.Now().Unix() < e.validTill
}

type mapType[KeyType comparable, ValueType any] map[KeyType]CacheEntry[ValueType]

type CacheMap[KeyType comparable, ValueType any] struct {
	first  mapType[KeyType, ValueType]
	second mapType[KeyType, ValueType]
	size   int
	mu     sync.RWMutex
}

func NewCacheMap[KeyType comparable, ValueType any](size int) CacheMap[KeyType, ValueType] {
	return CacheMap[KeyType, ValueType]{
		first:  make(mapType[KeyType, ValueType], size),
		second: make(mapType[KeyType, ValueType], size),
		size:   size,
	}
}

func (m *CacheMap[KeyType, ValueType]) Put(id KeyType, data ValueType, validTill int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.first) < m.size {
		m.first[id] = NewCacheEntry(data, validTill)
		return
	}
	if len(m.second) < m.size {
		m.second[id] = NewCacheEntry(data, validTill)
		return
	}
	m.first = m.second
	m.second = make(mapType[KeyType, ValueType], m.size)
	m.second[id] = NewCacheEntry(data, validTill)
}

func (m *CacheMap[KeyType, ValueType]) Get(id KeyType) (ValueType, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if res, e := m.first[id]; e && res.valid() {
		return res.data, true
	}
	if res, e := m.second[id]; e && res.valid() {
		return res.data, true
	}
	var res ValueType
	return res, false
}
