package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

type Hub struct {
	Rooms      map[string]map[*Client]bool
	Broadcast  chan MessageEvent
	Register   chan *Client
	Unregister chan *Client
	Redis      *redis.Client
	Ctx        context.Context
	Database   *sql.DB
}

func NewHub(rdb *redis.Client, db *sql.DB) *Hub {
	return &Hub{
		Broadcast:  make(chan MessageEvent),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Rooms:      make(map[string]map[*Client]bool),
		Redis:      rdb,
		Database:   db,
		Ctx:        context.Background(),
	}
}

func (h *Hub) addClientToRoom(roomName string, client *Client) {
	if h.Rooms[roomName] == nil {
		h.Rooms[roomName] = make(map[*Client]bool)
	}
	h.Rooms[roomName][client] = true
	log.Printf("client joined room: %s", roomName)
}

func (h *Hub) removeClient(client *Client) {
	for roomName, clients := range h.Rooms {
		if _, ok := clients[client]; ok {
			delete(clients, client)
			if len(clients) == 0 {
				delete(h.Rooms, roomName)
			}
		}
	}
	close(client.Send)
}
func (h *Hub) BroadcastUserList(roomName string) {

	redisKey := "presence:" + roomName
	users, err := h.Redis.SMembers(h.Ctx, redisKey).Result()
	if err != nil {
		slog.Error("Redis presence error", "error", err)
		return
	}


	data, err := json.Marshal(map[string]interface{}{
		"type":    "user_list",
		"payload": users,
	})

	if err != nil {
		slog.Error("Failed to marshal user list", "error", err)
		return
	}

	event := MessageEvent{
		Data: data,
		Room: roomName,
	}
	payload, _ := json.Marshal(event)
	h.Redis.Publish(h.Ctx, "chat_channel", payload)

}

func (h *Hub) Run() {

	go h.listenToRedis()

	for {
		select {
		case client := <-h.Register:
			h.addClientToRoom("general", client)
			h.Redis.SAdd(h.Ctx, "presence:general", client.Username)
			h.BroadcastUserList("general")

		case client := <-h.Unregister:
			h.Redis.SRem(h.Ctx, "presence:"+client.Room, client.Username)
			h.removeClient(client)
			h.BroadcastUserList(client.Room)

		case event := <-h.Broadcast:
			payload, _ := json.Marshal(event)
			h.Redis.Publish(h.Ctx, "chat_channel", payload)

			go h.SaveMessage(event)
		}
	}
}

func (h *Hub) listenToRedis() {
	pubsub := h.Redis.Subscribe(h.Ctx, "chat_channel")
	defer pubsub.Close()

	ch := pubsub.Channel()

	for msg := range ch {
		var event MessageEvent

		json.Unmarshal([]byte(msg.Payload), &event)

		// DEBUG
		slog.Info("Redis received", "room", event.Room, "data_len", len(event.Data))

		for client := range h.Rooms[event.Room] {
			select {
			case client.Send <- event.Data:
			default:
				h.removeClient(client)
			}
		}
	}

}
func (h *Hub) GetOnlineUsers() []string {
	var users []string

	for _, clients := range h.Rooms {
		for clients := range clients {
			users = append(users, clients.Username)
		}

	}
	return users

}
func (h *Hub) SaveMessage(event MessageEvent) {
	if h.Database == nil {
		slog.Error("Database connection is nil in Hub")
		return
	}

	var e Event
	if err := json.Unmarshal(event.Data, &e); err != nil {
		return
	}

	if e.Type == "send_message" {
		var payload map[string]interface{}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			slog.Error("SaveMessage: Failed to unmarshal envelope", "error", err)
			return
		}

		query := `INSERT INTO messages (username, content, room) VALUES ($1, $2, $3)`

		_, err := h.Database.Exec(query, payload["from"], payload["message"], event.Room)
		if err != nil {
			slog.Error("Database error", "error", err)
		} else {
			slog.Info("Message saved to database", "user", payload["from"])
		}
	}

}

func (h *Hub) JoinRoom(roomName string, client *Client) {
	h.Redis.SRem(h.Ctx, "presence:"+client.Room, client.Username)
	h.removeClientFromAllRooms(client)

	if h.Rooms[roomName] == nil {
		h.Rooms[roomName] = make(map[*Client]bool)
	}
	h.Rooms[roomName][client] = true

	h.Redis.SAdd(h.Ctx, "presence:"+roomName, client.Username)

}

func (h *Hub) removeClientFromAllRooms(client *Client) {
	for roomName, clients := range h.Rooms {
		if _, ok := clients[client]; ok {
			delete(clients, client)
			if len(clients) == 0 {
				delete(h.Rooms, roomName)
			}
		}
	}
}
