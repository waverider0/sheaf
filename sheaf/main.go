package main

import (
	"database/sql"
	_ "embed"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"sheaf/sheaf/db"
	"sheaf/sheaf/templates"
)

//go:embed db/schema.sql
var schemaSQL string

var queries *db.Queries

func main() {
	database, err := sql.Open("sqlite3", "file:sheaf.db?_busy_timeout=5000&_fk=1&_journal_mode=WAL")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	// SQLite allows one writer; a single connection avoids SQLITE_BUSY.
	database.SetMaxOpenConns(1)

	if _, err := database.Exec(schemaSQL); err != nil {
		log.Fatalf("init schema: %v", err)
	}
	queries = db.New(database)
	defer database.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", renderIndex)
	mux.HandleFunc("POST /search", handleSearch)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func renderIndex(w http.ResponseWriter, r *http.Request) {
	if err := templates.IndexPage().Render(r.Context(), w); err != nil {
		log.Printf("render index: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	searchQuery := strings.TrimSpace(r.FormValue("q"))
	if searchQuery == "" {
		http.Error(w, "empty query", http.StatusBadRequest)
		return
	}

	// the DB CHECK is the backstop
	const searchQueryLimit = 200
	if runes := []rune(searchQuery); len(runes) > searchQueryLimit {
		searchQuery = string(runes[:searchQueryLimit])
	}

	if err := queries.InsertSearch(r.Context(), db.InsertSearchParams{
		Query:           searchQuery,
		CreatedAtUnixMs: time.Now().UnixMilli(),
	}); err != nil {
		log.Printf("insert search: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	log.Printf("search: %s", searchQuery)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
