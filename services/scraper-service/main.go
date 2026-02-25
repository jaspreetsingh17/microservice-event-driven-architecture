package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Article is the event payload published to RedPanda.
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

// Feed defines an RSS source.
type Feed struct {
	Name     string
	URL      string
	Category string
}

var (
	articlesScrapedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scraper_articles_scraped_total",
		Help: "Total number of articles scraped and published to RedPanda.",
	}, []string{"feed", "category"})
)

var feeds = []Feed{
	{Name: "BBC News", URL: "http://feeds.bbci.co.uk/news/rss.xml", Category: "general"},
	{Name: "The Guardian", URL: "https://www.theguardian.com/world/rss", Category: "world"},
	{Name: "Al Jazeera", URL: "https://www.aljazeera.com/xml/rss/all.xml", Category: "world"},
	{Name: "NPR News", URL: "https://feeds.npr.org/1001/rss.xml", Category: "general"},
	{Name: "TechCrunch", URL: "https://techcrunch.com/feed/", Category: "technology"},
	{Name: "ESPN", URL: "https://www.espn.com/espn/rss/news", Category: "sports"},
	{Name: "BBC Science", URL: "http://feeds.bbci.co.uk/news/science_and_environment/rss.xml", Category: "science"},
	{Name: "BBC Business", URL: "http://feeds.bbci.co.uk/news/business/rss.xml", Category: "business"},
	{Name: "BBC Health", URL: "http://feeds.bbci.co.uk/news/health/rss.xml", Category: "health"},
}

func main() {
	brokers := getEnv("REDPANDA_BROKERS", "localhost:9092")
	intervalSec := getEnvInt("SCRAPE_INTERVAL", 120)

	log.Printf("[scraper] Starting scraper-service | brokers=%s interval=%ds", brokers, intervalSec)

	// Kafka producer via franz-go
	client, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(brokers, ",")...),
		kgo.DefaultProduceTopic("raw-articles"),
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
	)
	if err != nil {
		log.Fatalf("[scraper] Failed to create Kafka client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Health endpoint
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","service":"scraper"}`))
		})
		mux.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(":8081", mux))
	}()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Scrape immediately on startup, then on interval
	scrapeAll(ctx, client)

	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			scrapeAll(ctx, client)
		case <-sigCh:
			log.Println("[scraper] Shutting down...")
			cancel()
			return
		}
	}
}

func scrapeAll(ctx context.Context, client *kgo.Client) {
	log.Println("[scraper] Starting scrape cycle...")
	var wg sync.WaitGroup
	parser := gofeed.NewParser()
	parser.UserAgent = "NewsAggregator/1.0"

	var totalArticles int
	var mu sync.Mutex

	for _, f := range feeds {
		wg.Add(1)
		go func(feed Feed) {
			defer wg.Done()
			count := scrapeFeed(ctx, client, parser, feed)
			mu.Lock()
			totalArticles += count
			mu.Unlock()
		}(f)
	}
	wg.Wait()
	log.Printf("[scraper] Scrape cycle complete: published %d articles", totalArticles)
}

func scrapeFeed(ctx context.Context, client *kgo.Client, parser *gofeed.Parser, feed Feed) int {
	ctxTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	parsedFeed, err := parser.ParseURLWithContext(feed.URL, ctxTimeout)
	if err != nil {
		log.Printf("[scraper] Error parsing %s: %v", feed.Name, err)
		return 0
	}

	count := 0
	for _, item := range parsedFeed.Items {
		article := Article{
			Title:       item.Title,
			Description: item.Description,
			Content:     item.Content,
			Source:      feed.Name,
			URL:         item.Link,
			Category:    feed.Category,
		}

		// Extract image
		if item.Image != nil {
			article.ImageURL = item.Image.URL
		} else if len(item.Enclosures) > 0 {
			for _, enc := range item.Enclosures {
				if strings.HasPrefix(enc.Type, "image") {
					article.ImageURL = enc.URL
					break
				}
			}
		}

		// Parse published time
		if item.PublishedParsed != nil {
			article.PublishedAt = item.PublishedParsed.UTC().Format("2006-01-02 15:04:05")
		} else if item.UpdatedParsed != nil {
			article.PublishedAt = item.UpdatedParsed.UTC().Format("2006-01-02 15:04:05")
		} else {
			article.PublishedAt = time.Now().UTC().Format("2006-01-02 15:04:05")
		}

		data, err := json.Marshal(article)
		if err != nil {
			log.Printf("[scraper] Error marshaling article: %v", err)
			continue
		}

		record := &kgo.Record{
			Key:   []byte(article.URL),
			Value: data,
		}
		client.Produce(ctx, record, func(r *kgo.Record, err error) {
			if err != nil {
				log.Printf("[scraper] Error producing to RedPanda: %v", err)
			} else {
				articlesScrapedTotal.WithLabelValues(feed.Name, feed.Category).Inc()
			}
		})
		count++
	}

	log.Printf("[scraper] %s: published %d articles", feed.Name, count)
	return count
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Printf("[scraper] Warning: invalid %s=%s, using default %d\n", key, v, fallback)
		return fallback
	}
	return n
}
