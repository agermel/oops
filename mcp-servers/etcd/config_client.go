package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type config struct {
	Endpoints   []string
	Username    string
	Password    string
	DialTimeout time.Duration
}

func loadConfig() config {
	cfg := config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: 5 * time.Second,
	}
	if value := os.Getenv("ETCD_ENDPOINTS"); value != "" {
		cfg.Endpoints = strings.Split(value, ",")
	}
	cfg.Username = os.Getenv("ETCD_USERNAME")
	cfg.Password = os.Getenv("ETCD_PASSWORD")
	if value := os.Getenv("ETCD_DIAL_TIMEOUT"); value != "" {
		if timeout, err := time.ParseDuration(value); err == nil {
			cfg.DialTimeout = timeout
		}
	}
	return cfg
}

var (
	client   *clientv3.Client
	clientMu sync.Mutex
)

func getClient() (*clientv3.Client, error) {
	clientMu.Lock()
	defer clientMu.Unlock()
	if client != nil {
		return client, nil
	}
	cfg := loadConfig()
	connected, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		Username:    cfg.Username,
		Password:    cfg.Password,
		DialTimeout: cfg.DialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("connect etcd: %w", err)
	}
	client = connected
	return client, nil
}
