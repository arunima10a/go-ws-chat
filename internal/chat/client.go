package chat

import (
	"database/sql"
	"encoding/json"
	"html"
	"log"
	"log/slog"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

type Client struct {
	Hub      *Hub
	Conn     *websocket.Conn
	Send     chan []byte
	Username string
	Room     string
	Limiter  *rate.Limiter
}

func NewClient(h *Hub, conn *websocket.Conn, username string) *Client {
	client := &Client{
		Hub:      h,
		Conn:     conn,
		Send:     make(chan []byte, 256),
		Username: username,
		Room:     "general",
		Limiter:  rate.NewLimiter(rate.Every(time.Second/5), 10),
	}

	h.Register <- client

	return client
}

func (c *Client) ReadPump() {
	defer func() {
		c.Hub.Unregister <- c
		c.Conn.Close()
	}()
	c.Conn.SetReadLimit(1024)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, payload, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		if !c.Limiter.Allow() {
			slog.Warn("Rate limit exceeded", "user", c.Username)
			continue
		}

		var request Event
		if err := json.Unmarshal(payload, &request); err != nil {
			log.Printf("error parsing message: %v", err)
			continue
		}

		switch request.Type {

		case "join_room":
			var payload struct {
				NewRoom  string `json:"new_room"`
				Password string `json:"password"`
			}
			if err := json.Unmarshal(request.Payload, &payload); err != nil {
				continue
			}

			targetRoom := strings.TrimSpace(strings.ToLower(payload.NewRoom))

			if targetRoom != "general" {
				var dbHash string
				err := c.Hub.Database.QueryRow("SELECT password_hash FROM room_passwords WHERE room_name = $1", targetRoom).Scan(&dbHash)

				if err == sql.ErrNoRows {
					c.Send <- []byte(`{"type":"error", "payload":"Room '` + targetRoom + `' does not exist. Please create it first"}`)
					continue
				} else if err != nil {
					slog.Error("DB error", "error", err)
					continue
				}

				if bcrypt.CompareHashAndPassword([]byte(dbHash), []byte(payload.Password)) != nil {
					c.Send <- []byte(`{"type":"error", "payload":"Incorrect password for room: ` + payload.NewRoom + `"}`)
					continue
				}
			}

			oldRoom := c.Room
			c.Hub.JoinRoom(targetRoom, c)
			c.Room = targetRoom

			c.Hub.BroadcastUserList(oldRoom)
			c.Hub.BroadcastUserList(c.Room)

			c.Send <- []byte(`{"type":"room_joined", "payload":"` + payload.NewRoom + `"}`)


			go func(r string) {
				time.Sleep(100 * time.Millisecond)
				c.LoadHistoryForRoom(r)
			}(c.Room)

		case "create_room":
			var payload JoinRoomPayload
			json.Unmarshal(request.Payload, &payload)

			targetRoom := strings.TrimSpace(strings.ToLower(payload.NewRoom))

			if targetRoom == "general" {
				c.Send <- []byte(`{"type":"error", "payload":"'general' is a reserved public room."}`)
				continue
			}

			hashedPwd, _ := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)

			_, err := c.Hub.Database.Exec("INSERT INTO room_passwords (room_name, password_hash) VALUES ($1, $2)", targetRoom, string(hashedPwd))
			if err != nil {
				c.Send <- []byte(`{"type":"error", "payload":"Room already exists"}`)
				continue
			}
			c.Send <- []byte(`{"type":"system", "payload":"Room created with password"}`)

		case "send_message":
			var msgData map[string]interface{}
			if err := json.Unmarshal(request.Payload, &msgData); err != nil {
				continue
			}
			msgData["from"] = c.Username
			msgData["message"] = html.EscapeString(msgData["message"].(string))

			finalPayload, _ := json.Marshal(map[string]interface{}{
				"type":    "send_message",
				"payload": msgData,
			})

			c.Hub.Broadcast <- MessageEvent{
				Data: finalPayload,
				Room: c.Room,
			}
		case "typing":

			var typingData map[string]interface{}
			json.Unmarshal(request.Payload, &typingData)

			typingData["from"] = c.Username

			broadcastData, _ := json.Marshal(map[string]interface{}{
				"type":    "typing",
				"payload": typingData,
			})

			c.Hub.Broadcast <- MessageEvent{
				Data: broadcastData,
				Room: c.Room,
			}

		default:
			log.Printf("unknown event type: %s", request.Type)

		}
	}
}

func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) LoadHistoryForRoom(roomName string) {
	query := `SELECT username, content FROM messages WHERE room = $1 ORDER BY created_at DESC LIMIT 50`
	rows, err := c.Hub.Database.Query(query, roomName)
	if err != nil {
		log.Printf("Error fetching history: %v", err)
		return
	}
	defer rows.Close()
	type msgLog struct {
		Username string
		Content  string
	}
	var history []msgLog

	for rows.Next() {
		var m msgLog
		if err := rows.Scan(&m.Username, &m.Content); err == nil {
			history = append(history, m)
		}
	}

	for i := len(history)/2 - 1; i >= 0; i-- {
		opp := len(history) - 1 - i
		history[i], history[opp] = history[opp], history[i]
	}

	for _, m := range history {
		msg := map[string]interface{}{
			"type": "send_message",
			"payload": map[string]string{
				"from":    m.Username,
				"message": m.Content,
			},
		}
		data, _ := json.Marshal(msg)
		c.Send <- data
	}
}
