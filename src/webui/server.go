package webui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"time"

	"github.com/0h41/pulsekontrol/src/pulseaudio"
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
	paClient       *pulseaudio.PAClient
	stopChan       chan struct{}
}

func NewWebUIServer(addr string, paClient *pulseaudio.PAClient) *WebUIServer {
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
		paClient:       paClient,
		stopChan:       make(chan struct{}),
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

	// Start audio sources monitoring
	go s.monitorAudioSources()

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
		
		// Parse the message
		var clientMsg map[string]interface{}
		if err := json.Unmarshal(message, &clientMsg); err != nil {
			log.Error().Err(err).Msg("Failed to parse client message")
			continue
		}
		
		// Handle based on message type
		msgType, ok := clientMsg["type"].(string)
		if !ok {
			log.Error().Msg("Message missing 'type' field")
			continue
		}
		
		switch msgType {
		case "getState":
			// Client is requesting initial state
			// Currently handled by monitorAudioSources periodic updates
			
		case "setVolume":
			// Client wants to change volume
			sourceId, ok := clientMsg["sourceId"].(string)
			if !ok {
				log.Error().Msg("setVolume missing sourceId")
				continue
			}
			
			volumeFloat, ok := clientMsg["volume"].(float64)
			if !ok {
				log.Error().Msg("setVolume missing volume or not a number")
				continue
			}
			
			volume := int(volumeFloat)
			log.Debug().Str("sourceId", sourceId).Int("volume", volume).Msg("Setting volume")
			
			// TODO: Implement volume change via pulseaudio client
			
		case "assignControl":
			// Client wants to assign a source to a control
			controlId, ok := clientMsg["controlId"].(string)
			if !ok {
				log.Error().Msg("assignControl missing controlId")
				continue
			}
			
			controlType, ok := clientMsg["controlType"].(string)
			if !ok {
				log.Error().Msg("assignControl missing controlType")
				continue
			}
			
			sourceId, ok := clientMsg["sourceId"].(string)
			if !ok {
				log.Error().Msg("assignControl missing sourceId")
				continue
			}
			
			log.Debug().
				Str("controlId", controlId).
				Str("controlType", controlType).
				Str("sourceId", sourceId).
				Msg("Assigning source to control")
			
			// TODO: Update configuration to add this assignment
			
		case "unassignControl":
			// Client wants to remove a source from a control
			controlId, ok := clientMsg["controlId"].(string)
			if !ok {
				log.Error().Msg("unassignControl missing controlId")
				continue
			}
			
			controlType, ok := clientMsg["controlType"].(string)
			if !ok {
				log.Error().Msg("unassignControl missing controlType")
				continue
			}
			
			sourceId, ok := clientMsg["sourceId"].(string)
			if !ok {
				log.Error().Msg("unassignControl missing sourceId")
				continue
			}
			
			log.Debug().
				Str("controlId", controlId).
				Str("controlType", controlType).
				Str("sourceId", sourceId).
				Msg("Removing source from control")
			
			// TODO: Update configuration to remove this assignment
			
		default:
			log.Debug().Str("type", msgType).Msg("Unknown message type")
		}
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
		case <-s.stopChan:
			return
		}
	}
}

// monitorAudioSources periodically fetches audio sources and broadcasts them to clients
func (s *WebUIServer) monitorAudioSources() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Get audio sources
			sources := s.paClient.GetAudioSources()
			
			// Create message
			message := map[string]interface{}{
				"type":    "audioSourcesUpdate",
				"sources": sources,
			}
			
			// Convert to JSON
			jsonData, err := json.Marshal(message)
			if err != nil {
				log.Error().Err(err).Msg("Failed to marshal audio sources")
				continue
			}
			
			// Broadcast to clients
			s.broadcast <- jsonData
		case <-s.stopChan:
			return
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