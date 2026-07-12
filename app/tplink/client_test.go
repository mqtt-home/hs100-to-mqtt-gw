package tplink

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeHandler decides how the fake server responds to a decoded request.
// It returns the response bytes to write back (post-decryption); the
// framework encrypts and length-prefixes them. If splitAt > 0, the encoded
// frame is written in two chunks with a small sleep between them, to
// exercise the TCP-segmentation buffering logic.
type fakeHandler func(reqPlaintext []byte) (respPlaintext []byte, splitAt int)

func startFakeServer(t *testing.T, handler fakeHandler) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	addr := ln.Addr().(*net.TCPAddr)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				// Read 4-byte length prefix.
				hdr := make([]byte, 4)
				if _, err := io.ReadFull(conn, hdr); err != nil {
					return
				}
				n := binary.BigEndian.Uint32(hdr)
				body := make([]byte, n)
				if _, err := io.ReadFull(conn, body); err != nil {
					return
				}
				plain, err := Decode(body)
				if err != nil {
					return
				}
				resp, splitAt := handler(plain)
				frame := Encode(resp)
				if splitAt > 0 && splitAt < len(frame) {
					_, _ = conn.Write(frame[:splitAt])
					time.Sleep(20 * time.Millisecond)
					_, _ = conn.Write(frame[splitAt:])
				} else {
					_, _ = conn.Write(frame)
				}
			}(conn)
		}
	}()
	return addr.IP.String(), addr.Port
}

// rawResponse allows the fake server to return arbitrary bytes instead of
// a real JSON response (used for the malformed-JSON test).
func startFakeServerRaw(t *testing.T, respRaw []byte) (string, int) {
	return startFakeServer(t, func(_ []byte) ([]byte, int) {
		return respRaw, 0
	})
}

func TestPollHS100(t *testing.T) {
	handler := func(req []byte) ([]byte, int) {
		if !strings.Contains(string(req), `"get_sysinfo"`) {
			t.Errorf("request missing get_sysinfo: %s", req)
		}
		if strings.Contains(string(req), `"emeter"`) {
			t.Errorf("HS100 request should not include emeter block: %s", req)
		}
		resp := `{"system":{"get_sysinfo":{"err_code":0,"model":"HS100(EU)","alias":"Living","deviceId":"ABC","sw_ver":"1.0","hw_ver":"1.0","feature":"TIM","relay_state":1,"mac":"00:11:22:33:44:55"}}}`
		return []byte(resp), 0
	}
	host, port := startFakeServer(t, handler)
	c := &Client{Host: host, Port: port}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sys, em, err := c.Poll(ctx, false)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if em != nil {
		t.Fatalf("expected nil emeter, got %+v", em)
	}
	if sys.Model != "HS100(EU)" || sys.Feature != "TIM" {
		t.Fatalf("unexpected sysinfo: %+v", sys)
	}
	if !sys.RelayOn() {
		t.Fatalf("expected relay on")
	}
	if HasEmeterFeature(sys) {
		t.Fatalf("HS100 should not report emeter feature")
	}
}

func TestPollHS110(t *testing.T) {
	handler := func(req []byte) ([]byte, int) {
		if !strings.Contains(string(req), `"get_sysinfo"`) || !strings.Contains(string(req), `"get_realtime"`) {
			t.Errorf("HS110 request must include both modules: %s", req)
		}
		resp := `{"system":{"get_sysinfo":{"err_code":0,"model":"HS110(EU)","alias":"Fridge","deviceId":"XYZ","sw_ver":"1.5","hw_ver":"2.0","feature":"TIM:ENE","relay_state":0}},"emeter":{"get_realtime":{"err_code":0,"current_ma":183,"voltage_mv":230500,"power_mw":41200,"total_wh":12400}}}`
		return []byte(resp), 0
	}
	host, port := startFakeServer(t, handler)
	c := &Client{Host: host, Port: port}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sys, em, err := c.Poll(ctx, true)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if !HasEmeterFeature(sys) {
		t.Fatalf("expected HS110 to advertise emeter feature")
	}
	if sys.RelayOn() {
		t.Fatalf("expected relay off")
	}
	if em == nil {
		t.Fatalf("expected non-nil emeter")
	}
	if em.PowerW != 41.2 || em.VoltageV != 230.5 || em.CurrentA != 0.183 || em.EnergyKwh != 12.4 {
		t.Fatalf("emeter normalisation wrong: %+v", em)
	}
}

func TestPollMultiSegment(t *testing.T) {
	handler := func(_ []byte) ([]byte, int) {
		resp := `{"system":{"get_sysinfo":{"err_code":0,"model":"HS100","alias":"a","deviceId":"d","sw_ver":"1","hw_ver":"1","feature":"TIM","relay_state":1}}}`
		// Encoded frame length = 4 + len(resp). Split half-way through.
		return []byte(resp), 4 + len(resp)/2
	}
	host, port := startFakeServer(t, handler)
	c := &Client{Host: host, Port: port}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sys, _, err := c.Poll(ctx, false)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if sys.Alias != "a" {
		t.Fatalf("unexpected sysinfo: %+v", sys)
	}
}

func TestPollMalformedJSON(t *testing.T) {
	host, port := startFakeServerRaw(t, []byte("not json at all"))
	c := &Client{Host: host, Port: port}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := c.Poll(ctx, false); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestPollMissingRelayState(t *testing.T) {
	handler := func(_ []byte) ([]byte, int) {
		resp := `{"system":{"get_sysinfo":{"err_code":0,"model":"HS100","feature":"TIM"}}}`
		return []byte(resp), 0
	}
	host, port := startFakeServer(t, handler)
	c := &Client{Host: host, Port: port}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := c.Poll(ctx, false)
	if err == nil {
		t.Fatal("expected error for missing relay_state")
	}
	if !strings.Contains(err.Error(), "relay_state") {
		t.Fatalf("expected error to mention relay_state, got: %v", err)
	}
}

func TestPollNonZeroErrCode(t *testing.T) {
	handler := func(_ []byte) ([]byte, int) {
		resp := `{"system":{"get_sysinfo":{"err_code":-1,"err_msg":"invalid argument"}}}`
		return []byte(resp), 0
	}
	host, port := startFakeServer(t, handler)
	c := &Client{Host: host, Port: port}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := c.Poll(ctx, false)
	if err == nil {
		t.Fatal("expected error for non-zero err_code")
	}
	if !strings.Contains(err.Error(), "-1") {
		t.Fatalf("expected error to mention code -1, got: %v", err)
	}
}

func TestPollEmeterErrCode(t *testing.T) {
	handler := func(_ []byte) ([]byte, int) {
		resp := `{"system":{"get_sysinfo":{"err_code":0,"model":"HS110","feature":"TIM:ENE","relay_state":1}},"emeter":{"get_realtime":{"err_code":-2,"err_msg":"nope"}}}`
		return []byte(resp), 0
	}
	host, port := startFakeServer(t, handler)
	c := &Client{Host: host, Port: port}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := c.Poll(ctx, true)
	if err == nil {
		t.Fatal("expected error for non-zero emeter err_code")
	}
}

func TestSetRelay(t *testing.T) {
	t.Run("success on", func(t *testing.T) {
		handler := func(req []byte) ([]byte, int) {
			var parsed map[string]map[string]map[string]int
			if err := json.Unmarshal(req, &parsed); err != nil {
				t.Fatalf("fake server: parse request: %v", err)
			}
			state, ok := parsed["system"]["set_relay_state"]["state"]
			if !ok {
				t.Fatalf("request missing state field: %s", req)
			}
			if state != 1 {
				t.Errorf("expected state=1, got %d", state)
			}
			return []byte(`{"system":{"set_relay_state":{"err_code":0}}}`), 0
		}
		host, port := startFakeServer(t, handler)
		c := &Client{Host: host, Port: port}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.SetRelay(ctx, true); err != nil {
			t.Fatalf("SetRelay: %v", err)
		}
	})

	t.Run("success off", func(t *testing.T) {
		var seen int = -1
		handler := func(req []byte) ([]byte, int) {
			var parsed map[string]map[string]map[string]int
			_ = json.Unmarshal(req, &parsed)
			seen = parsed["system"]["set_relay_state"]["state"]
			return []byte(`{"system":{"set_relay_state":{"err_code":0}}}`), 0
		}
		host, port := startFakeServer(t, handler)
		c := &Client{Host: host, Port: port}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := c.SetRelay(ctx, false); err != nil {
			t.Fatalf("SetRelay: %v", err)
		}
		if seen != 0 {
			t.Fatalf("expected state=0, got %d", seen)
		}
	})

	t.Run("error", func(t *testing.T) {
		handler := func(_ []byte) ([]byte, int) {
			return []byte(`{"system":{"set_relay_state":{"err_code":-1,"err_msg":"denied"}}}`), 0
		}
		host, port := startFakeServer(t, handler)
		c := &Client{Host: host, Port: port}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := c.SetRelay(ctx, true)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "denied") {
			t.Fatalf("expected err_msg in error, got: %v", err)
		}
	})
}

func TestClientDefaults(t *testing.T) {
	c := &Client{}
	if c.port() != 9999 {
		t.Fatalf("default port: got %d, want 9999", c.port())
	}
	if c.timeout() != 5*time.Second {
		t.Fatalf("default timeout: got %v, want 5s", c.timeout())
	}
}

func TestPollDialError(t *testing.T) {
	// Reserve a port then close it — dial should fail.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()
	c := &Client{Host: "127.0.0.1", Port: addr.Port, Timeout: 200 * time.Millisecond}
	ctx := context.Background()
	_, _, err = c.Poll(ctx, false)
	if err == nil {
		t.Fatal("expected dial error")
	}
	// Sanity: no wrapped net.OpError panic path.
	var nerr *net.OpError
	_ = errors.As(err, &nerr)
	_ = strconv.Itoa(addr.Port)
}
