package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/OpenRealmCn/OpenIPShift/internal/adapters"
	"github.com/OpenRealmCn/OpenIPShift/internal/domain"
)

func main() {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if os.Getenv("ALLOW_NON_LOOPBACK") != "1" && !strings.HasPrefix(addr, "127.") && addr != "localhost:8080" {
		log.Fatal("refusing non-loopback LISTEN_ADDR; set ALLOW_NON_LOOPBACK=1 only behind an authenticated trusted proxy")
	}
	nodes := []domain.Node{}
	p := adapters.NewMockProvider()
	d := adapters.NewMockDNS()
	v := adapters.MockVerifier{}
	state := os.Getenv("STATE_PATH")
	if state == "" {
		state = "./data/state.json"
	}
	e, err := domain.New(state, nodes, p, d, v, 30*time.Minute)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "mode": "mock"})
	})
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, _ *http.Request) { json.NewEncoder(w).Encode(e.Snapshot()) })
	mux.HandleFunc("/api/rotations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+os.Getenv("ROTATOR_API_TOKEN") || os.Getenv("ROTATOR_API_TOKEN") == "" {
			http.Error(w, "unauthorized", 401)
			return
		}
		var in struct {
			NodeID string `json:"nodeId"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid json", 400)
			return
		}
		t, err := e.Create(in.NodeID, r.Header.Get("Idempotency-Key"))
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		go func() {
			if err := e.Run(context.Background(), t.ID); err != nil {
				log.Printf("rotation %s: %v", t.ID, err)
			}
		}()
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(t)
	})
	mux.HandleFunc("/api/tick", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+os.Getenv("ROTATOR_API_TOKEN") || os.Getenv("ROTATOR_API_TOKEN") == "" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if err := e.Tick(r.Context()); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		json.NewEncoder(w).Encode(e.Snapshot())
	})
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		s := e.Snapshot()
		if t := s.Tasks[id]; t != nil {
			json.NewEncoder(w).Encode(t)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		var n domain.Node
		switch r.Method {
		case "POST":
			if json.NewDecoder(r.Body).Decode(&n) != nil {
				http.Error(w, "invalid json", 400)
				return
			}
			if err := e.AddNode(n); err != nil {
				http.Error(w, err.Error(), status(err))
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(n)
		case "GET":
			json.NewEncoder(w).Encode(e.Snapshot().Nodes)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/nodes/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/nodes/")
		if r.Method != "PUT" && r.Method != "DELETE" {
			http.Error(w, "method not allowed", 405)
			return
		}
		var err error
		if r.Method == "DELETE" {
			err = e.DeleteNode(id)
		} else {
			var n domain.Node
			if json.NewDecoder(r.Body).Decode(&n) != nil {
				http.Error(w, "invalid json", 400)
				return
			}
			err = e.UpdateNode(id, n)
		}
		if err != nil {
			http.Error(w, err.Error(), status(err))
			return
		}
		json.NewEncoder(w).Encode(e.Snapshot())
	})
	handler := logging(cors(auth(mux)))
	log.Printf("listening on %s (mock mode)", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:3000")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,Idempotency-Key")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func status(err error) int {
	if errors.Is(err, domain.ErrBusy) || errors.Is(err, domain.ErrDuplicateNode) {
		return 409
	}
	if errors.Is(err, domain.ErrNodeNotFound) {
		return 404
	}
	return 400
}
func auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv("ROTATOR_API_TOKEN")
		if token == "" || r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}
