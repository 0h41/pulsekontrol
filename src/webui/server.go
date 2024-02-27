package webui

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

//go:embed static
var staticFiles embed.FS

type WebUIServer struct {
	Addr           string
	upgrader       websocket.Upgrader
	clients        map[*websocket.Conn]bool
	broadcast      chan []byte
	configUpdateCh chan interface{}
}

func NewWebUIServer(addr string) *WebUIServer {
	return &WebUIServer{
		Addr: addr,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all connections for now
			},
		},
		clients:        make(map[*websocket.Conn]bool),
		broadcast:      make(chan []byte),
		configUpdateCh: make(chan interface{}),
	}
}

func (s *WebUIServer) Start() error {
	// Create a file system with just the static files
	staticFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return fmt.Errorf("failed to create static filesystem: %w", err)
	}

	// Setup HTTP server and routes
	http.Handle("/", http.FileServer(http.FS(staticFS)))
	http.HandleFunc("/ws", s.handleWebSocket)

	// Start WebSocket broadcasting
	go s.handleBroadcasts()

	// Start HTTP server
	log.Info().Msgf("Starting web server on %s", s.Addr)
	server := &http.Server{
		Addr:         s.Addr,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

func (s *WebUIServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upgrade to websocket")
		return
	}
	defer conn.Close()

	// Register new client
	s.clients[conn] = true
	log.Info().Msgf("New WebSocket client connected: %s", conn.RemoteAddr())

	// Send initial state
	initialMsg := []byte(`{"type":"welcome","message":"Connected to pulsekontrol"}`)
	err = conn.WriteMessage(websocket.TextMessage, initialMsg)
	if err != nil {
		log.Error().Err(err).Msg("Failed to send welcome message")
		delete(s.clients, conn)
		return
	}

	// Handle client messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Info().Msgf("WebSocket client disconnected: %s", conn.RemoteAddr())
			delete(s.clients, conn)
			break
		}

		// Process messages from client
		log.Debug().Msgf("Received message: %s", string(message))
		// TODO: Handle client commands
	}
}

func (s *WebUIServer) handleBroadcasts() {
	for {
		select {
		case message := <-s.broadcast:
			// Send to all connected clients
			for client := range s.clients {
				err := client.WriteMessage(websocket.TextMessage, message)
				if err != nil {
					log.Error().Err(err).Msg("Failed to send message to client")
					client.Close()
					delete(s.clients, client)
				}
			}
		case update := <-s.configUpdateCh:
			// Handle config updates
			log.Debug().Interface("update", update).Msg("Config updated, notifying clients")
			// TODO: Convert update to JSON and broadcast it
		}
	}
}

// BroadcastMessage sends a message to all connected clients
func (s *WebUIServer) BroadcastMessage(message []byte) {
	s.broadcast <- message
}

// NotifyConfigUpdate sends a config update to all connected clients
func (s *WebUIServer) NotifyConfigUpdate(update interface{}) {
	s.configUpdateCh <- update
}