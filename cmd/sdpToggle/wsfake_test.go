package main

// Minimal in-test WebSocket server for fake CDP responses.
// Implements just enough of the handshake + framing for our client.

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type wsUpgrader struct{}

func (u wsUpgrader) serveEval(w http.ResponseWriter, r *http.Request, match, value string) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", 500)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "not ws", 400)
		return
	}
	h := sha1.New()
	h.Write([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(h.Sum(nil))
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	rw.Flush()

	// read one text frame, reply with CDP response
	for {
		payload, op, err := readFrame(rw.Reader)
		if err != nil {
			return
		}
		_ = payload
		if op == 8 { // close
			return
		}
		// decode the Runtime.evaluate request, build a response
		var req struct {
			ID int `json:"id"`
		}
		// payload may be masked? server side receives unmasked from our client (coder/websocket masks client->server; we must unmask) — readFrame handles it.
		_ = json.Unmarshal(payload, &req)
		if req.ID == 0 {
			continue
		}
		resp := map[string]interface{}{
			"id": req.ID,
			"result": map[string]interface{}{
				"result": map[string]interface{}{"type": "string", "value": value},
			},
		}
		b, _ := json.Marshal(resp)
		writeFrame(conn, b)
	}
}

func readFrame(r *bufio.Reader) ([]byte, byte, error) {
	var hdr [2]byte
	if _, err := io_ReadFull(r, hdr[:]); err != nil {
		return nil, 0, err
	}
	op := hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	length := uint64(hdr[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io_ReadFull(r, ext[:]); err != nil {
			return nil, 0, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io_ReadFull(r, ext[:]); err != nil {
			return nil, 0, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	var mask [4]byte
	if masked {
		if _, err := io_ReadFull(r, mask[:]); err != nil {
			return nil, 0, err
		}
	}
	payload := make([]byte, length)
	if _, err := io_ReadFull(r, payload); err != nil {
		return nil, 0, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return payload, op, nil
}

func io_ReadFull(r *bufio.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		b, err := r.ReadByte()
		if err != nil {
			return n, err
		}
		buf[n] = b
		n++
	}
	return n, nil
}

func writeFrame(conn net.Conn, payload []byte) {
	var hdr []byte
	hdr = append(hdr, 0x81) // FIN + text
	l := len(payload)
	switch {
	case l < 126:
		hdr = append(hdr, byte(l))
	case l <= 0xFFFF:
		hdr = append(hdr, 126, byte(l>>8), byte(l))
	default:
		hdr = append(hdr, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(l))
		hdr = append(hdr, ext[:]...)
	}
	conn.Write(append(hdr, payload...))
}

var _ = strings.Contains

// serveFlow routes Runtime.evaluate to per-command scripted responses by
// looking for the IPC command name in the expression.
func (u wsUpgrader) serveFlow(w http.ResponseWriter, r *http.Request, responses map[string]string, state *atomic.Value) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "no hijack", 500)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "not ws", 400)
		return
	}
	h := sha1.New()
	h.Write([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(h.Sum(nil))
	conn, rw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept)
	rw.Flush()
	for {
		payload, op, err := readFrame(rw.Reader)
		if err != nil {
			return
		}
		if op == 8 {
			return
		}
		var req struct {
			ID     int `json:"id"`
			Params struct {
				Expression string `json:"expression"`
			} `json:"params"`
		}
		json.Unmarshal(payload, &req)
		if req.ID == 0 {
			continue
		}
		var value string
		matched := false
		for cmd, resp := range responses {
			if strings.Contains(req.Params.Expression, "'"+cmd+"'") {
				value = resp
				matched = true
				break
			}
		}
		if !matched {
			value = `{"code":"-1","msg":"unmocked"}`
		}
		resp := map[string]interface{}{
			"id": req.ID,
			"result": map[string]interface{}{
				"result": map[string]interface{}{"type": "string", "value": value},
			},
		}
		b, _ := json.Marshal(resp)
		writeFrame(conn, b)
	}
}
