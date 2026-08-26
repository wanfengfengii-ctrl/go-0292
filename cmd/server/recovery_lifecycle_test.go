package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"example.com/uhpc-wet-joint-traffic-release/internal/domain"
	"example.com/uhpc-wet-joint-traffic-release/internal/engine"
)

func TestModel_RecoveryIntegrityFailureKeepsReadonlyHTTPService(t *testing.T) {
	tmp := t.TempDir()
	dataPath := filepath.Join(tmp, "state.db")

	eng := engine.New(dataPath)
	if err := eng.RegisterSpan(domain.BridgeSpan{
		ID:              "existing-span",
		CoordinateScale: 1000,
		AllowedRecipes:  []string{"UHPC-1"},
		RuleDigest:      "rule-v1",
	}); err != nil {
		t.Fatalf("create durable snapshot: %v", err)
	}

	db, err := sql.Open("sqlite", dataPath)
	if err != nil {
		t.Fatalf("open snapshot database: %v", err)
	}
	if _, err := db.Exec("UPDATE snapshot SET digest = ? WHERE id = 1", "invalid-digest"); err != nil {
		_ = db.Close()
		t.Fatalf("corrupt snapshot digest: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close snapshot database: %v", err)
	}

	binary := filepath.Join(tmp, "server")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server command: %v\n%s", err, output)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test address: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release test address: %v", err)
	}

	var logs bytes.Buffer
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "ADDR="+addr, "DATA_PATH="+dataPath)
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server command: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		select {
		case <-exited:
		default:
			_ = cmd.Process.Kill()
			<-exited
		}
	}()

	client := &http.Client{Timeout: 200 * time.Millisecond}
	baseURL := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-exited:
			exited <- err
			t.Fatalf("server exited after recovery integrity failure: %v\n%s", err, logs.String())
		default:
		}
		resp, err := client.Get(baseURL + "/v1/health")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not begin serving after recovery integrity failure: %v\n%s", err, logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "health exposes recovery integrity failure",
			method:     http.MethodGet,
			path:       "/v1/health",
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "RECOVERY_INTEGRITY_FAILED",
		},
		{
			name:       "write remains rejected",
			method:     http.MethodPost,
			path:       "/v1/spans",
			body:       `{"id":"new-span","coordinate_scale":1000,"allowed_recipes":["UHPC-1"],"rule_digest":"rule-v1"}`,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "RECOVERY_INTEGRITY_FAILED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, baseURL+tt.path, bytes.NewBufferString(tt.body))
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, tt.wantStatus, body)
			}
			var payload struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode response %q: %v", body, err)
			}
			if payload.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q; body=%s", payload.Code, tt.wantCode, body)
			}
		})
	}
}
