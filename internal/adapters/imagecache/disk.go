// Package imagecache stores bounded, disposable encoded images across launches.
package imagecache

import (
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	magic           = "WHIC0001"
	headerSize      = len(magic) + 8 + sha256.Size
	cacheSuffix     = ".cache"
	temporaryPrefix = ".wallhaven-image-cache-"
	temporarySuffix = ".tmp"
	staleTemporary  = 24 * time.Hour
)

type Options struct {
	Directory string
	// Ready optionally delays image reads and writes until persisted capacity
	// preferences have been restored. Management operations bypass this gate.
	// The owner must also release it before waiting for image workers to stop.
	Ready <-chan struct{}
	// MaxBytes bounds complete cache files, including their small headers.
	MaxBytes int64
	// MaxEntryBytes bounds the encoded image payload of a single entry.
	MaxEntryBytes int64
	MaxEntries    int
}

// Usage describes complete cache files, including their integrity headers.
type Usage struct {
	Bytes   int64
	Entries int
	Limit   int64
}

type diskEntry struct {
	name string
	size int64
}

// Disk is a best-effort cache: failures become misses or ignored writes. Its
// synchronous methods belong on image workers, never on the UI thread. New does
// no I/O; the first operation initializes the directory and its LRU index.
// Separate instances share complete files safely, but their capacity accounting
// is independent and therefore best-effort while both are writing.
type Disk struct {
	mu                      sync.Mutex
	directory               string
	ready                   <-chan struct{}
	maxBytes, maxEntryBytes int64
	maxEntries              int
	initialized, enabled    bool
	initializationError     error
	generation              atomic.Uint64
	entries                 map[string]*list.Element
	recent                  list.List
	used                    int64
}

func New(options Options) *Disk {
	if options.MaxBytes <= 0 {
		options.MaxBytes = 64 << 20
	}
	if options.MaxEntryBytes <= 0 {
		options.MaxEntryBytes = 8 << 20
	}
	if options.MaxEntries <= 0 {
		options.MaxEntries = 512
	}
	return &Disk{directory: options.Directory, ready: options.Ready, maxBytes: options.MaxBytes, maxEntryBytes: options.MaxEntryBytes, maxEntries: options.MaxEntries}
}

func cacheName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]) + cacheSuffix
}

func isCacheName(name string) bool {
	if len(name) != sha256.Size*2+len(cacheSuffix) || !strings.HasSuffix(name, cacheSuffix) {
		return false
	}
	for _, c := range name[:sha256.Size*2] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func isTemporaryName(name string) bool {
	if !strings.HasPrefix(name, temporaryPrefix) || !strings.HasSuffix(name, temporarySuffix) {
		return false
	}
	identifier := strings.TrimSuffix(strings.TrimPrefix(name, temporaryPrefix), temporarySuffix)
	if identifier == "" {
		return false
	}
	for _, c := range identifier {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (c *Disk) initializeLocked() bool {
	if c.initialized {
		return c.enabled
	}
	c.initialized = true
	if c.directory == "" {
		c.initializationError = errors.New("image cache directory is not configured")
		return false
	}
	if err := os.MkdirAll(c.directory, 0700); err != nil {
		c.initializationError = err
		return false
	}
	info, err := os.Lstat(c.directory)
	if err != nil {
		c.initializationError = err
		return false
	}
	if !info.IsDir() {
		c.initializationError = errors.New("image cache directory is not a directory")
		return false
	}
	if info.Mode().Perm()&0222 == 0 {
		c.initializationError = errors.New("image cache directory is not writable")
		return false
	}
	if err := os.Chmod(c.directory, 0700); err != nil {
		c.initializationError = err
		return false
	}
	// A read-only directory can still be readable; require a successful write
	// before enabling the cache. The probe has the same private mode as entries.
	probe, err := os.CreateTemp(c.directory, temporaryPrefix+"*"+temporarySuffix)
	if err != nil {
		c.initializationError = err
		return false
	}
	name := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(name)
	if closeErr != nil || removeErr != nil {
		c.initializationError = errors.Join(closeErr, removeErr)
		return false
	}
	if err := c.scanLocked(true); err != nil {
		c.initializationError = err
		return false
	}
	c.enabled = true
	_ = c.pruneLocked("")
	return true
}

// scanLocked refreshes metadata for management operations. Image Get/Put retain
// their incremental index and never scan the whole directory after startup.
func (c *Disk) scanLocked(cleanTemporary bool) error {
	files, err := os.ReadDir(c.directory)
	if err != nil {
		return err
	}
	type stored struct {
		name     string
		size     int64
		modified time.Time
	}
	var storedFiles []stored
	var failures []error
	now := time.Now()
	for _, file := range files {
		if !isCacheName(file.Name()) && !isTemporaryName(file.Name()) {
			continue
		}
		info, err := file.Info()
		if err != nil {
			if !os.IsNotExist(err) {
				failures = append(failures, err)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if isTemporaryName(file.Name()) {
			if cleanTemporary && now.Sub(info.ModTime()) >= staleTemporary {
				if err := os.Remove(filepath.Join(c.directory, file.Name())); err != nil && !os.IsNotExist(err) {
					failures = append(failures, err)
				}
			}
			continue
		}
		storedFiles = append(storedFiles, stored{file.Name(), info.Size(), info.ModTime()})
	}
	sort.Slice(storedFiles, func(i, j int) bool {
		if storedFiles[i].modified.Equal(storedFiles[j].modified) {
			return storedFiles[i].name < storedFiles[j].name
		}
		return storedFiles[i].modified.After(storedFiles[j].modified)
	})
	c.entries = make(map[string]*list.Element, len(storedFiles))
	c.recent.Init()
	c.used = 0
	for _, file := range storedFiles {
		c.entries[file.name] = c.recent.PushBack(diskEntry{file.name, file.size})
		c.used += file.size
	}
	return errors.Join(failures...)
}

// Get returns an independent snapshot when its size and checksum are valid.
// A damaged entry remains replaceable by Put and never reaches image decoders.
func (c *Disk) Get(key string) ([]byte, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	c.waitReady()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.initializeLocked() {
		return nil, false
	}
	name := cacheName(key)
	path := filepath.Join(c.directory, name)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			c.forgetLocked(name)
		}
		return nil, false
	}
	if !info.Mode().IsRegular() {
		return nil, false
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	// Inspect the opened file, which can be a newer complete atomic replacement.
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= int64(headerSize) || info.Size()-int64(headerSize) > c.maxEntryBytes || info.Size() > c.maxBytes {
		return nil, false
	}
	var header [headerSize]byte
	if _, err := io.ReadFull(file, header[:]); err != nil || string(header[:len(magic)]) != magic {
		return nil, false
	}
	length := binary.BigEndian.Uint64(header[len(magic) : len(magic)+8])
	if length != uint64(info.Size()-int64(headerSize)) {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(length)+1))
	if err != nil || uint64(len(data)) != length {
		return nil, false
	}
	checksum := sha256.Sum256(data)
	if !bytes.Equal(checksum[:], header[len(magic)+8:]) {
		return nil, false
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	c.rememberLocked(name, info.Size())
	_ = c.pruneLocked(name)
	return data, true
}

func (c *Disk) Put(key string, data []byte) {
	c.PutIfCurrent(key, data, c.Generation())
}

// Generation identifies writes permitted after the most recent Clear. Capture
// it before starting a network request and pass it to PutIfCurrent on completion.
func (c *Disk) Generation() uint64 {
	if c == nil {
		return 0
	}
	return c.generation.Load()
}

func (c *Disk) PutIfCurrent(key string, data []byte, generation uint64) {
	if c == nil || key == "" || len(data) == 0 || int64(len(data)) > c.maxEntryBytes {
		return
	}
	c.waitReady()
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation.Load() || c.maxBytes < int64(headerSize) || int64(len(data)) > c.maxBytes-int64(headerSize) || !c.initializeLocked() {
		return
	}
	file, err := os.CreateTemp(c.directory, temporaryPrefix+"*"+temporarySuffix)
	if err != nil {
		return
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	var header [headerSize]byte
	copy(header[:], magic)
	binary.BigEndian.PutUint64(header[len(magic):len(magic)+8], uint64(len(data)))
	checksum := sha256.Sum256(data)
	copy(header[len(magic)+8:], checksum[:])
	if count, writeErr := file.Write(header[:]); writeErr != nil {
		err = writeErr
	} else if count != len(header) {
		err = io.ErrShortWrite
	}
	if err == nil {
		if count, writeErr := file.Write(data); writeErr != nil {
			err = writeErr
		} else if count != len(data) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return
	}
	name := cacheName(key)
	if err := os.Rename(temporary, filepath.Join(c.directory, name)); err != nil {
		return
	}
	c.rememberLocked(name, int64(headerSize+len(data)))
	_ = c.pruneLocked(name)
}

func (c *Disk) waitReady() {
	if c.ready != nil {
		<-c.ready
	}
}

func (c *Disk) Delete(key string) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.initializeLocked() {
		return
	}
	name := cacheName(key)
	path := filepath.Join(c.directory, name)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		c.forgetLocked(name)
		return
	}
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	if err := os.Remove(path); err == nil || os.IsNotExist(err) {
		c.forgetLocked(name)
	}
}

func (c *Disk) usageLocked() Usage {
	return Usage{Bytes: c.used, Entries: len(c.entries), Limit: c.maxBytes}
}

func (c *Disk) unavailableLocked() error {
	return fmt.Errorf("image cache is unavailable: %w", c.initializationError)
}

// Stats rescans complete owned files so changes made by another instance are
// reflected in the result. Unlike best-effort image reads, management reports
// unavailable directories and filesystem failures to the caller.
func (c *Disk) Stats() (Usage, error) {
	if c == nil {
		return Usage{}, errors.New("image cache is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.initializeLocked() {
		return c.usageLocked(), c.unavailableLocked()
	}
	err := c.scanLocked(false)
	return c.usageLocked(), err
}

// Limit returns the configured capacity without initializing or reading disk.
func (c *Disk) Limit() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxBytes
}

// SetLimit applies a positive byte limit and immediately removes the oldest
// complete entries as needed. The new limit remains applied if eviction only
// partially succeeds; the returned error reports that failure. An unavailable
// directory leaves the previous configured limit unchanged.
func (c *Disk) SetLimit(maxBytes int64) error {
	if maxBytes <= 0 {
		return errors.New("image cache limit must be greater than zero")
	}
	if c == nil {
		return errors.New("image cache is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previous := c.maxBytes
	// Restored preferences must take effect before lazy startup scans and
	// prunes files, otherwise an older default could evict still-valid data.
	c.maxBytes = maxBytes
	if !c.initializeLocked() {
		c.maxBytes = previous
		return c.unavailableLocked()
	}
	if err := c.scanLocked(false); err != nil {
		c.maxBytes = previous
		return err
	}
	return c.pruneLocked("")
}

// Clear invalidates earlier writers even if removal only partially succeeds.
// It leaves downloads, unrelated files, directories and fresh temporary files
// untouched. A fetch begun before Clear cannot repopulate this Disk when its
// result is stored through PutIfCurrent with the captured generation.
func (c *Disk) Clear() error {
	if c == nil {
		return errors.New("image cache is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation.Add(1)
	if !c.initializeLocked() {
		return c.unavailableLocked()
	}
	files, err := os.ReadDir(c.directory)
	if err != nil {
		return err
	}
	var failures []error
	now := time.Now()
	for _, file := range files {
		if !isCacheName(file.Name()) && !isTemporaryName(file.Name()) {
			continue
		}
		info, err := file.Info()
		if err != nil {
			if !os.IsNotExist(err) {
				failures = append(failures, err)
			}
			continue
		}
		if !info.Mode().IsRegular() || isTemporaryName(file.Name()) && now.Sub(info.ModTime()) < staleTemporary {
			continue
		}
		if err := os.Remove(filepath.Join(c.directory, file.Name())); err != nil && !os.IsNotExist(err) {
			failures = append(failures, err)
		}
	}
	if err := c.scanLocked(false); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (c *Disk) forgetLocked(name string) {
	if previous := c.entries[name]; previous != nil {
		c.used -= previous.Value.(diskEntry).size
		c.recent.Remove(previous)
		delete(c.entries, name)
	}
}

func (c *Disk) rememberLocked(name string, size int64) {
	c.forgetLocked(name)
	c.entries[name] = c.recent.PushFront(diskEntry{name, size})
	c.used += size
}

func (c *Disk) pruneLocked(protect string) error {
	var failures []error
	for position := c.recent.Back(); position != nil && (c.used > c.maxBytes || len(c.entries) > c.maxEntries); {
		previous := position.Prev()
		entry := position.Value.(diskEntry)
		if entry.name != protect {
			path := filepath.Join(c.directory, entry.name)
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				c.forgetLocked(entry.name)
			} else if err != nil {
				failures = append(failures, err)
			} else if !info.Mode().IsRegular() {
				c.forgetLocked(entry.name)
			} else {
				if err := os.Remove(path); err == nil || os.IsNotExist(err) {
					c.forgetLocked(entry.name)
				} else {
					failures = append(failures, err)
				}
			}
		}
		position = previous
	}
	if len(failures) == 0 && (c.used > c.maxBytes || len(c.entries) > c.maxEntries) {
		return errors.New("image cache could not be reduced to its limit")
	}
	return errors.Join(failures...)
}
