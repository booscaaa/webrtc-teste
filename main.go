package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

// Client represents a single WebSocket connection
type Client struct {
	Name   string
	Socket *websocket.Conn
	Send   chan []byte
	Room   *Room
}

// Room represents a room where clients can join and communicate
type Room struct {
	Name    string
	Clients map[string]*Client
	Mutex   sync.Mutex
}

// Server maintains multiple rooms and their clients
type Server struct {
	Rooms map[string]*Room
	Mutex sync.Mutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for testing purposes; you might want to restrict this in production
	},
}

// Global server instance
var server = Server{
	Rooms: make(map[string]*Room),
}

// GetOrCreateRoom finds a room by name or creates a new one
func (s *Server) GetOrCreateRoom(roomName string) *Room {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()

	if room, exists := s.Rooms[roomName]; exists {
		return room
	}
	room := &Room{
		Name:    roomName,
		Clients: make(map[string]*Client),
	}
	s.Rooms[roomName] = room
	return room
}

// Broadcast sends a message to all clients in the room except the sender
func (r *Room) Broadcast(message []byte, exclude string) {
	r.Mutex.Lock()
	defer r.Mutex.Unlock()

	for name, client := range r.Clients {
		if name != exclude {
			client.Send <- message
		}
	}
}

// RemoveClient removes a client from the room
func (r *Room) RemoveClient(clientName string) {
	r.Mutex.Lock()
	defer r.Mutex.Unlock()
	delete(r.Clients, clientName)
}

// handleWebSocket manages incoming WebSocket connections
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	socket, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade:", err)
		return
	}

	// Read initial join message
	var initialMessage struct {
		Type string `json:"type"`
		Name string `json:"name"`
		Room string `json:"room"`
	}

	_, message, err := socket.ReadMessage()
	if err != nil {
		log.Println("ReadMessage:", err)
		socket.Close()
		return
	}

	err = json.Unmarshal(message, &initialMessage)
	if err != nil || initialMessage.Type != "join" {
		log.Println("Invalid join message:", err)
		socket.Close()
		return
	}

	// Create and register the new client
	client := &Client{
		Name:   initialMessage.Name,
		Socket: socket,
		Send:   make(chan []byte),
	}

	// Get or create the room and add the client to it
	room := server.GetOrCreateRoom(initialMessage.Room)
	client.Room = room

	room.Mutex.Lock()
	room.Clients[client.Name] = client
	room.Mutex.Unlock()

	// Start reading and writing messages for the client
	go client.writeMessages()
	go client.readMessages()
}

// readMessages listens for incoming messages from the client and routes them
func (c *Client) readMessages() {
	defer func() {
		c.Room.RemoveClient(c.Name)
		c.Socket.Close()
	}()

	for {
		_, message, err := c.Socket.ReadMessage()
		if err != nil {
			log.Println("ReadMessage:", err)
			break
		}

		// Parse the incoming message
		var data map[string]interface{}
		if err := json.Unmarshal(message, &data); err != nil {
			log.Println("Invalid message format:", err)
			continue
		}

		// Handle message types based on the parsed data
		if target, ok := data["target"].(string); ok && (data["type"] == "offer" || data["type"] == "answer" || data["type"] == "candidate") {
			// Send the message to a specific target within the same room
			if targetClient, exists := c.Room.Clients[target]; exists {
				targetClient.Send <- message
			}
		} else {
			// Broadcast the message to the room
			c.Room.Broadcast(message, c.Name)
		}
	}
}

// writeMessages sends outgoing messages from the client's send channel
func (c *Client) writeMessages() {
	defer c.Socket.Close()
	for message := range c.Send {
		if err := c.Socket.WriteMessage(websocket.TextMessage, message); err != nil {
			log.Println("WriteMessage:", err)
			break
		}
	}
}

// main initializes the server and routes
func main() {
	http.HandleFunc("/ws", handleWebSocket)
	log.Println("Starting WebSocket server on :3000")
	log.Fatal(http.ListenAndServe(":3000", nil))
}
