package chat

import "encoding/json"

type Event struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type NewMessagPayload struct {
	Message string `json:"message"`
	From    string `json:"from"`
	Sent    string `json:"sent"`
}

type MessageEvent struct {
	Data []byte `json:"data"`
	Room string `json:"room"`
}

type JoinRoomPayload struct {
	NewRoom  string `json:"new_room"`
	Password string `json:"password"`
}
