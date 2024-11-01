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
		log.Printf("Room '%s' found. Reusing existing room.", roomName)
		return room
	}
	room := &Room{
		Name:    roomName,
		Clients: make(map[string]*Client),
	}
	s.Rooms[roomName] = room
	log.Printf("Room '%s' created.", roomName)
	return room
}

// Broadcast sends a message to all clients in the room except the sender
func (r *Room) Broadcast(message []byte, exclude string) {
	r.Mutex.Lock()
	defer r.Mutex.Unlock()

	log.Printf("Broadcasting message in room '%s' from '%s'", r.Name, exclude)
	for name, client := range r.Clients {
		if name != exclude {
			select {
			case client.Send <- message:
				log.Printf("Message sent to client '%s' in room '%s'", name, r.Name)
			default:
				log.Printf("Send buffer full for client '%s' in room '%s'. Message dropped.", name, r.Name)
			}
		}
	}
}

// RemoveClient removes a client from the room
func (r *Room) RemoveClient(clientName string) {
	r.Mutex.Lock()
	defer r.Mutex.Unlock()
	delete(r.Clients, clientName)
	log.Printf("Client '%s' removed from room '%s'", clientName, r.Name)

	// Broadcast 'leave' message to others in the room
	leaveMessage := map[string]interface{}{
		"type": "leave",
		"name": clientName,
	}
	leaveJSON, _ := json.Marshal(leaveMessage)
	r.Broadcast(leaveJSON, "")
}

// handleWebSocket manages incoming WebSocket connections
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	log.Println("New WebSocket connection attempt")
	socket, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	log.Println("WebSocket connection established")

	// Read initial join message
	var initialMessage struct {
		Type string `json:"type"`
		Name string `json:"name"`
		Room string `json:"room"`
	}

	_, message, err := socket.ReadMessage()
	if err != nil {
		log.Println("ReadMessage error during initial join:", err)
		socket.Close()
		return
	}
	log.Printf("Initial message received: %s", message)

	err = json.Unmarshal(message, &initialMessage)
	if err != nil || initialMessage.Type != "join" {
		log.Println("Invalid join message format or type:", err)
		socket.Close()
		return
	}
	log.Printf("Client '%s' is joining room '%s'", initialMessage.Name, initialMessage.Room)

	// Create and register the new client
	client := &Client{
		Name:   initialMessage.Name,
		Socket: socket,
		Send:   make(chan []byte, 256), // Buffered channel to prevent blocking
	}

	// Get or create the room and add the client to it
	room := server.GetOrCreateRoom(initialMessage.Room)
	client.Room = room

	room.Mutex.Lock()
	room.Clients[client.Name] = client
	room.Mutex.Unlock()
	log.Printf("Client '%s' added to room '%s'", client.Name, room.Name)

	// Send user-list to the new client
	var userList []string
	room.Mutex.Lock()
	for name := range room.Clients {
		if name != client.Name {
			userList = append(userList, name)
		}
	}
	room.Mutex.Unlock()

	userListMessage := map[string]interface{}{
		"type":  "user-list",
		"users": userList,
	}
	userListJSON, _ := json.Marshal(userListMessage)
	client.Send <- userListJSON
	log.Printf("User list sent to client '%s' in room '%s'", client.Name, room.Name)

	// Broadcast new-user to other clients in the room
	newUserMessage := map[string]interface{}{
		"type": "new-user",
		"name": client.Name,
	}
	newUserJSON, _ := json.Marshal(newUserMessage)
	room.Broadcast(newUserJSON, client.Name)
	log.Printf("New user '%s' broadcasted in room '%s'", client.Name, room.Name)

	// Start reading and writing messages for the client
	go client.writeMessages()
	go client.readMessages()
}

// readMessages listens for incoming messages from the client and routes them
func (c *Client) readMessages() {
	defer func() {
		log.Printf("Client '%s' disconnected from room '%s'", c.Name, c.Room.Name)
		c.Room.RemoveClient(c.Name)
		c.Socket.Close()
		close(c.Send)
	}()

	for {
		_, message, err := c.Socket.ReadMessage()
		if err != nil {
			log.Println("ReadMessage error:", err)
			break
		}
		log.Printf("Message received from client '%s': %s", c.Name, message)

		// Parse the incoming message
		var data map[string]interface{}
		if err := json.Unmarshal(message, &data); err != nil {
			log.Println("Invalid message format from client:", err)
			continue
		}

		messageType, _ := data["type"].(string)

		// Handle message types based on the parsed data
		if target, ok := data["target"].(string); ok && (messageType == "offer" || messageType == "answer" || messageType == "candidate") {
			// Send the message to a specific target within the same room
			c.Room.Mutex.Lock()
			targetClient, exists := c.Room.Clients[target]
			c.Room.Mutex.Unlock()
			if exists {
				select {
				case targetClient.Send <- message:
					log.Printf("Message of type '%s' from '%s' forwarded to '%s' in room '%s'", messageType, c.Name, target, c.Room.Name)
				default:
					log.Printf("Send buffer full for client '%s'. Message dropped.", target)
				}
			} else {
				log.Printf("Target client '%s' not found in room '%s'", target, c.Room.Name)
			}
		} else {
			// Broadcast the message to the room
			log.Printf("Broadcasting message of type '%s' from '%s' in room '%s'", messageType, c.Name, c.Room.Name)
			c.Room.Broadcast(message, c.Name)
		}
	}
}

// writeMessages sends outgoing messages from the client's send channel
func (c *Client) writeMessages() {
	defer c.Socket.Close()
	for message := range c.Send {
		if err := c.Socket.WriteMessage(websocket.TextMessage, message); err != nil {
			log.Println("WriteMessage error:", err)
			break
		}
		log.Printf("Message sent to client '%s': %s", c.Name, message)
	}
}

// main initializes the server and routes
func main() {
	http.HandleFunc("/ws", handleWebSocket)
	log.Println("Starting WebSocket server on :3000")
	log.Fatal(http.ListenAndServe(":3000", nil))
}
