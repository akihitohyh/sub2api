package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// The production service and request builders run unchanged. Only the lowest
// HTTP port is an ordinary loopback client, with no external network access.
type smartProtocolNetworkHTTP struct{ client *http.Client }

func (u smartProtocolNetworkHTTP) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(r)
}
func (u smartProtocolNetworkHTTP) DoWithTLS(r *http.Request, p string, a int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, a, n)
}

type smartProtocolNetwork struct {
	t         *testing.T
	cfg       *config.Config
	svc       *OpenAIGatewayService
	account   *Account
	upstream  *httptest.Server
	ingress   *httptest.Server
	ctx       context.Context
	cancel    context.CancelFunc
	httpCalls atomic.Int32
	wsCalls   atomic.Int32
	active    atomic.Int32
	mu        sync.Mutex
	bodies    [][]byte
	results   chan *OpenAIForwardResult
	errors    chan error
	timings   chan requesttiming.Snapshot
	onTurn    func(int)
	fail      string
}

func newSmartProtocolNetwork(t *testing.T, mode string, known bool, fail string) *smartProtocolNetwork {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	f := &smartProtocolNetwork{t: t, ctx: ctx, cancel: cancel, results: make(chan *OpenAIForwardResult, 32), errors: make(chan error, 16), timings: make(chan requesttiming.Snapshot, 16), fail: fail}
	cfg := &config.Config{}
	f.cfg = cfg
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.SmartProtocol = config.GatewayOpenAIWSSmartProtocolConfig{Enabled: true, Mode: mode}
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 4
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 4
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	f.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.active.Add(1)
		defer f.active.Add(-1)
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			conn, err := coderws.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			for {
				_, body, err := conn.Read(ctx)
				if err != nil {
					return
				}
				f.wsCalls.Add(1)
				f.remember(body)
				for _, event := range f.events(body) {
					if err := conn.Write(ctx, coderws.MessageText, []byte(event)); err != nil {
						return
					}
				}
				if fail == "eof" {
					return
				}
			}
		}
		f.httpCalls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		f.remember(body)
		if fail == "http_error" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"synthetic unavailable"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range f.events(body) {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
			w.(http.Flusher).Flush()
		}
		if fail == "cancel" {
			select {
			case <-r.Context().Done():
			case <-ctx.Done():
			}
			return
		}
	}))
	f.account = &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 4, Credentials: map[string]any{"api_key": "synthetic-only", "base_url": f.upstream.URL}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
	if known {
		f.account.Extra["openai_responses_supported"] = true
	}
	pool := newOpenAIWSConnPool(cfg)
	f.svc = &OpenAIGatewayService{cfg: cfg, httpUpstream: smartProtocolNetworkHTTP{f.upstream.Client()}, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), openaiWSPool: pool, openaiWSStateStore: NewOpenAIWSStateStore(nil)}
	f.ingress = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			f.errors <- err
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(64 * 1024 * 1024)
		_, first, err := conn.Read(ctx)
		if err != nil {
			f.errors <- err
			return
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = r.Clone(ctx)
		collector := requesttiming.New(time.Now(), 0)
		turnCtx := requesttiming.With(ctx, collector)
		hooks := &OpenAIWSIngressHooks{AfterTurn: func(turn int, result *OpenAIForwardResult, _ error) {
			if result != nil {
				f.results <- result
			}
			if f.onTurn != nil {
				f.onTurn(turn)
			}
		}}
		err = f.svc.ProxyResponsesWebSocketFromClient(turnCtx, c, conn, f.account, "synthetic-only", first, hooks)
		collector.WhenFinished(func(s requesttiming.Snapshot) { f.timings <- s })
		collector.Finish(200, false)
		f.errors <- err
	}))
	t.Cleanup(func() { cancel(); f.ingress.Close(); pool.Close(); f.upstream.Close() })
	return f
}
func (f *smartProtocolNetwork) remember(body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies = append(f.bodies, append([]byte(nil), body...))
}
func (f *smartProtocolNetwork) events(body []byte) []string {
	seed := gjson.GetBytes(body, "input").String()
	if seed == "" {
		seed = "tools"
	}
	if len(seed) > 64 {
		seed = "large"
	}
	id := "resp_" + seed
	events := []string{fmt.Sprintf(`{"type":"response.created","response":{"id":%q}}`, id)}
	if f.fail == "eof" || f.fail == "cancel" {
		return events
	}
	if f.fail == "event_error" {
		return append(events, `{"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":{"code":"server_error","message":"synthetic failure"},"usage":{"input_tokens":3,"output_tokens":2}}}`)
	}
	events = append(events, `{"type":"response.function_call_arguments.delta","delta":"{}","item_id":"fc_synthetic","output_index":0}`, `{"type":"response.output_item.done","item":{"id":"fc_synthetic","type":"function_call","call_id":"call_synthetic","name":"lookup","arguments":"{}"}}`, `{"type":"response.output_text.delta","delta":"ok"}`)
	events = append(events, fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":"gpt-5.1","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":7}}}`, id))
	return events
}
func (f *smartProtocolNetwork) dial() *coderws.Conn {
	f.t.Helper()
	c, _, err := coderws.Dial(f.ctx, "ws"+strings.TrimPrefix(f.ingress.URL, "http"), nil)
	require.NoError(f.t, err)
	f.t.Cleanup(func() { _ = c.CloseNow() })
	return c
}
func (f *smartProtocolNetwork) send(c *coderws.Conn, input, previous string, cap int) {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"type": "response.create", "model": "gpt-5.1", "input": input, "previous_response_id": previous, "max_output_tokens": cap, "store": true})
	require.NoError(f.t, c.Write(f.ctx, coderws.MessageText, body))
}
func (f *smartProtocolNetwork) terminal(c *coderws.Conn) string {
	f.t.Helper()
	for {
		_, event, err := c.Read(f.ctx)
		require.NoError(f.t, err)
		if gjson.GetBytes(event, "type").String() == "response.completed" {
			return gjson.GetBytes(event, "response.id").String()
		}
	}
}
func (f *smartProtocolNetwork) result() *OpenAIForwardResult {
	f.t.Helper()
	select {
	case r := <-f.results:
		return r
	case <-f.ctx.Done():
		f.t.Fatal("missing actual turn result")
		return nil
	}
}

func TestSmartProtocolHTTPFirstSession(t *testing.T) {
	f := newSmartProtocolNetwork(t, "auto", true, "")
	c := f.dial()
	changed := make(chan struct{})
	f.onTurn = func(turn int) {
		if turn == 1 {
			f.cfg.Gateway.OpenAIWS.SmartProtocol.Mode = "ws"
			close(changed)
		}
	}
	f.send(c, "first", "", 8000)
	id := f.terminal(c)
	<-changed
	first := f.result()
	require.Equal(t, "http", first.UpstreamProtocol)
	require.NotNil(t, first.FirstUpstreamEventMs)
	require.NotNil(t, first.FirstTokenMs)
	require.LessOrEqual(t, *first.FirstUpstreamEventMs, *first.FirstTokenMs)
	f.send(c, "second", id, 100)
	f.terminal(c)
	second := f.result()
	require.Equal(t, "http", second.UpstreamProtocol)
	require.EqualValues(t, 2, f.httpCalls.Load())
	require.Zero(t, f.wsCalls.Load())
	f.mu.Lock()
	body := append([]byte(nil), f.bodies[1]...)
	f.mu.Unlock()
	require.Equal(t, id, gjson.GetBytes(body, "previous_response_id").String())
	require.Equal(t, "second", gjson.GetBytes(body, "input").String())
	_ = c.CloseNow()
	select {
	case err := <-f.errors:
		require.NoError(t, err)
	case <-f.ctx.Done():
		t.Fatal("session cleanup did not finish")
	}
	timing := <-f.timings
	require.NotNil(t, timing.ProtocolDecision)
	require.Equal(t, "ws", timing.ProtocolDecision.Ingress)
	require.Equal(t, "http", timing.ProtocolDecision.Upstream)
	// The changed global manual control applies to a new connection only.
	next := f.dial()
	f.send(next, "new", "", 8000)
	f.terminal(next)
	require.Equal(t, "ws", f.result().UpstreamProtocol)
	require.EqualValues(t, 1, f.wsCalls.Load())
}
func TestSmartProtocolNetworkModes(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		known      bool
		cap        int
		protocol   string
	}{{"ws_first", "auto", true, 100, "ws"}, {"unknown", "auto", false, 8000, "ws"}, {"negative_http", "http", false, 8000, "ws"}, {"manual_ws", "ws", true, 8000, "ws"}, {"manual_http", "http", true, 100, "http"}, {"shadow", "shadow", true, 8000, "ws"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSmartProtocolNetwork(t, tc.mode, tc.known, "")
			c := f.dial()
			f.send(c, "first", "", tc.cap)
			id := f.terminal(c)
			require.Equal(t, tc.protocol, f.result().UpstreamProtocol)
			f.send(c, "second", id, 8000)
			f.terminal(c)
			require.Equal(t, tc.protocol, f.result().UpstreamProtocol)
			if tc.protocol == "ws" {
				require.EqualValues(t, 2, f.wsCalls.Load())
				require.Zero(t, f.httpCalls.Load())
			} else {
				require.EqualValues(t, 2, f.httpCalls.Load())
				require.Zero(t, f.wsCalls.Load())
			}
		})
	}
}
func TestSmartProtocolNoReplayAndCleanup(t *testing.T) {
	for _, mode := range []string{"http", "ws"} {
		for _, failure := range []string{"eof", "event_error", "cancel", "http_error"} {
			if mode == "ws" && failure == "http_error" {
				continue
			}
			t.Run(mode+"_"+failure, func(t *testing.T) {
				f := newSmartProtocolNetwork(t, mode, true, failure)
				c := f.dial()
				f.send(c, "failure", "", 100)
				if failure == "cancel" {
					require.Eventually(t, func() bool { return f.httpCalls.Load()+f.wsCalls.Load() == 1 }, 2*time.Second, time.Millisecond)
					_ = c.CloseNow()
				} else {
					for {
						_, _, err := c.Read(f.ctx)
						if err != nil {
							break
						}
					}
				}
				select {
				case <-f.errors:
				case <-f.ctx.Done():
					t.Fatal("failed turn cleanup stalled")
				}
				require.EqualValues(t, 1, f.httpCalls.Load()+f.wsCalls.Load(), "submitted request must execute exactly once")
			})
		}
	}
}
func TestSmartProtocolForeignContinuation(t *testing.T) {
	f := newSmartProtocolNetwork(t, "http", true, "")
	c := f.dial()
	f.send(c, "first", "", 100)
	f.terminal(c)
	f.result()
	f.send(c, "second", "resp_foreign", 100)
	_, _, err := c.Read(f.ctx)
	require.Error(t, err)
	require.EqualValues(t, 1, f.httpCalls.Load())
}
func TestSmartProtocolConcurrentSticky(t *testing.T) {
	f := newSmartProtocolNetwork(t, "auto", true, "")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, _, err := coderws.Dial(f.ctx, "ws"+strings.TrimPrefix(f.ingress.URL, "http"), nil)
			if err != nil {
				t.Error("loopback dial failed")
				return
			}
			defer c.CloseNow()
			for turn := 0; turn < 2; turn++ {
				cap := 100
				if i%2 == 0 {
					cap = 8000
				}
				raw := fmt.Sprintf(`{"type":"response.create","model":"gpt-5.1","input":"session%dturn%d","max_output_tokens":%d,"store":true}`, i, turn, cap)
				if err := c.Write(f.ctx, coderws.MessageText, []byte(raw)); err != nil {
					t.Error("loopback write failed")
					return
				}
				for {
					_, body, err := c.Read(f.ctx)
					if err != nil {
						t.Error("loopback stream failed")
						return
					}
					if gjson.GetBytes(body, "type").String() == "response.completed" {
						break
					}
				}
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 4, f.httpCalls.Load())
	require.EqualValues(t, 4, f.wsCalls.Load())
}

func TestSmartProtocolLegacy15MiB(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("enabled_%t", enabled), func(t *testing.T) {
			f := newSmartProtocolNetwork(t, "ws", true, "")
			f.cfg.Gateway.OpenAIWS.SmartProtocol.Enabled = enabled
			c := f.dial()
			f.send(c, strings.Repeat("x", 15*1024*1024), "", 100)
			f.terminal(c)
			result := f.result()
			require.Equal(t, "http", result.UpstreamProtocol)
			require.EqualValues(t, 1, f.httpCalls.Load())
			require.Zero(t, f.wsCalls.Load())
		})
	}
}
