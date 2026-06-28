// etcd-mcp-server is a Model Context Protocol (MCP) server that exposes
// etcd v3 key-value operations as MCP tools. It connects to an etcd cluster
// configured via environment variables and communicates over stdio.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// ---- configuration from environment ----

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
	if v := os.Getenv("ETCD_ENDPOINTS"); v != "" {
		cfg.Endpoints = strings.Split(v, ",")
	}
	cfg.Username = os.Getenv("ETCD_USERNAME")
	cfg.Password = os.Getenv("ETCD_PASSWORD")
	if v := os.Getenv("ETCD_DIAL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.DialTimeout = d
		}
	}
	return cfg
}

// ---- etcd client (lazy connect) ----

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
	c, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		Username:    cfg.Username,
		Password:    cfg.Password,
		DialTimeout: cfg.DialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("connect etcd: %w", err)
	}
	client = c
	return client, nil
}

// ---- helpers ----

func textResult(format string, args ...any) *mcp.CallToolResult {
	return mcp.NewToolResultText(fmt.Sprintf(format, args...))
}

func errorResult(format string, args ...any) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(format, args...))
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult("json marshal: %v", err), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

func ctxWithTimeout(parent context.Context, seconds int) (context.Context, context.CancelFunc) {
	if seconds <= 0 {
		seconds = 10
	}
	return context.WithTimeout(parent, time.Duration(seconds)*time.Second)
}

// ---- main ----

func main() {
	s := server.NewMCPServer(
		"etcd-mcp-server",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	registerHealth(s)
	registerGet(s)
	registerPut(s)
	registerDelete(s)
	registerList(s)
	registerWatch(s)
	registerStatus(s)
	registerLease(s)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "etcd-mcp-server: %v\n", err)
		os.Exit(1)
	}
}

// ---- etcd_health ----

func registerHealth(s *server.MCPServer) {
	tool := mcp.NewTool("etcd_health",
		mcp.WithDescription("Check etcd cluster health by calling the maintenance status endpoint"),
	)
	s.AddTool(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		c, err := getClient()
		if err != nil {
			return errorResult("connect: %v", err), nil
		}
		ctx, cancel := ctxWithTimeout(ctx, 5)
		defer cancel()

		resp, err := c.Maintenance.Status(ctx, c.Endpoints()[0])
		if err != nil {
			return errorResult("health check failed: %v", err), nil
		}
		return jsonResult(map[string]any{
			"version":    resp.Version,
			"db_size_mb": float64(resp.DbSize) / (1024 * 1024),
			"leader":     resp.Header.MemberId,
			"raft_index": resp.RaftIndex,
			"raft_term":  resp.RaftTerm,
		})
	})
}

// ---- etcd_get ----

type getArgs struct {
	Key      string `json:"key,omitempty"`
	Prefix   bool   `json:"prefix,omitempty"`
	RangeEnd string `json:"range_end,omitempty"`
	Limit    int64  `json:"limit,omitempty"`
	KeysOnly bool   `json:"keys_only,omitempty"`
	Order    string `json:"order,omitempty"` // "asc" or "desc"
}

func registerGet(s *server.MCPServer) {
	tool := mcp.NewTool("etcd_get",
		mcp.WithDescription("Get key-value pairs from etcd. Supports prefix scan, range query, ordering, and key-only mode."),
		mcp.WithString("key", mcp.Description("Key to retrieve")),
		mcp.WithBoolean("prefix", mcp.Description("If true, treat key as a prefix and fetch all matching keys")),
		mcp.WithString("range_end", mcp.Description("End of key range for range queries")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of keys to return")),
		mcp.WithBoolean("keys_only", mcp.Description("If true, return keys without values")),
		mcp.WithString("order", mcp.Description("Sort order: 'asc' or 'desc'"), mcp.Enum("asc", "desc")),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args getArgs
		if err := decodeArgs(req.Params.Arguments, &args); err != nil {
			return errorResult("parse args: %v", err), nil
		}
		c, err := getClient()
		if err != nil {
			return errorResult("connect: %v", err), nil
		}
		ctx, cancel := ctxWithTimeout(ctx, 10)
		defer cancel()

		var opts []clientv3.OpOption
		if args.Prefix {
			opts = append(opts, clientv3.WithPrefix())
		}
		if args.RangeEnd != "" {
			opts = append(opts, clientv3.WithRange(args.RangeEnd))
		}
		if args.Limit > 0 {
			opts = append(opts, clientv3.WithLimit(args.Limit))
		}
		if args.KeysOnly {
			opts = append(opts, clientv3.WithKeysOnly())
		}
		switch strings.ToLower(args.Order) {
		case "desc":
			opts = append(opts, clientv3.WithSort(clientv3.SortByKey, clientv3.SortDescend))
		case "asc":
			opts = append(opts, clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend))
		}

		// First, get count
		countResp, err := c.Get(ctx, args.Key, append(opts, clientv3.WithCountOnly())...)
		if err != nil {
			return errorResult("get count: %v", err), nil
		}

		// Then, get data
		resp, err := c.Get(ctx, args.Key, opts...)
		if err != nil {
			return errorResult("get: %v", err), nil
		}

		type kvPair struct {
			Key            string `json:"key"`
			Value          string `json:"value,omitempty"`
			CreateRevision int64  `json:"create_revision"`
			ModRevision    int64  `json:"mod_revision"`
			Version        int64  `json:"version"`
		}
		kvs := make([]kvPair, 0, len(resp.Kvs))
		for _, kv := range resp.Kvs {
			kvp := kvPair{
				Key:            string(kv.Key),
				CreateRevision: kv.CreateRevision,
				ModRevision:    kv.ModRevision,
				Version:        kv.Version,
			}
			if !args.KeysOnly {
				kvp.Value = string(kv.Value)
			}
			kvs = append(kvs, kvp)
		}
		return jsonResult(map[string]any{
			"count":    countResp.Count,
			"kvs":      kvs,
			"more":     resp.More,
			"revision": resp.Header.Revision,
		})
	})
}

// ---- etcd_put ----

type putArgs struct {
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
	LeaseID int64  `json:"lease_id,omitempty"`
}

func registerPut(s *server.MCPServer) {
	tool := mcp.NewTool("etcd_put",
		mcp.WithDescription("Put a key-value pair into etcd. Optionally associate with a lease for auto-expiry."),
		mcp.WithString("key", mcp.Required(), mcp.Description("Key to write")),
		mcp.WithString("value", mcp.Required(), mcp.Description("Value to store")),
		mcp.WithNumber("lease_id", mcp.Description("Optional lease ID for TTL")),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args putArgs
		if err := decodeArgs(req.Params.Arguments, &args); err != nil {
			return errorResult("parse args: %v", err), nil
		}
		c, err := getClient()
		if err != nil {
			return errorResult("connect: %v", err), nil
		}
		ctx, cancel := ctxWithTimeout(ctx, 10)
		defer cancel()

		var opts []clientv3.OpOption
		if args.LeaseID > 0 {
			opts = append(opts, clientv3.WithLease(clientv3.LeaseID(args.LeaseID)))
		}
		resp, err := c.Put(ctx, args.Key, args.Value, opts...)
		if err != nil {
			return errorResult("put: %v", err), nil
		}
		return jsonResult(map[string]any{
			"key":      args.Key,
			"revision": resp.Header.Revision,
		})
	})
}

// ---- etcd_delete ----

type deleteArgs struct {
	Key    string `json:"key,omitempty"`
	Prefix bool   `json:"prefix,omitempty"`
}

func registerDelete(s *server.MCPServer) {
	tool := mcp.NewTool("etcd_delete",
		mcp.WithDescription("Delete key-value pairs from etcd. Supports prefix-based deletion."),
		mcp.WithString("key", mcp.Required(), mcp.Description("Key to delete")),
		mcp.WithBoolean("prefix", mcp.Description("If true, delete all keys with this prefix")),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args deleteArgs
		if err := decodeArgs(req.Params.Arguments, &args); err != nil {
			return errorResult("parse args: %v", err), nil
		}
		c, err := getClient()
		if err != nil {
			return errorResult("connect: %v", err), nil
		}
		ctx, cancel := ctxWithTimeout(ctx, 10)
		defer cancel()

		var opts []clientv3.OpOption
		if args.Prefix {
			opts = append(opts, clientv3.WithPrefix())
		}
		resp, err := c.Delete(ctx, args.Key, opts...)
		if err != nil {
			return errorResult("delete: %v", err), nil
		}
		return jsonResult(map[string]any{
			"deleted": resp.Deleted,
			"revision": resp.Header.Revision,
		})
	})
}

// ---- etcd_list ----

type listArgs struct {
	Prefix string `json:"prefix,omitempty"`
	Limit  int64  `json:"limit,omitempty"`
}

func registerList(s *server.MCPServer) {
	tool := mcp.NewTool("etcd_list",
		mcp.WithDescription("List all keys under a given prefix. Useful for exploring the keyspace."),
		mcp.WithString("prefix", mcp.Description("Key prefix to list under (default: '/' for root)")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of keys to return")),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args listArgs
		if err := decodeArgs(req.Params.Arguments, &args); err != nil {
			return errorResult("parse args: %v", err), nil
		}
		if args.Prefix == "" {
			args.Prefix = "/"
		}
		if args.Limit <= 0 || args.Limit > 1000 {
			args.Limit = 100
		}
		c, err := getClient()
		if err != nil {
			return errorResult("connect: %v", err), nil
		}
		ctx, cancel := ctxWithTimeout(ctx, 10)
		defer cancel()

		opts := []clientv3.OpOption{
			clientv3.WithPrefix(),
			clientv3.WithKeysOnly(),
			clientv3.WithLimit(args.Limit),
			clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend),
		}
		resp, err := c.Get(ctx, args.Prefix, opts...)
		if err != nil {
			return errorResult("list: %v", err), nil
		}

		keys := make([]string, 0, len(resp.Kvs))
		for _, kv := range resp.Kvs {
			keys = append(keys, string(kv.Key))
		}
		return jsonResult(map[string]any{
			"prefix":   args.Prefix,
			"keys":     keys,
			"count":    resp.Count,
			"more":     resp.More,
			"revision": resp.Header.Revision,
		})
	})
}

// ---- etcd_watch ----

type watchArgs struct {
	Key           string `json:"key,omitempty"`
	Prefix        bool   `json:"prefix,omitempty"`
	TimeoutSeconds int   `json:"timeout_seconds,omitempty"`
}

func registerWatch(s *server.MCPServer) {
	tool := mcp.NewTool("etcd_watch",
		mcp.WithDescription("Watch a key or prefix for changes. Returns the first batch of events observed within the timeout."),
		mcp.WithString("key", mcp.Required(), mcp.Description("Key or prefix to watch")),
		mcp.WithBoolean("prefix", mcp.Description("If true, watch all keys with this prefix")),
		mcp.WithNumber("timeout_seconds", mcp.Description("How long to watch (default: 10s, max: 60s)")),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args watchArgs
		if err := decodeArgs(req.Params.Arguments, &args); err != nil {
			return errorResult("parse args: %v", err), nil
		}
		if args.TimeoutSeconds <= 0 {
			args.TimeoutSeconds = 10
		}
		if args.TimeoutSeconds > 60 {
			args.TimeoutSeconds = 60
		}
		c, err := getClient()
		if err != nil {
			return errorResult("connect: %v", err), nil
		}
		ctx, cancel := ctxWithTimeout(ctx, args.TimeoutSeconds)
		defer cancel()

		var opts []clientv3.OpOption
		if args.Prefix {
			opts = append(opts, clientv3.WithPrefix())
		}

		type event struct {
			Type     string `json:"type"` // PUT or DELETE
			Key      string `json:"key"`
			Value    string `json:"value,omitempty"`
			Revision int64  `json:"revision"`
		}

		var events []event
		watchChan := c.Watch(ctx, args.Key, opts...)

		// Collect the first batch of events
		for wr := range watchChan {
			if wr.Err() != nil {
				return errorResult("watch error: %v", wr.Err()), nil
			}
			for _, ev := range wr.Events {
				events = append(events, event{
					Type:     ev.Type.String(),
					Key:      string(ev.Kv.Key),
					Value:    string(ev.Kv.Value),
					Revision: ev.Kv.ModRevision,
				})
			}
			break // return after first batch
		}

		if events == nil {
			events = []event{}
		}
		return jsonResult(map[string]any{
			"watched_key": args.Key,
			"prefix":      args.Prefix,
			"events":      events,
			"timeout":     args.TimeoutSeconds, // reports false if events arrived
		})
	})
}

// ---- etcd_status ----

func registerStatus(s *server.MCPServer) {
	tool := mcp.NewTool("etcd_status",
		mcp.WithDescription("Get etcd cluster status including member list and endpoint statuses."),
	)
	s.AddTool(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		c, err := getClient()
		if err != nil {
			return errorResult("connect: %v", err), nil
		}
		ctx, cancel := ctxWithTimeout(ctx, 10)
		defer cancel()

		// Member list
		membersResp, err := c.MemberList(ctx)
		if err != nil {
			return errorResult("member list: %v", err), nil
		}

		type member struct {
			ID       uint64   `json:"id"`
			Name     string   `json:"name"`
			PeerURLs []string `json:"peer_urls"`
			ClientURLs []string `json:"client_urls"`
		}
		members := make([]member, 0, len(membersResp.Members))
		for _, m := range membersResp.Members {
			members = append(members, member{
				ID:         m.ID,
				Name:       m.Name,
				PeerURLs:   m.PeerURLs,
				ClientURLs: m.ClientURLs,
			})
		}

		// Endpoint statuses
		type endpointStatus struct {
			Endpoint  string `json:"endpoint"`
			Version   string `json:"version"`
			DbSizeMB  float64 `json:"db_size_mb"`
			LeaderID  uint64 `json:"leader_id"`
			RaftIndex uint64 `json:"raft_index"`
			RaftTerm  uint64 `json:"raft_term"`
			IsLeader  bool   `json:"is_leader"`
		}
		var statuses []endpointStatus
		for _, ep := range c.Endpoints() {
			sr, err := c.Status(ctx, ep)
			if err != nil {
				statuses = append(statuses, endpointStatus{
					Endpoint: ep,
					Version:  fmt.Sprintf("error: %v", err),
				})
				continue
			}
			statuses = append(statuses, endpointStatus{
				Endpoint:  ep,
				Version:   sr.Version,
				DbSizeMB:  float64(sr.DbSize) / (1024 * 1024),
				LeaderID:  uint64(sr.Leader),
				RaftIndex: sr.RaftIndex,
				RaftTerm:  sr.RaftTerm,
				IsLeader:  sr.Header.MemberId == uint64(sr.Leader),
			})
		}

		return jsonResult(map[string]any{
			"endpoints": statuses,
			"members":   members,
		})
	})
}

// ---- etcd_lease ----

type leaseArgs struct {
	Action  string `json:"action,omitempty"` // grant, revoke, list, timetolive
	TTL     int64  `json:"ttl,omitempty"`    // for grant
	LeaseID int64  `json:"lease_id,omitempty"` // for revoke, timetolive
}

func registerLease(s *server.MCPServer) {
	tool := mcp.NewTool("etcd_lease",
		mcp.WithDescription("Manage etcd leases. Actions: grant (create a lease), revoke (delete a lease), list (list all leases), timetolive (get remaining TTL)."),
		mcp.WithString("action", mcp.Required(), mcp.Description("Action to perform"), mcp.Enum("grant", "revoke", "list", "timetolive")),
		mcp.WithNumber("ttl", mcp.Description("TTL in seconds (required for 'grant')")),
		mcp.WithNumber("lease_id", mcp.Description("Lease ID (required for 'revoke' and 'timetolive')")),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args leaseArgs
		if err := decodeArgs(req.Params.Arguments, &args); err != nil {
			return errorResult("parse args: %v", err), nil
		}
		c, err := getClient()
		if err != nil {
			return errorResult("connect: %v", err), nil
		}
		ctx, cancel := ctxWithTimeout(ctx, 10)
		defer cancel()

		switch args.Action {
		case "grant":
			if args.TTL <= 0 {
				return errorResult("grant requires ttl > 0"), nil
			}
			resp, err := c.Grant(ctx, args.TTL)
			if err != nil {
				return errorResult("grant: %v", err), nil
			}
			return jsonResult(map[string]any{
				"lease_id": int64(resp.ID),
				"ttl":      resp.TTL,
			})

		case "revoke":
			if args.LeaseID <= 0 {
				return errorResult("revoke requires lease_id"), nil
			}
			_, err := c.Revoke(ctx, clientv3.LeaseID(args.LeaseID))
			if err != nil {
				return errorResult("revoke: %v", err), nil
			}
			return textResult("lease %d revoked", args.LeaseID), nil

		case "list":
			resp, err := c.Leases(ctx)
			if err != nil {
				return errorResult("list leases: %v", err), nil
			}
			leases := make([]map[string]any, 0, len(resp.Leases))
			for _, ls := range resp.Leases {
				leases = append(leases, map[string]any{
					"id": int64(ls.ID),
				})
			}
			return jsonResult(map[string]any{
				"leases": leases,
			})

		case "timetolive":
			if args.LeaseID <= 0 {
				return errorResult("timetolive requires lease_id"), nil
			}
			resp, err := c.TimeToLive(ctx, clientv3.LeaseID(args.LeaseID))
			if err != nil {
				return errorResult("timetolive: %v", err), nil
			}
			return jsonResult(map[string]any{
				"lease_id":    args.LeaseID,
				"ttl_seconds": resp.TTL,
				"granted_ttl": resp.GrantedTTL,
				"keys_count":  len(resp.Keys),
			})

		default:
			return errorResult("unknown action: %q (use grant, revoke, list, or timetolive)", args.Action), nil
		}
	})
}

// decodeArgs unmarshals the raw arguments into the target struct.
func decodeArgs(raw any, target any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, target)
}
