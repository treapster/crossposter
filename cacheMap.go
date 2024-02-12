package main

import "sync"

type CacheMap[KeyType comparable, ValueType any] struct {
	first  map[KeyType]ValueType
	second map[KeyType]ValueType
	size   int
	mu     sync.RWMutex
}

func NewCacheMap[KeyType comparable, ValueType any](size int) CacheMap[KeyType, ValueType] {
	return CacheMap[KeyType, ValueType]{
		first:  make(map[KeyType]ValueType, size),
		second: make(map[KeyType]ValueType, size),
		size:   size,
	}
}
func (m *CacheMap[KeyType, ValueType]) Put(id KeyType, data ValueType) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.first) < m.size {
		m.first[id] = data
		return
	}
	if len(m.second) < m.size {
		m.second[id] = data
		return
	}
	m.first = m.second
	m.second = make(map[KeyType]ValueType, m.size)
	m.second[id] = data
}

func (m *CacheMap[KeyType, ValueType]) Get(id KeyType) (ValueType, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if res, e := m.first[id]; e {
		return res, true
	}
	if res, e := m.second[id]; e {
		return res, true
	}
	var res ValueType
	return res, false
}
