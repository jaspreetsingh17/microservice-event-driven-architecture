package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Article mirrors the scraper event payload.
type Article struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Source      string `json:"source"`
	URL         string `json:"url"`
	ImageURL    string `json:"image_url"`
	Category    string `json:"category"`
	PublishedAt string `json:"published_at"`
}

var (
	processedCount int64

	articlesProcessedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "processor_articles_processed_total",
		Help: "Total number of articles successfully processed and stored.",
	})
)

func main() {
	brokers := getEnv("REDPANDA_BROKERS", "localhost:9092")
	dsn := getEnv("MYSQL_DSN", "newsuser:newspass@tcp(localhost:3306)/newsdb?parseTime=true&charset=utf8mb4")

	log.Printf("[processor] Starting processor-service | brokers=%s", brokers)

	// Connect to MySQL with retries
	var db *sql.DB
	var err error
	for i := 0; i < 30; i++ {
		db, err = sql.Open("mysql", dsn)
		if err == nil {
			err = db.Ping()
		}
		if err == nil {
			break
		}
		log.Printf("[processor] Waiting for MySQL... attempt %d: %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("[processor] Failed to connect to MySQL: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	log.Println("[processor] Connected to MySQL")

	// Kafka consumer
	client, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(brokers, ",")...),
		kgo.ConsumerGroup("news-processor-group"),
		kgo.ConsumeTopics("raw-articles"),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		log.Fatalf("[processor] Failed to create Kafka client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Health endpoint
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			count := atomic.LoadInt64(&processedCount)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":    "ok",
				"service":   "processor",
				"processed": count,
			})
		})
		mux.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(":8082", mux))
	}()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("[processor] Shutting down...")
		cancel()
	}()

	// Prepare insert statement
	insertSQL := `
		INSERT INTO articles (title, description, content, source, url, image_url, category, published_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			title       = VALUES(title),
			description = VALUES(description),
			content     = VALUES(content),
			image_url   = VALUES(image_url),
			category    = VALUES(category),
			published_at= VALUES(published_at)
	`

	log.Println("[processor] Starting consume loop...")
	for {
		fetches := client.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				log.Printf("[processor] Fetch error topic=%s partition=%d: %v", e.Topic, e.Partition, e.Err)
			}
		}

		fetches.EachRecord(func(record *kgo.Record) {
			var article Article
			if err := json.Unmarshal(record.Value, &article); err != nil {
				log.Printf("[processor] Error unmarshaling record: %v", err)
				return
			}

			// Skip empty URLs
			if article.URL == "" {
				return
			}

			// Truncate fields to fit DB columns
			if len(article.Title) > 512 {
				article.Title = article.Title[:512]
			}
			if len(article.Source) > 128 {
				article.Source = article.Source[:128]
			}
			if len(article.URL) > 1024 {
				article.URL = article.URL[:1024]
			}
			if len(article.ImageURL) > 1024 {
				article.ImageURL = article.ImageURL[:1024]
			}
			if len(article.Category) > 64 {
				article.Category = article.Category[:64]
			}

			// Parse published_at
			var pubTime time.Time
			if article.PublishedAt != "" {
				pubTime, err = time.Parse("2006-01-02 15:04:05", article.PublishedAt)
				if err != nil {
					pubTime = time.Now().UTC()
				}
			} else {
				pubTime = time.Now().UTC()
			}

			_, err := db.ExecContext(ctx, insertSQL,
				article.Title,
				article.Description,
				article.Content,
				article.Source,
				article.URL,
				article.ImageURL,
				article.Category,
				pubTime,
			)
			if err != nil {
				log.Printf("[processor] Error inserting article: %v", err)
				return
			}
			atomic.AddInt64(&processedCount, 1)
			articlesProcessedTotal.Inc()
		})
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
