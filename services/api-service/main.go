package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

//go:embed static/*
var staticFiles embed.FS

// Article represents a stored news article.
type Article struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Source      string `json:"source"`
	URL         string `json:"url"`
	ImageURL    string `json:"image_url"`
	Category    string `json:"category"`
	PublishedAt string `json:"published_at"`
	CreatedAt   string `json:"created_at"`
}

type ArticlesResponse struct {
	Articles   []Article `json:"articles"`
	Total      int       `json:"total"`
	Page       int       `json:"page"`
	PerPage    int       `json:"per_page"`
	TotalPages int       `json:"total_pages"`
}

var (
	db *sql.DB

	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests.",
	}, []string{"path", "method"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duration of HTTP requests.",
		Buckets: prometheus.DefBuckets,
	}, []string{"path", "method"})
)

func main() {
	dsn := getEnv("MYSQL_DSN", "newsuser:newspass@tcp(localhost:3306)/newsdb?parseTime=true&charset=utf8mb4")
	port := getEnv("PORT", "8080")

	// Connect to MySQL with retries
	var err error
	for i := 0; i < 30; i++ {
		db, err = sql.Open("mysql", dsn)
		if err == nil {
			err = db.Ping()
		}
		if err == nil {
			break
		}
		log.Printf("[api] Waiting for MySQL... attempt %d: %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("[api] Failed to connect to MySQL: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	log.Printf("[api] Connected to MySQL, starting server on :%s", port)

	mux := http.NewServeMux()

	// ── API routes under /user/api/ ─────────────────────────────
	mux.HandleFunc("/user/api/health", handleHealth)
	mux.HandleFunc("/user/api/articles", handleArticles)
	mux.HandleFunc("/user/api/articles/", handleArticleByID)
	mux.HandleFunc("/user/api/categories", handleCategories)
	mux.HandleFunc("/user/api/sources", handleSources)
	mux.HandleFunc("/user/api/stats", handleStats)
	mux.Handle("/metrics", promhttp.Handler())

	// ── Serve frontend static files under /user/ ────────────────
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("[api] Failed to load static files: %v", err)
	}
	fileServer := http.FileServer(http.FS(staticFS))
	mux.Handle("/user/", http.StripPrefix("/user/", fileServer))

	// ── Health check at root (for internal health checks) ───────
	mux.HandleFunc("/health", handleHealth)

	// CORS middleware
	handler := corsMiddleware(mux)

	log.Fatal(http.ListenAndServe(":"+port, handler))
}

// ── Middleware ──────────────────────────────────────────────────

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		method := r.Method

		timer := prometheus.NewTimer(httpRequestDuration.WithLabelValues(path, method))
		defer timer.ObserveDuration()

		httpRequestsTotal.WithLabelValues(path, method).Inc()

		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Handlers ───────────────────────────────────────────────────

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var count int
	db.QueryRow("SELECT COUNT(*) FROM articles").Scan(&count)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":         "ok",
		"service":        "api",
		"total_articles": count,
	})
}

func handleArticles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}

	category := strings.TrimSpace(r.URL.Query().Get("category"))
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	search := strings.TrimSpace(r.URL.Query().Get("search"))

	offset := (page - 1) * perPage

	where := []string{"1=1"}
	args := []interface{}{}

	if category != "" && category != "all" {
		where = append(where, "category = ?")
		args = append(args, category)
	}
	if source != "" {
		where = append(where, "source = ?")
		args = append(args, source)
	}
	if search != "" {
		where = append(where, "(title LIKE ? OR description LIKE ?)")
		searchTerm := "%" + search + "%"
		args = append(args, searchTerm, searchTerm)
	}

	whereClause := strings.Join(where, " AND ")

	var total int
	countQuery := "SELECT COUNT(*) FROM articles WHERE " + whereClause
	if err := db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		log.Printf("[api] Count error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	query := "SELECT id, title, description, content, source, url, image_url, category, published_at, created_at FROM articles WHERE " + whereClause + " ORDER BY published_at DESC LIMIT ? OFFSET ?"
	queryArgs := append(args, perPage, offset)

	rows, err := db.Query(query, queryArgs...)
	if err != nil {
		log.Printf("[api] Query error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	articles := make([]Article, 0)
	for rows.Next() {
		var a Article
		var pubTime, createdTime time.Time
		var desc, content sql.NullString
		if err := rows.Scan(&a.ID, &a.Title, &desc, &content, &a.Source, &a.URL, &a.ImageURL, &a.Category, &pubTime, &createdTime); err != nil {
			log.Printf("[api] Scan error: %v", err)
			continue
		}
		a.Description = desc.String
		a.Content = content.String
		a.PublishedAt = pubTime.Format(time.RFC3339)
		a.CreatedAt = createdTime.Format(time.RFC3339)
		articles = append(articles, a)
	}

	totalPages := total / perPage
	if total%perPage != 0 {
		totalPages++
	}

	json.NewEncoder(w).Encode(ArticlesResponse{
		Articles:   articles,
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
	})
}

func handleArticleByID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/user/api/articles/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, `{"error":"article ID required"}`, http.StatusBadRequest)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid article ID"}`, http.StatusBadRequest)
		return
	}

	var a Article
	var pubTime, createdTime time.Time
	var desc, content sql.NullString
	err = db.QueryRow("SELECT id, title, description, content, source, url, image_url, category, published_at, created_at FROM articles WHERE id = ?", id).
		Scan(&a.ID, &a.Title, &desc, &content, &a.Source, &a.URL, &a.ImageURL, &a.Category, &pubTime, &createdTime)

	if err == sql.ErrNoRows {
		http.Error(w, `{"error":"article not found"}`, http.StatusNotFound)
		return
	} else if err != nil {
		log.Printf("[api] Query error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	a.Description = desc.String
	a.Content = content.String
	a.PublishedAt = pubTime.Format(time.RFC3339)
	a.CreatedAt = createdTime.Format(time.RFC3339)

	json.NewEncoder(w).Encode(a)
}

func handleCategories(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	rows, err := db.Query("SELECT category, COUNT(*) as count FROM articles GROUP BY category ORDER BY count DESC")
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Category struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	categories := []Category{}
	for rows.Next() {
		var c Category
		rows.Scan(&c.Name, &c.Count)
		categories = append(categories, c)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"categories": categories,
	})
}

func handleSources(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	rows, err := db.Query("SELECT source, COUNT(*) as count FROM articles GROUP BY source ORDER BY count DESC")
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Source struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	sources := []Source{}
	for rows.Next() {
		var s Source
		rows.Scan(&s.Name, &s.Count)
		sources = append(sources, s)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"sources": sources,
	})
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var totalArticles, totalSources, totalCategories int
	db.QueryRow("SELECT COUNT(*) FROM articles").Scan(&totalArticles)
	db.QueryRow("SELECT COUNT(DISTINCT source) FROM articles").Scan(&totalSources)
	db.QueryRow("SELECT COUNT(DISTINCT category) FROM articles").Scan(&totalCategories)

	var latestTime time.Time
	db.QueryRow("SELECT MAX(published_at) FROM articles").Scan(&latestTime)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_articles":   totalArticles,
		"total_sources":    totalSources,
		"total_categories": totalCategories,
		"latest_article":   latestTime.Format(time.RFC3339),
	})
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
