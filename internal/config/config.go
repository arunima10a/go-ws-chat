package main

import (
	"flag"
	"os"
)

type Config struct {
	Port      string
	RedisAddr string
	DBURL     string
}

func LoadConfig() *Config {
	cfg := &Config{}

	flag.StringVar(&cfg.Port, "port", "8080", "Server port")
	flag.StringVar(&cfg.RedisAddr, "redis", "localhost:6379", "Redis address")
	flag.StringVar(&cfg.DBURL, "db", "postgres://user:pass@localhost:5432/chat", "Postgres URL")
	flag.Parse()

	if envPort := os.Getenv("PORT"); envPort != "" {
		cfg.Port = envPort
	}

	return cfg
}
