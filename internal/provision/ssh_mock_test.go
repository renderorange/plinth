package provision

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

type commandRecorder struct {
	mu   sync.Mutex
	cmds []string
}

func (r *commandRecorder) add(cmd string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, cmd)
}

func (r *commandRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.cmds...)
}

func startMockSSHServer(t *testing.T) (addr string, hostKey ssh.PublicKey, cleanup func()) {
	addr, hostKey, cleanup, _ = startRecordingMockSSHServer(t)
	return addr, hostKey, cleanup
}

func startRecordingMockSSHServer(t *testing.T) (addr string, hostKey ssh.PublicKey, cleanup func(), commands func() []string) {
	t.Helper()

	pubKey, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating SSH key: %v", err)
	}

	hostKey, err = ssh.NewPublicKey(pubKey)
	if err != nil {
		t.Fatalf("deriving host public key: %v", err)
	}

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("creating signer: %v", err)
	}

	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	config.AddHostKey(signer)

	recorder := &commandRecorder{}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSSHConn(conn, config, recorder)
		}
	}()

	return listener.Addr().String(), hostKey, func() { listener.Close() }, recorder.list
}

func handleSSHConn(conn net.Conn, config *ssh.ServerConfig, recorder *commandRecorder) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()

	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}

		go func() {
			for req := range requests {
				if req.Type == "exec" {
					var payload struct {
						Command string
					}
					ssh.Unmarshal(req.Payload, &payload)
					recorder.add(payload.Command)

					var stdinDone sync.WaitGroup
					stdinDone.Add(1)
					go func() {
						defer stdinDone.Done()
						io.Copy(io.Discard, channel)
					}()

					output := "ok"
					if strings.Contains(payload.Command, "systemctl is-active") {
						output = "active"
					}

					channel.Write([]byte(output + "\n"))
					req.Reply(true, nil)
					channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					channel.CloseWrite()
					stdinDone.Wait()
					channel.Close()
				} else {
					req.Reply(false, nil)
				}
			}
		}()
	}
}
