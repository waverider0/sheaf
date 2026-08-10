// sheaf: serves the landing page — a SHEAF logo with a search bar.
// Submitting a search inserts it into a SQLite database; the page renders
// the search list server-side from that database.
package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"sheaf/templates"
)

func searchHistory() []templates.Search {
	rows, err := db.Query(
		`SELECT query, created_at_unix_ms FROM searches
		 ORDER BY created_at_unix_ms DESC, id DESC`,
	)
	if err != nil {
		log.Printf("query searches: %v", err)
		return nil
	}
	defer rows.Close()
	var queries []templates.Search
	for rows.Next() {
		var q string
		var ts int64
		if err := rows.Scan(&q, &ts); err != nil {
			log.Printf("scan search: %v", err)
			return nil
		}
		queries = append(queries, templates.Search{
			Query: q,
			When:  time.UnixMilli(ts).Local().Format("2006-01-02 15:04"),
		})
	}
	if err := rows.Err(); err != nil {
		log.Printf("iterate searches: %v", err)
		return nil
	}
	return queries
}

func renderIndex(w http.ResponseWriter, r *http.Request) {
	if err := templates.IndexPage(searchHistory()).Render(r.Context(), w); err != nil {
		log.Printf("render index: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("q"))
	if q == "" {
		http.Error(w, "empty query", http.StatusBadRequest)
		return
	}
	// cap at 200 chars; the DB CHECK is the backstop
	if r := []rune(q); len(r) > 200 {
		q = string(r[:200])
	}
	if _, err := db.Exec(
		`INSERT INTO searches (query, created_at_unix_ms) VALUES (?, ?)`,
		q, time.Now().UnixMilli(),
	); err != nil {
		log.Printf("insert search: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// htmx: return the fresh list for an in-place swap. Plain-form fallback:
	// redirect so the updated list renders from the database and a refresh
	// can't re-submit the search.
	if r.Header.Get("HX-Request") != "" {
		if err := templates.SearchList(searchHistory()).Render(r.Context(), w); err != nil {
			log.Printf("render search list: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func main() {
	initDB(dbPath())
	defer db.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", renderIndex)
	mux.HandleFunc("POST /search", handleSearch)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
