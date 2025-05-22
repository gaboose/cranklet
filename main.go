package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
)

type SharedDocuments struct {
	inner map[string]*SharedDocument
	mu    sync.Mutex
}

type SharedDocument struct {
	inner *Document
	mu    sync.Mutex
}

type Server struct {
	documents *SharedDocuments
	db        *DB
}

func NewServer() (*Server, error) {
	db, err := NewDB(path.Join(*data, "db.sqlite3"))
	if err != nil {
		return nil, err
	}

	return &Server{
		documents: &SharedDocuments{
			inner: make(map[string]*SharedDocument),
		},
		db: db,
	}, nil
}

func (s *Server) document(name string) (*SharedDocument, error) {
	filename, err := s.db.DocumentFilename(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get document path: %w", err)
	}

	s.documents.mu.Lock()
	doc, ok := s.documents.inner[name]
	if !ok {
		doc = &SharedDocument{inner: nil}
		s.documents.inner[name] = doc

		// Release s.documents.mu quickly, but hold on to doc.mu while we restore over
		// network.
		doc.mu.Lock()
		defer doc.mu.Unlock()
		s.documents.mu.Unlock()

		if len(*restore) > 0 {
			cmd := exec.Command("sh", "-c", *restore+" "+*data+" "+filename)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return nil, fmt.Errorf("failed to restore document: %w", err)
			}
		}

		if len(*replicate) > 0 {
			cmd := exec.Command("sh", "-c", *replicate+" "+*data+" "+filename)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			go cmd.Run()
		}

		innerDoc, err := NewDocument(path.Join(*data, filename))
		if err != nil {
			return nil, fmt.Errorf("failed to create document: %w", err)
		}

		doc.inner = innerDoc
	} else {
		s.documents.mu.Unlock()
	}

	return doc, nil
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Allow all origins for testing (consider restricting in production)
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) wsHandler(w http.ResponseWriter, r *http.Request) {
	docName := r.URL.Path[1:]
	afterParam := r.URL.Query().Get("after")

	if afterParam == "" {
		http.Error(w, "Missing after parameter", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket upgrade error:", err)
		return
	}
	defer conn.Close()

	updateHandler := func(v Vertex) bool {
		if err := conn.WriteJSON(v); err != nil {
			log.Printf("ws write error: %v, closing connection", err)
			conn.Close()
			return false
		}

		return true
	}

	doc, err := s.document(docName)
	if err != nil {
		http.Error(w, "Failed to get document", http.StatusInternalServerError)
		log.Println(err)
		return
	}

	doc.mu.Lock()
	vertices, err := doc.inner.Get(strings.Split(afterParam, ","))
	if err != nil {
		doc.mu.Unlock()
		log.Println(err)
		return
	}
	for _, v := range vertices {
		if !updateHandler(v) {
			doc.mu.Unlock()
			return
		}
	}
	doc.inner.Subscribe(&updateHandler)
	doc.mu.Unlock()

	defer func() {
		doc.mu.Lock()
		doc.inner.Unsubscribe(&updateHandler)
		doc.mu.Unlock()
	}()

	for {
		if _, _, err := conn.NextReader(); err != nil {
			break
		}
	}
}

func (s *Server) writeHandler(w http.ResponseWriter, r *http.Request) {
	docName := r.URL.Path[1:]

	var v Vertex
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	doc, err := s.document(docName)
	if err != nil {
		http.Error(w, "Failed to get document", http.StatusInternalServerError)
		log.Println(err)
		return
	}

	doc.mu.Lock()
	if err := doc.inner.Post(v); err != nil {
		doc.mu.Unlock()
		http.Error(w, "Failed to write to document", http.StatusInternalServerError)
		log.Println(err)
		return
	}
	doc.mu.Unlock()

	w.WriteHeader(http.StatusCreated)
}

func (s *Server) readHandler(w http.ResponseWriter, r *http.Request) {
	docName := r.URL.Path[1:]
	afterParam := r.URL.Query().Get("after")

	if afterParam == "" {
		http.Error(w, "Missing after parameter", http.StatusBadRequest)
		return
	}

	doc, err := s.document(docName)
	if err != nil {
		http.Error(w, "Failed to get document", http.StatusInternalServerError)
		log.Println(err)
		return
	}

	doc.mu.Lock()
	result, err := doc.inner.Get(strings.Split(afterParam, ","))
	if err != nil {
		doc.mu.Unlock()
		http.Error(w, "Failed to read from document", http.StatusInternalServerError)
		log.Println(err)
		return
	}
	doc.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

var (
	data      = flag.String("data", "./data", "Data directory")
	restore   = flag.String("restore", "", "Restore command")
	replicate = flag.String("replicate", "", "Replicate command")
)

func main() {
	flag.Parse()

	var err error
	*data, err = filepath.Abs(*data)
	if err != nil {
		log.Fatal(err)
	}

	if len(*restore) > 0 {
		cmd := exec.Command("sh", "-c", *restore+" "+*data+" "+"db.sqlite3")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatal(err)
		}
	}

	if len(*replicate) > 0 {
		cmd := exec.Command("sh", "-c", *replicate+" "+*data+" "+"db.sqlite3")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		go cmd.Run()
	}

	s, err := NewServer()
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			s.wsHandler(w, r)
		} else if r.Method == http.MethodPost {
			s.writeHandler(w, r)
		} else if r.Method == http.MethodGet {
			s.readHandler(w, r)
		} else {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

	log.Println("Server is running on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}
