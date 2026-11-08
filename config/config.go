package config

import (
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/spf13/viper"
)

type AppConfig struct {
	AppID          int     `toml:"app_id" mapstructure:"app_id"`
	AppHash        string  `toml:"app_hash" mapstructure:"app_hash"`
	BotToken       string  `toml:"bot_token" mapstructure:"bot_token"`
	Admins         []int64 `toml:"admins" mapstructure:"admins"`
	ClientLogLevel string  `toml:"client_log_level" mapstructure:"client_log_level"`
	Plugin         struct {
		Enable   bool     `toml:"enable" mapstructure:"enable"`
		Prefixes []string `toml:"prefixes" mapstructure:"prefixes"`
	} `toml:"plugin" mapstructure:"plugin"`
	Engine struct {
		Type     string `toml:"type" mapstructure:"type"` // "meilisearch" or "bleve"
		Url      string `toml:"url" mapstructure:"url"`
		Index    string `toml:"index" mapstructure:"index"` // For meilisearch: index uid
		Key      string `toml:"key" mapstructure:"key"`
		Embedder struct {
			Name             string `toml:"name" mapstructure:"name"`
			Source           string `toml:"source" mapstructure:"source"`
			Model            string `toml:"model" mapstructure:"model"`
			ApiKey           string `toml:"api_key" mapstructure:"api_key"`
			DocumentTemplate string `toml:"document_template" mapstructure:"document_template"`
			Dimensions       int    `toml:"dimensions" mapstructure:"dimensions"`
			URL              string `toml:"url" mapstructure:"url"`
		} `toml:"embedder" mapstructure:"embedder"`
	} `toml:"engine" mapstructure:"engine"`
	Ocr struct {
		Enable bool   `toml:"enable" mapstructure:"enable"`
		Type   string `toml:"type" mapstructure:"type"` // "paddle"
		Paddle struct {
			Url       string  `toml:"url" mapstructure:"url"`
			Threshold float64 `toml:"threshold" mapstructure:"threshold"`
		}
	}
	Api struct {
		Enable bool   `toml:"enable" mapstructure:"enable"`
		Addr   string `toml:"addr" mapstructure:"addr"`
		Key    string `toml:"key" mapstructure:"key"`
	}
	FileCache struct {
		Disable bool   `toml:"disable" mapstructure:"disable"`
		Dir     string `toml:"dir" mapstructure:"dir"`
		TTL     string `toml:"ttl" mapstructure:"ttl"`
	} `toml:"file_cache" mapstructure:"file_cache"`
	// Import controls cold-history import, independent of enrichment.
	Import struct {
		BatchDelay time.Duration `toml:"batch_delay" mapstructure:"batch_delay"`
		Background bool          `toml:"background" mapstructure:"background"`
	} `toml:"import" mapstructure:"import"`
	Enrich struct {
		Enable                 bool          `toml:"enable" mapstructure:"enable"`
		Workers                int           `toml:"workers" mapstructure:"workers"`
		MaxDownloadMB          int           `toml:"max_download_mb" mapstructure:"max_download_mb"`
		MaxAttempts            int           `toml:"max_attempts" mapstructure:"max_attempts"`
		RetryBackoff           time.Duration `toml:"retry_backoff" mapstructure:"retry_backoff"`
		WaitIndexTasks         bool          `toml:"wait_index_tasks" mapstructure:"wait_index_tasks"`
		MediaFetchPerSec       float64       `toml:"media_fetch_per_second" mapstructure:"media_fetch_per_second"`
		MediaFetchConcurrency  int           `toml:"media_fetch_concurrency" mapstructure:"media_fetch_concurrency"`
		MediaEnrichPerSec      float64       `toml:"media_enrich_per_second" mapstructure:"media_enrich_per_second"`
		MediaEnrichConcurrency int           `toml:"media_enrich_concurrency" mapstructure:"media_enrich_concurrency"`
		MediaFetchRetries      int           `toml:"media_fetch_retries" mapstructure:"media_fetch_retries"`
		FloodWaitExtra         time.Duration `toml:"flood_wait_extra" mapstructure:"flood_wait_extra"`
		Embedder               struct {
			Enable        bool          `toml:"enable" mapstructure:"enable"`
			MediaName     string        `toml:"media_name" mapstructure:"media_name"`
			URL           string        `toml:"url" mapstructure:"url"`
			Model         string        `toml:"model" mapstructure:"model"`
			Dimensions    int           `toml:"dimensions" mapstructure:"dimensions"`
			Timeout       time.Duration `toml:"timeout" mapstructure:"timeout"`
			QueryTimeout  time.Duration `toml:"query_timeout" mapstructure:"query_timeout"`
			MediaEnable   bool          `toml:"media_enable" mapstructure:"media_enable"`
			MediaMinScore float64       `toml:"media_min_score" mapstructure:"media_min_score"`
			AudioEnable   bool          `toml:"audio_enable" mapstructure:"audio_enable"`
			VideoEnable   bool          `toml:"video_enable" mapstructure:"video_enable"`
			VideoFrames   int           `toml:"video_frames" mapstructure:"video_frames"`
			Warmup        bool          `toml:"warmup" mapstructure:"warmup"`
			NumCtx        int           `toml:"num_ctx" mapstructure:"num_ctx"`
		} `toml:"embedder" mapstructure:"embedder"`
		Tagger struct {
			Enable    bool          `toml:"enable" mapstructure:"enable"`
			URL       string        `toml:"url" mapstructure:"url"`
			Token     string        `toml:"token" mapstructure:"token"`
			Threshold float64       `toml:"threshold" mapstructure:"threshold"`
			MaxTags   int           `toml:"max_tags" mapstructure:"max_tags"`
			Timeout   time.Duration `toml:"timeout" mapstructure:"timeout"`
		} `toml:"tagger" mapstructure:"tagger"`
		Documents struct {
			Enable    bool   `toml:"enable" mapstructure:"enable"`
			PDFToText string `toml:"pdftotext" mapstructure:"pdftotext"`
			MaxSizeMB int    `toml:"max_size_mb" mapstructure:"max_size_mb"`
			MaxChars  int    `toml:"max_chars" mapstructure:"max_chars"`
		} `toml:"documents" mapstructure:"documents"`
	} `toml:"enrich" mapstructure:"enrich"`
}

var C AppConfig

const maxConfiguredVideoFrames = 8

func Init() {
	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	viper.AddConfigPath(".")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.SetEnvPrefix("btts")
	viper.AutomaticEnv()

	viper.SetDefault("engine.index", "btts")
	viper.SetDefault("engine.type", "meilisearch")
	viper.SetDefault("file_cache.disable", false)
	viper.SetDefault("file_cache.dir", "data/file_cache")
	viper.SetDefault("file_cache.ttl", "24h")
	viper.SetDefault("client_log_level", "info")

	viper.SetDefault("plugin.prefixes", []string{","})

	viper.SetDefault("import.batch_delay", "1s")
	viper.SetDefault("import.background", true)

	viper.SetDefault("enrich.workers", 2)
	viper.SetDefault("enrich.max_download_mb", 20)
	viper.SetDefault("enrich.max_attempts", 5)
	viper.SetDefault("enrich.retry_backoff", "30s")
	viper.SetDefault("enrich.wait_index_tasks", true)
	viper.SetDefault("enrich.media_fetch_per_second", 1.0)
	viper.SetDefault("enrich.media_fetch_concurrency", 1)
	viper.SetDefault("enrich.media_enrich_per_second", 1.0)
	viper.SetDefault("enrich.media_enrich_concurrency", 1)
	viper.SetDefault("enrich.media_fetch_retries", 3)
	viper.SetDefault("enrich.flood_wait_extra", "5s")
	viper.SetDefault("enrich.embedder.media_name", "btts_media")
	viper.SetDefault("enrich.embedder.url", "http://127.0.0.1:11434")
	viper.SetDefault("enrich.embedder.model", "embeddinggemma-2:latest")
	viper.SetDefault("enrich.embedder.dimensions", 768)
	viper.SetDefault("enrich.embedder.timeout", "30s")
	viper.SetDefault("enrich.embedder.query_timeout", "10s")
	viper.SetDefault("enrich.embedder.media_enable", true)
	viper.SetDefault("enrich.embedder.media_min_score", 0.8)
	viper.SetDefault("enrich.embedder.audio_enable", false)
	viper.SetDefault("enrich.embedder.video_enable", true)
	viper.SetDefault("enrich.embedder.video_frames", 3)
	viper.SetDefault("enrich.embedder.num_ctx", 8192)
	viper.SetDefault("enrich.tagger.threshold", 0.4)
	viper.SetDefault("enrich.tagger.max_tags", 32)
	viper.SetDefault("enrich.tagger.timeout", "5s")
	viper.SetDefault("enrich.documents.pdftotext", "pdftotext")
	viper.SetDefault("enrich.documents.max_size_mb", 20)
	viper.SetDefault("enrich.documents.max_chars", 12000)

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}
	if err := viper.Unmarshal(&C); err != nil {
		log.Fatalf("Unable to decode into struct, %v", err)
	}
	if C.Api.Enable && C.Api.Key == "" {
		log.Warn("API is enabled but API key is not set!\nThis should only be used for testing purposes!")
	}
	normalize()
}

// normalize fills in defaults once so constructors do not repeat them.
func normalize() {
	if C.Import.BatchDelay < 0 {
		C.Import.BatchDelay = 0
	}
	enrich := &C.Enrich
	if enrich.Workers <= 0 {
		enrich.Workers = 1
	}
	if enrich.MaxDownloadMB <= 0 {
		enrich.MaxDownloadMB = 20
	}
	if enrich.MaxAttempts <= 0 {
		enrich.MaxAttempts = 5
	}
	if enrich.RetryBackoff <= 0 {
		enrich.RetryBackoff = 30 * time.Second
	}
	if enrich.MediaFetchPerSec < 0 {
		enrich.MediaFetchPerSec = 0
	}
	if enrich.MediaFetchConcurrency <= 0 {
		enrich.MediaFetchConcurrency = 1
	}
	if enrich.MediaEnrichConcurrency <= 0 {
		enrich.MediaEnrichConcurrency = 1
	}
	if enrich.MediaEnrichPerSec < 0 {
		enrich.MediaEnrichPerSec = 0
	}
	if enrich.MediaFetchRetries <= 0 {
		enrich.MediaFetchRetries = 3
	}
	if enrich.FloodWaitExtra < 0 {
		enrich.FloodWaitExtra = 0
	}
	if enrich.Embedder.Timeout <= 0 {
		enrich.Embedder.Timeout = 30 * time.Second
	}
	if enrich.Embedder.QueryTimeout <= 0 {
		enrich.Embedder.QueryTimeout = 10 * time.Second
	}
	if enrich.Embedder.NumCtx <= 0 {
		enrich.Embedder.NumCtx = 8192
	}
	if enrich.Embedder.VideoFrames <= 0 {
		enrich.Embedder.VideoFrames = 3
	}
	if enrich.Embedder.VideoFrames > maxConfiguredVideoFrames {
		enrich.Embedder.VideoFrames = maxConfiguredVideoFrames
	}
	if enrich.Tagger.MaxTags <= 0 {
		enrich.Tagger.MaxTags = 32
	}
	if enrich.Tagger.Timeout <= 0 {
		enrich.Tagger.Timeout = 5 * time.Second
	}
	if enrich.Documents.PDFToText == "" {
		enrich.Documents.PDFToText = "pdftotext"
	}
	if enrich.Documents.MaxSizeMB <= 0 {
		enrich.Documents.MaxSizeMB = 20
	}
	if enrich.Documents.MaxChars <= 0 {
		enrich.Documents.MaxChars = 12000
	}
	if C.Enrich.Enable && C.Enrich.Embedder.Enable {
		if C.Enrich.Embedder.URL == "" {
			log.Fatal("enrich.embedder.url is required when the embedder is enabled")
		}
		if C.Enrich.Embedder.Model == "" {
			log.Fatal("enrich.embedder.model is required when the embedder is enabled")
		}
		if C.Enrich.Embedder.Dimensions <= 0 {
			log.Fatal("enrich.embedder.dimensions must be positive")
		}
		if C.Enrich.Embedder.MediaMinScore < 0 || C.Enrich.Embedder.MediaMinScore > 1 {
			log.Fatal("enrich.embedder.media_min_score must be between 0 and 1")
		}
	}
}
