package api

import (
	"net/http"

	"github.com/openshift/jira-agent-dashboard/internal/db"
)

// Server is the HTTP server for the dashboard API.
type Server struct {
	store *db.Store
	mux   *http.ServeMux
}

// NewServer creates a new Server with the given store and web directory.
func NewServer(store *db.Store, webDir string) *Server {
	s := &Server{store: store, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/trends", s.handleGetTrends)
	s.mux.HandleFunc("GET /api/issues", s.handleGetIssues)
	s.mux.HandleFunc("GET /api/issues/{id}", s.handleGetIssueDetail)
	s.mux.HandleFunc("GET /api/comments/summary", s.handleGetCommentsSummary)
	s.mux.HandleFunc("GET /api/comments/{issueID}", s.handleGetComments)
	s.mux.HandleFunc("PATCH /api/comments/{id}", s.handlePatchComment)
	s.mux.HandleFunc("GET /api/scraper-status", s.handleGetScraperStatus)
	fs := http.FileServer(http.Dir(webDir))
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		fs.ServeHTTP(w, r)
	})
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
