package imageactivity

import "sync"

type imageBytes struct {
	data []byte
	mime string
}
type memoryItem struct {
	owner                       int64
	payload                     []byte
	reserved                    int64
	committed, running, retired bool
	images                      []imageBytes
	expires                     int64
	readers                     int
}
type memoryStore struct {
	mu           sync.Mutex
	used, queued int64
	items        map[string]*memoryItem
}

func (m *memoryStore) reserveQueued(id string, owner int64, payload []byte, limit int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	size := int64(cap(payload))
	if _, ok := m.items[id]; ok || size > limit-m.used || size > limit/4-m.queued {
		return false
	}
	m.items[id] = &memoryItem{owner: owner, payload: payload, reserved: size}
	m.used += size
	m.queued += size
	return true
}
func (m *memoryStore) commitQueued(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil || item.retired {
		return false
	}
	item.committed = true
	return true
}
func (m *memoryStore) prepareExecution(id string, limit int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil || !item.committed || item.retired || item.running {
		return false
	}
	delta := executionMemory - item.reserved
	if delta > limit-m.used {
		return false
	}
	m.used += delta
	m.queued -= item.reserved
	item.reserved = executionMemory
	item.running = true
	return true
}
func (m *memoryStore) restoreExecution(id string, owner int64, limit int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item := m.items[id]; item != nil {
		return item.running
	}
	if executionMemory > limit-m.used {
		return false
	}
	m.items[id] = &memoryItem{owner: owner, reserved: executionMemory, running: true, committed: true, retired: owner == 0}
	m.used += executionMemory
	return true
}
func (m *memoryStore) rollbackExecution(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil || !item.running || item.images != nil {
		return
	}
	if item.retired {
		item.running = false
		m.releaseLocked(id, item)
		return
	}
	size := int64(cap(item.payload))
	m.used -= item.reserved - size
	m.queued += size
	item.reserved = size
	item.running = false
}
func (m *memoryStore) payload(id string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil || item.retired || !item.running {
		return nil
	}
	return item.payload
}
func (m *memoryStore) dropPayload(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item := m.items[id]; item != nil {
		item.payload = nil
	}
}
func (m *memoryStore) publish(id string, owner int64, images []imageBytes, expires int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil || item.retired || !item.running || item.owner != owner || owner <= 0 {
		return false
	}
	size := int64(0)
	for _, img := range images {
		size += int64(cap(img.data))
	}
	if size > maxImages {
		return false
	}
	item.payload = nil
	m.used -= item.reserved - size
	item.reserved = size
	item.running = false
	item.images = images
	item.expires = expires
	return true
}

// Retiring an active task revokes publication immediately, but its memory
// remains charged until the HTTP job finishes. A slow download also retains
// its original charge until its last reader releases the immutable bytes.
func (m *memoryStore) purge(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil {
		return
	}
	item.retired = true
	if !item.running {
		m.releaseLocked(id, item)
	}
}
func (m *memoryStore) discard(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil {
		return
	}
	item.retired = true
	item.running = false
	m.releaseLocked(id, item)
}
func (m *memoryStore) releaseLocked(id string, item *memoryItem) {
	if !item.retired || item.running || item.readers > 0 {
		return
	}
	if item.images == nil && item.reserved < executionMemory {
		m.queued -= item.reserved
	}
	m.used -= item.reserved
	delete(m.items, id)
}
func (m *memoryStore) metadata(id string, user, now int64) []ImageInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	out := []ImageInfo{}
	if item == nil || item.retired || item.owner != user || item.expires <= now {
		return out
	}
	for i, img := range item.images {
		out = append(out, ImageInfo{Index: i, MIME: img.mime, Bytes: len(img.data)})
	}
	return out
}
func (m *memoryStore) image(id string, user, now int64, index int) (imageBytes, func(), bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := m.items[id]
	if item == nil || item.retired || item.owner != user || item.expires <= now || index < 0 || index >= len(item.images) {
		return imageBytes{}, nil, false
	}
	item.readers++
	var once sync.Once
	release := func() {
		once.Do(func() { m.mu.Lock(); defer m.mu.Unlock(); item.readers--; m.releaseLocked(id, item) })
	}
	return item.images[index], release, true
}
func (m *memoryStore) expire(now int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, item := range m.items {
		if len(item.images) > 0 && item.expires <= now {
			item.retired = true
			m.releaseLocked(id, item)
		}
	}
}
func (m *memoryStore) ids() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.items))
	for id, item := range m.items {
		if item.committed {
			ids = append(ids, id)
		}
	}
	return ids
}
func (m *memoryStore) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, item := range m.items {
		item.retired = true
		item.running = false
		m.releaseLocked(id, item)
	}
}
