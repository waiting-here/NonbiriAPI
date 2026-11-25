package logapi

import (
	"context"
	"encoding/binary"
	"os"
	"sync"

	"github.com/waiting-here/NonbiriAPI/internal/pagination"
)

// The scratch file contains only ordered row IDs and their visibility deadlines.
// A block directory keeps exact counts and supports direct jumps without
// retaining the SQL transaction or allocating memory proportional to row size.
const (
	searchBlockRows  = 512
	searchRecordSize = 16
)

type searchBlock struct {
	offset     int64
	rows, live int
	nextExpiry int64
}

type searchMatches struct {
	mu       sync.Mutex
	file     *os.File
	path     string
	blocks   []searchBlock
	tail     []byte
	tailEnd  int64
	lastRead int64
}

func newSearchMatches() (*searchMatches, error) {
	file, err := os.CreateTemp("", "nonbiri-log-search-*")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	// On platforms that support unlinking open files, the OS also cleans up
	// after an abrupt process exit.
	if os.Remove(path) == nil {
		path = ""
	}
	return &searchMatches{file: file, path: path, tail: make([]byte, 0, searchBlockRows*searchRecordSize)}, nil
}

func (m *searchMatches) add(id, expiresAt int64) error {
	m.tail = binary.LittleEndian.AppendUint64(m.tail, uint64(id))
	m.tail = binary.LittleEndian.AppendUint64(m.tail, uint64(expiresAt))
	m.tailEnd = earliestExpiry(m.tailEnd, expiresAt)
	if len(m.tail) == cap(m.tail) {
		return m.flush()
	}
	return nil
}

func earliestExpiry(current, next int64) int64 {
	if next > 0 && (current == 0 || next < current) {
		return next
	}
	return current
}

func (m *searchMatches) flush() error {
	if len(m.tail) == 0 {
		return nil
	}
	if _, err := m.file.Write(m.tail); err != nil {
		return err
	}
	rows := len(m.tail) / searchRecordSize
	m.blocks = append(m.blocks, searchBlock{
		offset: int64(len(m.blocks)) * searchBlockRows * searchRecordSize,
		rows:   rows, live: rows, nextExpiry: m.tailEnd,
	})
	m.tail = m.tail[:0]
	m.tailEnd = 0
	return nil
}

func (m *searchMatches) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file != nil {
		_ = m.file.Close()
		m.file = nil
		if m.path != "" {
			_ = os.Remove(m.path)
		}
	}
}

func (m *searchMatches) window(ctx context.Context, request pagination.Request, now int64) ([]int64, pagination.Metadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil {
		return nil, pagination.Metadata{}, ErrSearchExpired
	}
	// Concurrent page reads can acquire this lock out of time order. Once a
	// record ages out of the captured set, an earlier request cannot restore it.
	now = max(now, m.lastRead)
	m.lastRead = now
	var buffer [searchBlockRows * searchRecordSize]byte
	var total int64
	for i := range m.blocks {
		if err := ctx.Err(); err != nil {
			return nil, pagination.Metadata{}, err
		}
		block := &m.blocks[i]
		if block.nextExpiry > 0 && block.nextExpiry <= now {
			data := buffer[:block.rows*searchRecordSize]
			if _, err := m.file.ReadAt(data, block.offset); err != nil {
				return nil, pagination.Metadata{}, err
			}
			block.live, block.nextExpiry = 0, 0
			for len(data) > 0 {
				expiry := int64(binary.LittleEndian.Uint64(data[8:]))
				if expiry == 0 || expiry > now {
					block.live++
					block.nextExpiry = earliestExpiry(block.nextExpiry, expiry)
				}
				data = data[searchRecordSize:]
			}
		}
		total += int64(block.live)
	}
	metadata, skip, err := request.Window(total)
	if err != nil {
		return nil, pagination.Metadata{}, ErrInvalid
	}
	ids := make([]int64, 0, request.Size)
	for _, block := range m.blocks {
		if skip >= int64(block.live) {
			skip -= int64(block.live)
			continue
		}
		data := buffer[:block.rows*searchRecordSize]
		if _, err := m.file.ReadAt(data, block.offset); err != nil {
			return nil, pagination.Metadata{}, err
		}
		for len(data) > 0 {
			id := int64(binary.LittleEndian.Uint64(data))
			expiry := int64(binary.LittleEndian.Uint64(data[8:]))
			data = data[searchRecordSize:]
			if expiry > 0 && expiry <= now {
				continue
			}
			if skip > 0 {
				skip--
				continue
			}
			ids = append(ids, id)
			if len(ids) == request.Size {
				return ids, metadata, nil
			}
		}
	}
	return ids, metadata, nil
}
