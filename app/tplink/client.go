package tplink

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"
)

const (
	defaultPort    = 9999
	defaultTimeout = 5 * time.Second
)

// Client is a per-device TP-Link Smart Plug client. Each method opens a
// fresh short-lived TCP connection — plugs do not hold state between
// connections and short-lived sockets match the reference NodeJS
// implementation.
type Client struct {
	Host    string
	Port    int
	Timeout time.Duration
}

func (c *Client) port() int {
	if c.Port == 0 {
		return defaultPort
	}
	return c.Port
}

func (c *Client) timeout() time.Duration {
	if c.Timeout == 0 {
		return defaultTimeout
	}
	return c.Timeout
}

func (c *Client) address() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.port()))
}

// Poll performs a single-round-trip query. It always requests
// system.get_sysinfo; if wantEmeter is true it also requests
// emeter.get_realtime in the same JSON object so the plug returns both
// modules in one framed response. The returned *EmeterRealtime is nil iff
// wantEmeter is false.
func (c *Client) Poll(ctx context.Context, wantEmeter bool) (SysInfo, *EmeterRealtime, error) {
	var req string
	if wantEmeter {
		req = `{"system":{"get_sysinfo":{}},"emeter":{"get_realtime":{}}}`
	} else {
		req = `{"system":{"get_sysinfo":{}}}`
	}

	plaintext, err := c.exchange(ctx, []byte(req))
	if err != nil {
		return SysInfo{}, nil, err
	}

	var resp struct {
		System *struct {
			GetSysinfo *struct {
				SysInfo
				ErrCode int    `json:"err_code"`
				ErrMsg  string `json:"err_msg"`
			} `json:"get_sysinfo"`
		} `json:"system"`
		Emeter *struct {
			GetRealtime *struct {
				emeterRaw
				ErrCode int    `json:"err_code"`
				ErrMsg  string `json:"err_msg"`
			} `json:"get_realtime"`
		} `json:"emeter"`
	}
	if err := json.Unmarshal(plaintext, &resp); err != nil {
		return SysInfo{}, nil, fmt.Errorf("tplink: malformed JSON response: %w (raw=%.120q)", err, string(plaintext))
	}
	if resp.System == nil || resp.System.GetSysinfo == nil {
		return SysInfo{}, nil, fmt.Errorf("tplink: response missing system.get_sysinfo (raw=%.120q)", string(plaintext))
	}
	if resp.System.GetSysinfo.ErrCode != 0 {
		return SysInfo{}, nil, fmt.Errorf("tplink: sysinfo err_code=%d msg=%q", resp.System.GetSysinfo.ErrCode, resp.System.GetSysinfo.ErrMsg)
	}
	sys := resp.System.GetSysinfo.SysInfo
	// relay_state is required. A missing value is *not* silently defaulted
	// to 0 — that would let a broken response masquerade as "off".
	if sys.RelayState == nil {
		return SysInfo{}, nil, fmt.Errorf("tplink: sysinfo missing relay_state")
	}

	if !wantEmeter {
		return sys, nil, nil
	}
	if resp.Emeter == nil || resp.Emeter.GetRealtime == nil {
		return SysInfo{}, nil, fmt.Errorf("tplink: response missing emeter.get_realtime")
	}
	if resp.Emeter.GetRealtime.ErrCode != 0 {
		return SysInfo{}, nil, fmt.Errorf("tplink: emeter err_code=%d msg=%q", resp.Emeter.GetRealtime.ErrCode, resp.Emeter.GetRealtime.ErrMsg)
	}
	var em EmeterRealtime
	em.fromRaw(resp.Emeter.GetRealtime.emeterRaw)
	return sys, &em, nil
}

// SetRelay toggles the plug's relay. state=1 for on, state=0 for off.
func (c *Client) SetRelay(ctx context.Context, on bool) error {
	state := 0
	if on {
		state = 1
	}
	req := fmt.Sprintf(`{"system":{"set_relay_state":{"state":%d}}}`, state)
	plaintext, err := c.exchange(ctx, []byte(req))
	if err != nil {
		return err
	}
	var resp struct {
		System *struct {
			SetRelayState *struct {
				ErrCode int    `json:"err_code"`
				ErrMsg  string `json:"err_msg"`
			} `json:"set_relay_state"`
		} `json:"system"`
	}
	if err := json.Unmarshal(plaintext, &resp); err != nil {
		return fmt.Errorf("tplink: malformed JSON response: %w (raw=%.120q)", err, string(plaintext))
	}
	if resp.System == nil || resp.System.SetRelayState == nil {
		return fmt.Errorf("tplink: response missing system.set_relay_state (raw=%.120q)", string(plaintext))
	}
	if resp.System.SetRelayState.ErrCode != 0 {
		return fmt.Errorf("tplink: set_relay_state err_code=%d msg=%q", resp.System.SetRelayState.ErrCode, resp.System.SetRelayState.ErrMsg)
	}
	return nil
}

// exchange opens a TCP connection, writes a framed request, reads the
// framed response, and returns the decrypted plaintext.
func (c *Client) exchange(ctx context.Context, plaintext []byte) ([]byte, error) {
	timeout := c.timeout()
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.address())
	if err != nil {
		return nil, fmt.Errorf("tplink: dial %s: %w", c.address(), err)
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	frame := Encode(plaintext)
	if _, err := conn.Write(frame); err != nil {
		return nil, fmt.Errorf("tplink: write: %w", err)
	}

	// TCP-segmentation-safe read loop. The rolling-XOR key stream is
	// derived from the ciphertext itself, so decrypting a partial buffer
	// would corrupt the state. Accumulate until we have the full frame
	// (4-byte length prefix + declared body length), then decrypt once.
	var buf []byte
	tmp := make([]byte, 4096)
	var expected int = -1
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if expected < 0 && len(buf) >= 4 {
			expected = int(binary.BigEndian.Uint32(buf[:4]))
		}
		if expected >= 0 && len(buf)-4 >= expected {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tplink: read: %w", err)
		}
	}

	body := buf[4 : 4+expected]
	return Decode(body)
}
