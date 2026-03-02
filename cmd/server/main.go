package main

import (
	"context"
	"database/sql"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"

	"github.com/arunima10a/go-ws-chat/internal/chat"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"

	"golang.org/x/crypto/bcrypt"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:    4096,
	WriteBufferSize:   4096,
	EnableCompression: true,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}
type ApiHandler struct {
	db  *sql.DB
	hub *chat.Hub
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}


func main() {
	if err := godotenv.Load(); err != nil {
		slog.Warn("No .env file found, using system defaults")
	}

	redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
	dbURL := getEnv("DATABASE_URL", "postgres://chat_user:chat_pass@localhost:5432/chat_db?sslmode=disable")
	port := getEnv("PORT", "8080")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	rbd := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}

	hub := chat.NewHub(rbd, db)
	go hub.Run()

	api := &ApiHandler{db: db, hub: hub}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})
	mux.HandleFunc("/signup", api.handleSignup)
    mux.HandleFunc("/login", api.handleLogin)
    
    mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
        serveWs(api.hub, w, r)
    })
	wrappedMux := chat.LoggingMiddleware(mux)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: wrappedMux,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	go func() {
		slog.Info("Server Starting", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen error", "error", err)
			os.Exit(1)
		}
	}()

	<-stop

	slog.Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown:", "error", err)
	}

	if err := rbd.Close(); err != nil {
		slog.Error("Failed to close Redis", "error", err)
	}
	slog.Info("Server exited cleanly")

}

func serveWs(h *chat.Hub, w http.ResponseWriter, r *http.Request) {

	token := r.URL.Query().Get("token")

	username, err := chat.ValidateToken(token)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Upgrade error", "error", err)
		return
	}
	client := chat.NewClient(h, conn, username)

	go client.WritePump()
	go client.ReadPump()

	go func() {
		time.Sleep(100 * time.Millisecond)
		client.LoadHistoryForRoom("general")

	}()
}
func (h *ApiHandler) handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.ParseForm()
	username := r.FormValue("username")
	password := r.FormValue("password")

	slog.Info("Signup attempt", "user", username, "pass_len", len(password))

	if username == "" || password == "" {
		http.Error(w, "Username and password required", http.StatusBadRequest)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	_, err = h.db.Exec("INSERT INTO users (username, password_hash) VALUES ($1, $2)", username, string(hashedPassword))
	if err != nil {
		slog.Error("Database Error", "err", err)
		http.Error(w, "User already exists", http.StatusConflict)
		return
	}

	slog.Info("User created", "user", username)
	w.Write([]byte("Signup successful"))
}

func (h *ApiHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.ParseForm()
	username := r.FormValue("username")
	password := r.FormValue("password")

	slog.Info("Login attempt", "user", username)

	var dbHash string
	err := h.db.QueryRow("SELECT password_hash FROM users WHERE username = $1", username).Scan(&dbHash)
	if err != nil {
		slog.Error("Login DB Error", "err", err)
		http.Error(w, "User not found. Please sign up first.", http.StatusUnauthorized)
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(dbHash), []byte(password))
	if err != nil {
		http.Error(w, "Invalid password", http.StatusUnauthorized)
		return
	}

	token, err := chat.CreateToken(username)
	if err != nil {
		http.Error(w, "Token error", http.StatusInternalServerError)
		return
	}

	w.Write([]byte(token))
}