package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/krau/btts/config"
)

var (
	fileCacheTTL  time.Duration
	fileCacheOnce sync.Once
)

type fileCacheMeta struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Complete bool   `json:"complete"`
}

func initFileCache() {
	fileCacheOnce.Do(func() {
		ttl, err := time.ParseDuration(config.C.FileCache.TTL)
		if err != nil {
			ttl = 24 * time.Hour
		}
		fileCacheTTL = ttl
		dir := config.C.FileCache.Dir
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			log.Errorf("Failed to create file cache directory %s: %v", dir, err)
		}
		go fileCacheCleanupLoop()
	})
}

func fileCachePath(chatID int64, messageID int) string {
	return filepath.Join(config.C.FileCache.Dir, fmt.Sprintf("%d_%d.cache", chatID, messageID))
}

func fileCacheMetaPath(chatID int64, messageID int) string {
	return filepath.Join(config.C.FileCache.Dir, fmt.Sprintf("%d_%d.meta", chatID, messageID))
}

func fileCacheTmpPath(chatID int64, messageID int) string {
	// Include goroutine-unique suffix to avoid conflicts from concurrent requests for the same file
	return filepath.Join(config.C.FileCache.Dir, fmt.Sprintf("%d_%d.%d.cache.tmp", chatID, messageID, time.Now().UnixNano()))
}

// fileCacheCleanupLoop periodically scans the cache directory and removes expired files.
func fileCacheCleanupLoop() {
	cleanExpiredCache()

	interval := max(fileCacheTTL/2, time.Minute)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		cleanExpiredCache()
	}
}

func cleanExpiredCache() {
	dir := config.C.FileCache.Dir
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Errorf("Failed to read file cache directory: %v", err)
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Clean up stale .tmp files (older than TTL)
		if strings.Contains(name, ".cache.tmp") {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if now.Sub(info.ModTime()) > fileCacheTTL {
				os.Remove(filepath.Join(dir, name))
			}
			continue
		}
		if !strings.HasSuffix(name, ".cache") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > fileCacheTTL {
			cachePath := filepath.Join(dir, name)
			metaPath := strings.TrimSuffix(cachePath, ".cache") + ".meta"
			if err := os.Remove(cachePath); err != nil {
				log.Errorf("Failed to remove expired cache file %s: %v", cachePath, err)
			}
			os.Remove(metaPath)
		}
	}
}

func readCacheMeta(chatID int64, messageID int) (*fileCacheMeta, error) {
	data, err := os.ReadFile(fileCacheMetaPath(chatID, messageID))
	if err != nil {
		return nil, err
	}
	var meta fileCacheMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func writeCacheMeta(chatID int64, messageID int, meta *fileCacheMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(fileCacheMetaPath(chatID, messageID), data, 0644)
}

// getCachedFileReader returns a TGFileFileReader for a cached file if it exists and is not expired.
// Lock-free: only reads finalized .cache files (never .tmp), so no conflict with ongoing writes.
func getCachedFileReader(chatID int64, messageID int) (*TGFileFileReader, error) {
	path := fileCachePath(chatID, messageID)

	info, err := os.Stat(path)
	if err != nil {
		return nil, nil // cache miss
	}
	if time.Since(info.ModTime()) > fileCacheTTL {
		os.Remove(path)
		os.Remove(fileCacheMetaPath(chatID, messageID))
		return nil, nil // expired
	}
	meta, err := readCacheMeta(chatID, messageID)
	if err != nil {
		// No meta → cache is unusable (can't determine filename/MIME).
		os.Remove(path)
		return nil, nil
	}
	if !meta.Complete {
		// Incomplete cache (partial read last time) → discard and re-download.
		os.Remove(path)
		os.Remove(fileCacheMetaPath(chatID, messageID))
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	return &TGFileFileReader{
		RC:       f,
		Size:     info.Size(),
		Name:     meta.Name,
		FilePath: path,
	}, nil
}

// cacheWriter wraps the original reader and simultaneously writes data to a .tmp cache file.
// On Close, the .tmp file handle is closed first, then renamed to .cache atomically.
// No locks are held during streaming — the caller gets data immediately as it arrives.
type cacheWriter struct {
	original  io.ReadCloser
	cacheFile *os.File
	tmpPath   string
	chatID    int64
	messageID int
	fileName  string
	fileSize  int64
	logger    *log.Logger
	written   int64
	writeErr  bool // if true, discard cache on close
}

func newCacheWriter(original io.ReadCloser, chatID int64, messageID int, fileName string, fileSize int64, logger *log.Logger) (*cacheWriter, error) {
	tmpPath := fileCacheTmpPath(chatID, messageID)
	f, err := os.Create(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache temp file: %w", err)
	}
	return &cacheWriter{
		original:  original,
		cacheFile: f,
		tmpPath:   tmpPath,
		chatID:    chatID,
		messageID: messageID,
		fileName:  fileName,
		fileSize:  fileSize,
		logger:    logger,
	}, nil
}

func (cw *cacheWriter) Read(p []byte) (int, error) {
	n, err := cw.original.Read(p)
	if n > 0 && !cw.writeErr {
		if _, werr := cw.cacheFile.Write(p[:n]); werr != nil {
			cw.logger.Error("Failed to write to cache file", "error", werr)
			cw.writeErr = true
		} else {
			cw.written += int64(n)
		}
	}
	return n, err
}

func (cw *cacheWriter) Close() error {
	origErr := cw.original.Close()

	// Close the file handle FIRST — releases the Windows file lock before rename
	cw.cacheFile.Close()

	cachePath := fileCachePath(cw.chatID, cw.messageID)

	complete := cw.fileSize > 0 && cw.written == cw.fileSize

	if cw.written > 0 && !cw.writeErr {
		// If a finalized cache already exists (another concurrent request won the race),
		// just discard our tmp file.
		if _, err := os.Stat(cachePath); err == nil {
			os.Remove(cw.tmpPath)
		} else if err := os.Rename(cw.tmpPath, cachePath); err != nil {
			cw.logger.Warn("Failed to finalize cache file, will retry next time", "error", err)
			os.Remove(cw.tmpPath)
		} else {
			meta := &fileCacheMeta{
				Name:     cw.fileName,
				Size:     cw.fileSize,
				Complete: complete,
			}
			if err := writeCacheMeta(cw.chatID, cw.messageID, meta); err != nil {
				cw.logger.Error("Failed to write cache meta file", "error", err)
			}
		}
	} else {
		os.Remove(cw.tmpPath)
	}
	return origErr
}

// wrapWithCache wraps a TGFileFileReader so that read data is streamed to the caller
// and simultaneously written to a disk cache file. No locks are held during streaming.
func wrapWithCache(ctx context.Context, reader *TGFileFileReader, chatID int64, messageID int) *TGFileFileReader {
	logger := log.FromContext(ctx)
	cw, err := newCacheWriter(reader.RC, chatID, messageID, reader.Name, reader.Size, logger)
	if err != nil {
		logger.Error("Failed to create cache writer, serving without cache", "error", err)
		return reader
	}
	return &TGFileFileReader{
		RC:   cw,
		Size: reader.Size,
		Name: reader.Name,
	}
}
