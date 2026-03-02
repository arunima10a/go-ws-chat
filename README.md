# Distributed Real-Time Messaging Engine (Go + Redis)

A high-performance WebSocket orchestrator capable of handling stateful bi-directional communication across multiple server instances.

## Key Technical Solutions
- **Horizontal Scalability:** Solved the "Stateful WebSocket" problem using **Redis Pub/Sub** to synchronize messages across disparate server nodes.
- **Distributed Presence:** Implemented a global "Online Users" list using **Redis Sets**, ensuring real-time sidebar synchronization across all active tabs and server instances.
- **Deadlock Prevention:** Architected a non-blocking Hub pattern that bypasses internal Go channels for high-frequency state updates, preventing system freezes during high-volume room transitions.
- **Persistence & Replay:** Engineered a **PostgreSQL** storage layer with chronological reversal logic, allowing users to instantly load the latest 50 messages upon joining a room.
- **Access Control:** Secured private rooms using **Bcrypt-hashed passwords** and server-side gatekeepers to validate credentials before updating server state.
- **Defensive Design:** Integrated **Token-Bucket Rate Limiting**, XSS sanitization, and JWT-based handshakes to protect against DoS and identity spoofing.

## Tech Stack
- **Language:** Go 1.21+
- **Transport:** Gorilla WebSocket (RFC 6455)
- **Caching/PubSub:** Redis
- **Database:** PostgreSQL
- **Security:** JWT, Bcrypt, HTML Sanitization